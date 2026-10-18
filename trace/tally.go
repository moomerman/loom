package trace

import (
	"context"
	"sync/atomic"
	"time"
)

// Tally counts the database statements one action ran: a page's event,
// re-render or mount, or a job's attempt. The data layer adds every
// statement to the tally its context carries, and the action's page.*
// or job.finish event reports it as "queries" and "query_ms", so a
// click that runs forty queries is visible, and testable, where a
// query log would bury it.
type Tally struct {
	n, ns atomic.Int64
}

type tallyKey struct{}

// WithTally returns ctx carrying t.
func WithTally(ctx context.Context, t *Tally) context.Context {
	return context.WithValue(ctx, tallyKey{}, t)
}

// TallyOf returns the tally ctx carries, or nil.
func TallyOf(ctx context.Context) *Tally {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(tallyKey{}).(*Tally)
	return t
}

// Add counts one statement that ran for d. A nil Tally ignores it.
func (t *Tally) Add(d time.Duration) {
	if t == nil {
		return
	}
	t.n.Add(1)
	t.ns.Add(int64(d))
}

// Reset starts the count again, for a page's next action.
func (t *Tally) Reset() {
	t.n.Store(0)
	t.ns.Store(0)
}

// Count returns the statements counted and their time.
func (t *Tally) Count() (int64, time.Duration) {
	if t == nil {
		return 0, 0
	}
	return t.n.Load(), time.Duration(t.ns.Load())
}

// Attrs returns the count as event attributes: "queries" and
// "query_ms".
func (t *Tally) Attrs() []any {
	if t == nil {
		return nil
	}
	return []any{"queries", t.n.Load(), "query_ms", time.Duration(t.ns.Load())}
}
