package dashboard

import (
	"fmt"

	"github.com/moomerman/loom"
)

// QueriesPage lists every SQL statement the app's database ran since it
// started, the busiest first by total time, as `loom queries` does:
// Loom's own (jobs, sessions, rate limits) as well as the app's. A
// statement that is slow on average needs an index; one that is quick
// but run very often is a query in a loop.
type QueriesPage struct {
	app *App
}

// QueryRow is one statement's totals.
type QueryRow struct {
	Query      string
	Count      int
	LastMinute int
	Mean       string
	P95        string
	Max        string
	Total      string
	Rows       int
	Errors     int
	Share      int // of all the statements' time, in percent, for the bar
}

// Mount starts the refresh.
func (p *QueriesPage) Mount(c *loom.Ctx) {
	section(c, "queries")
	c.Title("Queries")
	p.app.tick(c)
}

// Events has the refresh's timer.
func (p *QueriesPage) Events() loom.Events {
	return loom.Events{"tick": func(c *loom.Ctx, _ loom.Payload) { p.app.tick(c) }}
}

// Assigns reads the statements' totals.
func (p *QueriesPage) Assigns(c *loom.Ctx) QueriesAssigns {
	res := p.app.app.Queries()
	a := QueriesAssigns{HasDb: p.app.app.DB().SQL() != nil}
	var total float64
	for _, q := range res.Queries {
		total += q.TotalMS
	}
	for _, q := range res.Queries {
		row := QueryRow{
			Query: q.Query, Count: int(q.Count), LastMinute: int(q.LastMinute), Mean: millis(q.MeanMS), P95: millis(q.P95MS),
			Max: millis(q.MaxMS), Total: millis(q.TotalMS), Rows: int(q.Rows), Errors: int(q.Errors),
		}
		if total > 0 {
			row.Share = int(q.TotalMS * 100 / total)
		}
		a.Statements += row.Count
		a.Queries = append(a.Queries, row)
	}
	a.Summary = fmt.Sprintf("%d statements of %d shapes, %s in all", a.Statements, len(a.Queries), millis(total))
	return a
}
