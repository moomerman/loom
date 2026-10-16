package jobsweb

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
)

// JobPage shows one job in full: its args, every failed attempt with its
// error and stack trace, and its history, with the actions its state
// allows. It is the page an operator lands on from a failure, so
// everything needed to decide "retry, cancel or delete?" is on it.
type JobPage struct {
	app     *App
	id      int64
	flash   string
	confirm *confirmation
	job     *jobs.JobDetail // nil if not found
	loadErr error
}

// AttemptRow is a failed attempt.
type AttemptRow struct {
	Attempt int
	At      string
	Code    string // e.g. JB505 for a panic
	Error   string
	Stack   string
}

// HistoryRow is a step in the job's life.
type HistoryRow struct {
	Seq   int // the row's place in the history, which only grows: its :key
	At    string
	State string
	Note  string
}

// Mount reads the job id from the path, loads the job and subscribes to
// job changes.
func (p *JobPage) Mount(c *loom.Ctx) {
	section(c, "jobs")
	p.id, _ = strconv.ParseInt(c.Request.PathValue("id"), 10, 64)
	c.Title(fmt.Sprintf("Job %d", p.id))
	c.Subscribe(jobs.Topic)
	p.load(c)
}

// Events runs the job's actions. Retry is safe to repeat, so it runs at
// once; cancel and delete lose work, so they ask first.
func (p *JobPage) Events() loom.Events {
	return loom.Events{
		"retry":   func(c *loom.Ctx, _ loom.Payload) { p.act(c, "retry") },
		"cancel":  func(c *loom.Ctx, _ loom.Payload) { p.ask("cancel", fmt.Sprintf("Cancel job %d?", p.id)) },
		"delete":  func(c *loom.Ctx, _ loom.Payload) { p.ask("delete", fmt.Sprintf("Delete job %d?", p.id)) },
		"dismiss": func(c *loom.Ctx, _ loom.Payload) { p.flash, p.confirm = "", nil },
		"confirm": func(c *loom.Ctx, _ loom.Payload) {
			if p.confirm != nil {
				action := p.confirm.action
				p.confirm = nil
				p.act(c, action)
			}
		},
	}
}

func (p *JobPage) ask(action, prompt string) {
	p.flash, p.confirm = "", &confirmation{action: action, prompt: prompt}
}

// act runs an action and reloads the job.
func (p *JobPage) act(c *loom.Ctx, action string) {
	p.flash = p.run(c, action)
	p.load(c)
}

// run runs an action, refusing it for read-only operators even though
// they have no buttons: an event can be sent without one.
func (p *JobPage) run(c *loom.Ctx, action string) string {
	if p.app.readOnly(c) {
		return "You have read-only access."
	}
	r, ctx := p.app.runner(), c.Context()
	var err error
	switch action {
	case "retry":
		err = r.Retry(ctx, p.id)
	case "cancel":
		err = r.Cancel(ctx, p.id)
	case "delete":
		err = r.Delete(ctx, p.id)
	}
	if err != nil {
		return errorMessage(err)
	}
	return map[string]string{"retry": "Retrying the job.", "cancel": "Cancelled the job.", "delete": "Deleted the job."}[action]
}

// HandleInfo reloads the job only when it changed: the runner announces
// every job's changes on one topic.
func (p *JobPage) HandleInfo(c *loom.Ctx, msg loom.Message) {
	if jobs.Changed(msg.Payload, p.id) {
		p.load(c)
	}
}

// load reads the job. It runs on Mount, after an action and when the job
// changes, rather than in Assigns, which runs after every message.
func (p *JobPage) load(c *loom.Ctx) {
	p.job, p.loadErr = p.app.runner().Job(c.Context(), p.id)
	var je *jobs.Error
	if errors.As(p.loadErr, &je) && je.Code == jobs.JB507 {
		p.job, p.loadErr = nil, nil // not found: deleted, pruned or never existed
	}
}

// Assigns formats the loaded job for the template.
func (p *JobPage) Assigns(c *loom.Ctx) JobDetailAssigns {
	r := p.app.runner()
	a := JobDetailAssigns{ID: strconv.FormatInt(p.id, 10), ReadOnly: p.app.readOnly(c), Flash: p.flash}
	if p.confirm != nil {
		a.Confirm = p.confirm.prompt
	}
	if p.loadErr != nil {
		a.Flash = errorMessage(p.loadErr)
		return a
	}
	d := p.job
	if d == nil {
		return a
	}
	rec := &d.Record
	a.Found = true
	a.Job = p.app.row(rec, r.Clock().Now())
	a.Args = indent(p.app.args(rec.Type, rec.Args))
	a.Inserted, a.Scheduled = stamp(rec.InsertedAt), stamp(rec.ScheduledAt)
	if rec.AttemptedAt != nil {
		a.Attempted = stamp(*rec.AttemptedAt) + " on " + rec.AttemptedBy
	}
	if rec.FinishedAt != nil {
		a.Finished = stamp(*rec.FinishedAt)
	}
	if d.WaitSeconds != nil {
		a.Wait = fmt.Sprintf("%.1fs", *d.WaitSeconds)
	}
	if d.RunSeconds != nil {
		a.Run = fmt.Sprintf("%.3fs", *d.RunSeconds)
	}
	for i := len(rec.Errors) - 1; i >= 0; i-- { // newest first
		e := rec.Errors[i]
		a.Attempts = append(a.Attempts, AttemptRow{Attempt: e.Attempt, At: stamp(e.At), Code: e.Code, Error: e.Error, Stack: e.Stack})
	}
	for _, h := range d.History {
		a.History = append(a.History, HistoryRow{Seq: len(a.History), At: stamp(h.At), State: string(h.State), Note: h.Note})
	}
	a.CanRetry = rec.State != jobs.Executing && rec.State != jobs.Available
	a.CanCancel = !rec.State.Finished()
	a.CanDelete = rec.State != jobs.Executing
	return a
}
