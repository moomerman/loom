package loomtest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/loomtest"
	"github.com/moomerman/loom/render"
)

// pingJob broadcasts on "pings" when it runs.
type pingJob struct{}

func (pingJob) Perform(ctx context.Context, c *jobs.Ctx) error {
	return c.Broadcast("pings", "pinged", nil)
}

// pingPage enqueues a ping in its "ping" handler, and counts the
// broadcasts it hears.
type pingPage struct {
	db    *loom.DB
	heard int
}

func (p *pingPage) Mount(c *loom.Ctx) { c.Subscribe("pings") }

func (p *pingPage) Events() loom.Events {
	return loom.Events{"ping": func(c *loom.Ctx, _ loom.Payload) {
		err := p.db.Tx(c.Context(), func(tx loom.Tx) error {
			_, err := jobs.Enqueue(tx, pingJob{})
			return err
		})
		if err != nil {
			c.Fail(err)
		}
	}}
}

func (p *pingPage) HandleInfo(c *loom.Ctx, msg loom.Message) { p.heard++ }

func (p *pingPage) Assigns(c *loom.Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: strings.Repeat("|", p.heard), Events: []render.Event{{Name: "ping"}}}
}

func pingApp(t *testing.T, db *data.DB, q *loomtest.JobQueue, cfg loom.Config) *loom.App {
	app := loom.New(cfg)
	app.UseDB(db)
	q.Attach(app)
	q.Jobs(pingJob{})
	app.Live("/", &pingPage{db: db}, hxtest.Counter)
	return app
}

// A click's events, and those of the job it enqueued once drained,
// share the page's trace, so AssertEvent finds them all.
func TestPageEvents(t *testing.T) {
	q := loomtest.Jobs(t)
	app := pingApp(t, q.DB(), q, loom.Config{})
	p := loomtest.Mount(t, app, "/")
	p.AssertEvent("page.mount", "route", "/", "outcome", "ok")
	p.Listen(q.PubSub())
	p.Event("ping", nil)
	click := p.Trace()
	p.AssertEvent("page.event", "event", "ping", "outcome", "ok")
	p.AssertEvent("tx.commit", "statements", 0) // an in-memory DB runs no SQL
	p.AssertEvent("job.enqueue", "type", "loomtest_test.pingJob", "duplicate", false)
	p.RefuteEvent("job.start")
	q.Drain(t)
	// The broadcast's HandleInfo is now the page's latest action, and it
	// carries on the click's trace.
	if p.Trace() != click {
		t.Errorf("the job's broadcast was handled under trace %s, not the click's %s", p.Trace(), click)
	}
	p.AssertEvent("job.finish", "type", "loomtest_test.pingJob", "state", "completed")
	p.AssertEvent("page.info", "topic", "pings", "diff", true)
	q.AssertEvent(t, "job.start", "attempt", 1)
}
