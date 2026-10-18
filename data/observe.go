package data

import (
	"context"
	"time"

	"github.com/moomerman/loom/trace"
)

// observer is where a DB reports its transactions and slow statements.
type observer struct {
	stream *trace.Stream
	slow   time.Duration
}

// Observe makes db report to stream: tx.commit and tx.rollback for each
// transaction, and db.slow for each statement that runs for slow or
// longer (zero reports none). The trace id of each event is the one in
// the context the transaction or statement was given, so a page
// handler's commit belongs to the click that caused it. The app calls it
// for its DB.
func (db *DB) Observe(stream *trace.Stream, slow time.Duration) {
	db.obs.Store(&observer{stream: stream, slow: slow})
}

// execDone counts a statement that started at start, with the rows it
// changed, and reports it if it was slow. Deferred with the start time
// and the statement's results, it times the call.
func (db *DB) execDone(ctx context.Context, query string, start time.Time, res *Result, err *error) {
	var n int64
	if *err == nil && *res != nil {
		n, _ = (*res).RowsAffected()
	}
	db.Record(ctx, query, start, n, *err)
}

// queryDone is execDone for a query: the rows it returns are counted as
// they are read, when the rows close.
func (db *DB) queryDone(ctx context.Context, query string, start time.Time, rows *Rows, err *error) {
	db.Record(ctx, query, start, 0, *err)
	if r, ok := (*rows).(*mappedRows); ok && *err == nil {
		r.count = func(n int64) { db.queries.addRows(query, n) }
	}
}

// slowly reports a statement that ran for d, if that was slow.
func (db *DB) slowly(ctx context.Context, query string, d time.Duration) {
	o := db.obs.Load()
	if o == nil || o.slow <= 0 {
		return
	}
	if d >= o.slow {
		o.stream.Emit(ctx, trace.DBSlow, "ms", d, "query", shortQuery(query))
	}
}

// finished reports a transaction's end: committed, or rolled back for
// reason.
func (db *DB) finished(t *tx, start time.Time, reason string) {
	o := db.obs.Load()
	if o == nil {
		return
	}
	if reason == "" {
		o.stream.Emit(t.ctx, trace.TxCommit, "ms", time.Since(start), "statements", t.statements.Load())
		return
	}
	o.stream.Emit(t.ctx, trace.TxRollback, "ms", time.Since(start), "statements", t.statements.Load(), "reason", reason)
}

// shortQuery is a statement as an event shows it: on one line, cut
// after 200 bytes. Arguments are never shown.
func shortQuery(q string) string {
	q = oneLine(q)
	if len(q) > 200 {
		q = q[:200] + "…"
	}
	return q
}
