package data

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/moomerman/loom/trace"
)

// queryStats counts a DB's statements by their text, so a query that
// is fast but runs a dozen times a second shows up as the busiest line
// of `loom queries`, which db.slow never would. Arguments are bound, so
// the text is the statement's shape, without its values.
type queryStats struct {
	mu     sync.Mutex
	shapes map[string]*queryStat
}

type queryStat struct {
	timing trace.Timing
	rows   uint64
	errors uint64
}

// maxShapes caps the statements counted; later ones count as "other",
// so SQL built with its values inline can't grow the stats without end.
const maxShapes = 500

// QueryStat is how often one statement ran on a DB since it opened, and
// how long it took.
type QueryStat struct {
	// Query is the statement on one line, cut after 500 bytes; never its
	// arguments. "other" counts statements past the first 500 seen.
	Query string `json:"query"`
	// Rows is the rows queries returned and statements changed, in all.
	Rows   uint64 `json:"rows"`
	Errors uint64 `json:"errors"`
	trace.TimingSummary
}

func (qs *queryStats) stat(query string) *queryStat {
	key := oneLine(query)
	if len(key) > 500 {
		key = key[:500] + "…"
	}
	qs.mu.Lock()
	defer qs.mu.Unlock()
	if qs.shapes == nil {
		qs.shapes = map[string]*queryStat{}
	}
	s := qs.shapes[key]
	if s == nil {
		if len(qs.shapes) >= maxShapes {
			key = "other"
			s = qs.shapes[key]
		}
		if s == nil {
			s = &queryStat{}
			qs.shapes[key] = s
		}
	}
	return s
}

func (qs *queryStats) add(query string, start time.Time, d time.Duration, rows int64, failed bool) {
	s := qs.stat(query)
	qs.mu.Lock()
	defer qs.mu.Unlock()
	s.timing.Add(start, trace.Millis(d), true)
	if rows > 0 {
		s.rows += uint64(rows)
	}
	if failed {
		s.errors++
	}
}

func (qs *queryStats) addRows(query string, rows int64) {
	if rows <= 0 {
		return
	}
	s := qs.stat(query)
	qs.mu.Lock()
	defer qs.mu.Unlock()
	s.rows += uint64(rows)
}

// QueryStats returns every statement the DB has run, the busiest first:
// by the time they took in all, so a query that is quick but frequent
// ranks with one that is slow.
func (db *DB) QueryStats() []QueryStat {
	qs := &db.queries
	now := time.Now()
	qs.mu.Lock()
	defer qs.mu.Unlock()
	out := make([]QueryStat, 0, len(qs.shapes))
	for q, s := range qs.shapes {
		out = append(out, QueryStat{Query: q, Rows: s.rows, Errors: s.errors, TimingSummary: s.timing.Summary(now)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalMS != out[j].TotalMS {
			return out[i].TotalMS > out[j].TotalMS
		}
		return out[i].Query < out[j].Query
	})
	return out
}

// Record counts a statement that code ran on db.SQL() directly, as
// Exec and Query count their own: in QueryStats, and as db.slow when it
// ran for the slow threshold or longer. rows is the rows it returned or
// changed, if known (else 0). The statement also counts toward the
// action ctx belongs to (trace.Tally). Loom's Postgres job store uses it, since
// it needs database/sql's own types for its locks and LISTEN; app code
// uses Exec and Query, which record themselves.
func (db *DB) Record(ctx context.Context, query string, start time.Time, rows int64, err error) {
	d := time.Since(start)
	db.queries.add(query, start, d, rows, err != nil)
	trace.TallyOf(ctx).Add(d)
	db.slowly(ctx, query, d)
}
