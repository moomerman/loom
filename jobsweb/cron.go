package jobsweb

import (
	"fmt"
	"strconv"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
)

// CronPage lists the app's cron entries with their schedule, next run
// and the outcome of the last tick, and can run an entry now: the usual
// way to test a nightly job without waiting for the night.
type CronPage struct {
	app   *App
	flash string
}

// CronRow is a cron entry.
type CronRow struct {
	Index     string
	Expr      string
	Type      string
	Args      string
	NextRun   string // in full, e.g. "2026-10-12 03:00:00 UTC"; schedules are UTC
	NextIn    string // relative, e.g. "in 16h"
	LastTick  string // "" if this node hasn't enqueued a tick yet
	LastJob   string // the job of the last tick, for a link
	LastState string
	Builtin   bool
}

// Mount subscribes to job changes, which update each entry's last result.
func (p *CronPage) Mount(c *loom.Ctx) {
	section(c, "cron")
	c.Title("Cron")
	c.Subscribe(jobs.Topic)
}

// Events lists the page's one action.
func (p *CronPage) Events() loom.Events {
	return loom.Events{"run_now": p.runNow}
}

// runNow runs an entry now. Running a cron job early is an ordinary
// enqueue, so it doesn't ask for confirmation.
func (p *CronPage) runNow(c *loom.Ctx, payload loom.Payload) {
	p.flash = p.enqueue(c, payload["index"])
}

func (p *CronPage) enqueue(c *loom.Ctx, index string) string {
	if p.app.readOnly(c) {
		return "You have read-only access."
	}
	i, err := strconv.Atoi(index)
	if err != nil {
		return "Unknown cron entry."
	}
	res, err := p.app.runner().RunCron(c.Context(), i)
	if err != nil {
		return errorMessage(err)
	}
	return fmt.Sprintf("Enqueued job %d.", res.ID)
}

// HandleInfo has nothing to change: Assigns reads the entries' latest
// results on every render.
func (p *CronPage) HandleInfo(c *loom.Ctx, msg loom.Message) {}

// Assigns reads the cron entries for the template.
func (p *CronPage) Assigns(c *loom.Ctx) CronAssigns {
	r := p.app.runner()
	a := CronAssigns{ReadOnly: p.app.readOnly(c), Flash: p.flash}
	entries, err := r.CronInfo(c.Context())
	if err != nil {
		a.Flash = errorMessage(err)
		return a
	}
	now := r.Clock().Now()
	for _, e := range entries {
		row := CronRow{
			Index: strconv.Itoa(e.Index), Expr: e.Expr, Type: e.Type, Args: compact(p.app.args(e.Type, e.Args)),
			NextRun: stamp(e.NextRun), NextIn: ago(now, e.NextRun), LastState: string(e.LastState), Builtin: e.Builtin,
		}
		if e.LastTick != nil {
			row.LastTick = ago(now, *e.LastTick)
		}
		if e.LastJob != nil {
			row.LastJob = strconv.FormatInt(*e.LastJob, 10)
		}
		a.Entries = append(a.Entries, row)
	}
	return a
}
