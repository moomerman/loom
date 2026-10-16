package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moomerman/loom/data"
)

// These methods are the one backend for looking at and acting on jobs:
// the loom CLI's jobs commands (through the app's /_loom/dev/ endpoints),
// the MCP tools and the jobsweb dashboard all call them. The JSON shapes
// are documented in docs/cli.md.

// JobDetail is a job with derived timings, for `loom jobs inspect`.
type JobDetail struct {
	Record
	// WaitSeconds is from scheduled to the latest attempt's start.
	WaitSeconds *float64 `json:"wait_seconds"`
	// RunSeconds is the finished job's latest attempt, start to finish.
	RunSeconds *float64 `json:"run_seconds"`
	// History is the job's life so far, oldest first, derived from its
	// timestamps and attempt errors.
	History []HistoryEntry `json:"history"`
}

// HistoryEntry is a step in a job's life.
type HistoryEntry struct {
	At    time.Time `json:"at"`
	State State     `json:"state"`          // the state the job entered
	Note  string    `json:"note,omitempty"` // e.g. the error of a failed attempt
}

// QueueInfo describes a queue, for `loom queues`.
type QueueInfo struct {
	Name        string        `json:"name"`
	Concurrency int           `json:"concurrency"` // per node
	Running     int           `json:"running"`     // on this node
	States      map[State]int `json:"states"`      // jobs in the store, cluster wide
	// Completed and Failed count attempts finished on this node in the
	// last minute: its throughput.
	Completed int `json:"completed_last_minute"`
	Failed    int `json:"failed_last_minute"`
	// OldestWaiting is how long the queue's oldest due job has waited
	// to run: growing while workers are busy, stuck or absent.
	OldestWaiting float64 `json:"oldest_available_seconds"`
}

// CronInfo describes a cron entry, for `loom cron`.
type CronInfo struct {
	Index     int            `json:"index"` // its place in the app's cron entries, for RunCron
	Expr      string         `json:"expr"`
	Type      string         `json:"type"`
	Args      []byte         `json:"-"`
	ArgsJSON  map[string]any `json:"args"`
	Builtin   bool           `json:"builtin"`
	NextRun   time.Time      `json:"next_run"`
	LastTick  *time.Time     `json:"last_tick"`  // enqueued by this node, while leader
	LastJob   *int64         `json:"last_job"`   // the job of that tick
	LastState State          `json:"last_state"` // and its state now
}

// Jobs lists jobs, newest first.
func (r *Runner) Jobs(ctx context.Context, f Filter) ([]*Record, error) {
	if f.State != "" && !f.State.Valid() {
		names := make([]string, len(States))
		for i, s := range States {
			names[i] = string(s)
		}
		return nil, &Error{Code: JB508, Msg: fmt.Sprintf("unknown state %q", f.State), Fix: "use one of " + strings.Join(names, ", ")}
	}
	return r.store.List(ctx, f)
}

// TypeNames lists the registered job types, for filters.
func (r *Runner) TypeNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.typeNames()
}

// Job returns one job with its timings. Errors are JB507.
func (r *Runner) Job(ctx context.Context, id int64) (*JobDetail, error) {
	rec, err := r.store.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, notFound(id)
	}
	if err != nil {
		return nil, err
	}
	d := &JobDetail{Record: *rec}
	if rec.AttemptedAt != nil {
		w := rec.AttemptedAt.Sub(rec.ScheduledAt).Seconds()
		if w < 0 {
			w = 0
		}
		d.WaitSeconds = &w
		if rec.FinishedAt != nil && !rec.FinishedAt.Before(*rec.AttemptedAt) {
			run := rec.FinishedAt.Sub(*rec.AttemptedAt).Seconds()
			d.RunSeconds = &run
		}
	}
	d.History = history(rec)
	return d, nil
}

// history derives a job's history from its record: inserted, scheduled,
// each failed attempt, the latest attempt and the outcome.
func history(rec *Record) []HistoryEntry {
	h := []HistoryEntry{{At: rec.InsertedAt, State: Available}}
	if rec.ScheduledAt.After(rec.InsertedAt) && len(rec.Errors) == 0 {
		h[0].State = Scheduled
	}
	for _, e := range rec.Errors {
		note := e.Error
		if e.Code != "" {
			note = e.Code + ": " + note
		}
		h = append(h, HistoryEntry{At: e.At, State: Retryable, Note: fmt.Sprintf("attempt %d failed: %s", e.Attempt, note)})
	}
	if rec.AttemptedAt != nil && rec.Attempt > len(rec.Errors) {
		h = append(h, HistoryEntry{At: *rec.AttemptedAt, State: Executing, Note: fmt.Sprintf("attempt %d started on %s", rec.Attempt, rec.AttemptedBy)})
	}
	switch {
	case rec.FinishedAt != nil:
		last := HistoryEntry{At: *rec.FinishedAt, State: rec.State}
		if rec.State == Discarded && len(h) > 1 && h[len(h)-1].State == Retryable {
			h[len(h)-1].State = Discarded // the last failure discarded it
			return h
		}
		h = append(h, last)
	case rec.State == Scheduled && len(h) > 1:
		h = append(h, HistoryEntry{At: rec.ScheduledAt, State: Scheduled, Note: "runs at this time"})
	}
	sort.SliceStable(h, func(i, j int) bool { return h[i].At.Before(h[j].At) })
	return h
}

func notFound(id int64) *Error {
	return &Error{Code: JB507, Msg: fmt.Sprintf("no job %d", id), Fix: "list jobs with `loom jobs`; finished jobs are pruned after the retention period"}
}

// Retry makes a job run now. Errors are JB507 and JB508.
func (r *Runner) Retry(ctx context.Context, id int64) error {
	err := r.store.Retry(ctx, id, Round(r.clock.Now()))
	if err == nil {
		r.changed(id)
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return notFound(id)
	case errors.Is(err, ErrState):
		return &Error{Code: JB508, Msg: fmt.Sprintf("job %d is already available or executing", id), Fix: "only scheduled, retryable, completed, discarded and cancelled jobs can be retried"}
	}
	return err
}

// Cancel cancels a job; if it is running on this node, its context is
// cancelled too. Errors are JB507 and JB508.
func (r *Runner) Cancel(ctx context.Context, id int64) error {
	err := r.store.Cancel(ctx, id, Round(r.clock.Now()))
	switch {
	case errors.Is(err, ErrNotFound):
		return notFound(id)
	case errors.Is(err, ErrState):
		return &Error{Code: JB508, Msg: fmt.Sprintf("job %d has already finished", id), Fix: "only scheduled, available, executing and retryable jobs can be cancelled"}
	case err != nil:
		return err
	}
	r.mu.Lock()
	if j := r.running[id]; j != nil {
		j.cancel()
	}
	r.mu.Unlock()
	r.changed(id)
	return nil
}

// Delete deletes a job that isn't executing. Errors are JB507 and JB508.
func (r *Runner) Delete(ctx context.Context, id int64) error {
	err := r.store.Delete(ctx, id)
	switch {
	case errors.Is(err, ErrNotFound):
		return notFound(id)
	case errors.Is(err, ErrState):
		return &Error{Code: JB508, Msg: fmt.Sprintf("job %d is executing", id), Fix: "cancel it first, then delete it"}
	case err != nil:
		return err
	}
	r.changed(id)
	return nil
}

// RetryDiscarded retries every discarded job, or those of one queue, and
// returns how many.
func (r *Runner) RetryDiscarded(ctx context.Context, queue string) (int, error) {
	n := 0
	for {
		recs, err := r.store.List(ctx, Filter{Queue: queue, State: Discarded, Limit: 100})
		if err != nil || len(recs) == 0 {
			return n, err
		}
		for _, rec := range recs {
			if err := r.store.Retry(ctx, rec.ID, Round(r.clock.Now())); err != nil && !errors.Is(err, ErrState) && !errors.Is(err, ErrNotFound) {
				return n, err
			}
			n++
		}
		r.changed()
	}
}

// RunCron enqueues the job of cron entry index (CronInfo.Index) to run
// now, outside its schedule, as an ordinary job. Errors are JB515.
func (r *Runner) RunCron(ctx context.Context, index int) (Result, error) {
	r.mu.Lock()
	if index < 0 || index >= len(r.cron) {
		n := len(r.cron)
		r.mu.Unlock()
		return Result{}, &Error{Code: JB515, Msg: fmt.Sprintf("no cron entry %d; there are %d", index, n), Fix: "use an index from `loom cron`"}
	}
	e := *r.cron[index]
	r.mu.Unlock()
	now := Round(r.clock.Now())
	res, err := r.insert(ctx, NewJob{Type: e.typ, Queue: e.opts.Queue, Args: e.args, MaxAttempts: e.opts.MaxAttempts, InsertedAt: now, ScheduledAt: now})
	if err == nil {
		r.changed(res.ID)
	}
	return res, err
}

// RecentFailures returns up to limit jobs whose latest attempt failed
// (retryable or discarded), most recent failure first.
func (r *Runner) RecentFailures(ctx context.Context, limit int) ([]*Record, error) {
	var out []*Record
	for _, st := range []State{Retryable, Discarded} {
		recs, err := r.store.List(ctx, Filter{State: st, Limit: limit})
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	failedAt := func(rec *Record) time.Time {
		if len(rec.Errors) == 0 {
			return rec.InsertedAt
		}
		return rec.Errors[len(rec.Errors)-1].At
	}
	sort.SliceStable(out, func(i, j int) bool { return failedAt(out[i]).After(failedAt(out[j])) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// LookupType finds a registered job type by name, with or without its
// package ("SendWelcome" or "accounts.SendWelcome"). Errors are JB501.
func (r *Runner) LookupType(name string) (Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var found []string
	for n := range r.types {
		if n == name || shortName(n) == name {
			found = append(found, n)
		}
	}
	sort.Strings(found)
	switch len(found) {
	case 1:
		return r.types[found[0]], nil
	case 0:
		return nil, unregistered(name, r.typeNames())
	}
	return nil, &Error{Code: JB501, Msg: fmt.Sprintf("job type name %q is ambiguous: it could be %s", name, strings.Join(found, " or ")), Fix: "use the full name, with the package"}
}

// EnqueueJSON enqueues a job of a registered type from JSON args, in a
// transaction of its own, for `loom jobs enqueue`. Errors are JB501 and
// JB509.
func (r *Runner) EnqueueJSON(ctx context.Context, typeName string, args []byte, opts ...Option) (Result, error) {
	proto, err := r.LookupType(typeName)
	if err != nil {
		return Result{}, err
	}
	if len(strings.TrimSpace(string(args))) == 0 {
		args = []byte("{}")
	}
	job, err := decodeJob(proto, args, true)
	if err != nil {
		return Result{}, &Error{Code: JB509, Msg: fmt.Sprintf("the args don't fit %s: %v", TypeName(proto), err), Fix: "give args as a JSON object with the job's fields, using their json tag names"}
	}
	var res Result
	err = r.store.DB().Tx(ctx, func(tx data.Tx) error {
		res, err = Enqueue(tx, job, opts...)
		return err
	})
	return res, err
}

// QueueInfo describes every configured queue, plus any queue that has
// jobs but no workers on this node.
func (r *Runner) QueueInfo(ctx context.Context) ([]QueueInfo, error) {
	stats, err := r.queueStats(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]QueueStats{}
	for _, s := range stats {
		byName[s.Queue] = s
	}
	now := r.clock.Now()
	waited := func(s QueueStats) float64 {
		if s.Waiting.IsZero() || !s.Waiting.Before(now) {
			return 0
		}
		return now.Sub(s.Waiting).Truncate(time.Second).Seconds()
	}
	r.mu.Lock()
	if r.queue(DefaultQueue) == nil {
		r.validate() // adds the default queue, as Start would
	}
	out := []QueueInfo{}
	seen := map[string]bool{}
	for _, q := range r.queues {
		st := byName[q.Name]
		qi := QueueInfo{Name: q.Name, Concurrency: q.Concurrency, Running: q.running, States: fullStates(st.States), OldestWaiting: waited(st)}
		for _, f := range q.finished {
			if now.Sub(f.at) <= time.Minute {
				if f.ok {
					qi.Completed++
				} else {
					qi.Failed++
				}
			}
		}
		out = append(out, qi)
		seen[q.Name] = true
	}
	r.mu.Unlock()
	for _, s := range stats {
		if !seen[s.Queue] {
			out = append(out, QueueInfo{Name: s.Queue, States: fullStates(s.States), OldestWaiting: waited(s)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// queueStats returns the store's counts, reusing them for StatsMaxAge:
// counting is the one query that grows with the table, and every open
// dashboard asks for it on each render.
func (r *Runner) queueStats(ctx context.Context) ([]QueueStats, error) {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	if r.stats != nil && time.Since(r.statsTime) < r.StatsMaxAge {
		return r.stats, nil
	}
	stats, err := r.store.Stats(ctx)
	if err != nil {
		return nil, err
	}
	r.stats, r.statsTime = stats, time.Now()
	return stats, nil
}

func fullStates(m map[State]int) map[State]int {
	out := map[State]int{}
	for _, s := range States {
		out[s] = m[s]
	}
	return out
}

// CronInfo describes the cron entries, including the built-in prune.
func (r *Runner) CronInfo(ctx context.Context) ([]CronInfo, error) {
	now := r.clock.Now()
	r.mu.Lock()
	entries := make([]cronEntry, len(r.cron))
	for i, e := range r.cron {
		entries[i] = *e
	}
	r.mu.Unlock()
	out := []CronInfo{}
	for i, e := range entries {
		ci := CronInfo{Index: i, Expr: e.sched.expr, Type: e.typ, Args: e.args, Builtin: e.builtin, NextRun: e.sched.Next(now)}
		ci.ArgsJSON = map[string]any{}
		_ = json.Unmarshal(e.args, &ci.ArgsJSON)
		if !e.lastTick.IsZero() {
			t, id := e.lastTick, e.lastID
			ci.LastTick, ci.LastJob = &t, &id
			if rec, err := r.store.Get(ctx, id); err == nil {
				ci.LastState = rec.State
			}
		}
		out = append(out, ci)
	}
	return out, nil
}
