package loomtest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/pubsub"
)

// JobQueue runs background jobs in a test: an in-memory store on a fake
// clock, with jobs run synchronously and deterministically by Drain.
//
//	q := loomtest.Jobs(t)
//	q.Jobs(SendWelcome{})                      // register job types, as app.Jobs does
//	signUp(q.DB(), "u1")                       // code that enqueues in q.DB()'s transactions
//	q.AssertEnqueued(t, SendWelcome{UserID: "u1"})
//	q.Advance(10 * time.Minute)                // scheduled jobs and cron ticks become due
//	q.Drain(t)                                 // run every due job
//	q.AssertCompleted(t, SendWelcome{})
//
// Assertions match a job by type, and by every field of the given value
// that isn't its zero value: SendWelcome{} matches any SendWelcome.
type JobQueue struct {
	t      testing.TB
	clock  *jobs.FakeClock
	store  jobs.Store
	runner *jobs.Runner
	ps     *recorder
}

// JobsEpoch is the fake clock's starting time in loomtest.Jobs.
var JobsEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// JobsOption changes how Jobs sets up a JobQueue.
type JobsOption func(*jobsOptions)

type jobsOptions struct {
	memory   jobs.MemoryOptions
	newStore func(clock jobs.Clock) jobs.Store
}

// UseDB makes the queue's store take jobs enqueued in db's transactions,
// such as a loomtest.DB(t): jobs.Enqueue(tx, ...) in db.Tx then lands in
// the queue when the transaction (a savepoint, in the default mode)
// commits. The default is a local DB of its own (q.DB()).
func UseDB(db *data.DB) JobsOption { return func(o *jobsOptions) { o.memory.DB = db } }

// WithStore runs the queue on the store newStore makes on the fake
// clock, such as a Postgres store, instead of in memory. Use it to run
// the same test on every store. Only the in-memory store announces
// enqueues on jobs.Topic, since it does so synchronously; changes made
// by running jobs, retries and other runner actions are announced on
// any store.
//
//	q := loomtest.Jobs(t, loomtest.WithStore(func(c jobs.Clock) jobs.Store {
//		return pgjobs.New(db, pgjobs.Options{Clock: c})
//	}))
func WithStore(newStore func(clock jobs.Clock) jobs.Store) JobsOption {
	return func(o *jobsOptions) { o.newStore = newStore }
}

// Jobs returns a JobQueue whose clock starts at JobsEpoch, with an
// in-memory store unless WithStore gives another. Only cron entries
// added with Cron run; the built-in prune job doesn't.
func Jobs(t testing.TB, opts ...JobsOption) *JobQueue {
	var o jobsOptions
	for _, opt := range opts {
		opt(&o)
	}
	clock := jobs.NewFakeClock(JobsEpoch)
	var store jobs.Store
	if o.newStore != nil {
		store = o.newStore(clock)
	} else {
		o.memory.Clock = clock
		store = jobs.NewMemoryStore(o.memory)
	}
	ps := &recorder{Local: pubsub.NewLocal()}
	r := jobs.NewRunner(store)
	r.Node = "loomtest"
	r.PubSub = ps
	r.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	r.Backoff = func(attempt int) time.Duration { return time.Duration(attempt) * time.Minute }
	r.ChangeInterval = 0 // announce job changes on jobs.Topic at once,
	r.StatsMaxAge = 0    // and count jobs afresh, so tests see every change
	r.Manual = true      // jobs run in Drain, even once an attached app serves requests
	if o.newStore == nil {
		t.Cleanup(r.Watch()) // the memory store notifies on the committing goroutine
	}
	return &JobQueue{t: t, clock: clock, store: store, runner: r, ps: ps}
}

// Jobs registers job types, as app.Jobs does, and gives their queues
// workers.
func (q *JobQueue) Jobs(js ...jobs.Job) {
	q.t.Helper()
	if err := q.runner.Register(js...); err != nil {
		q.t.Fatal(err)
	}
	for _, j := range js {
		q.runner.Queues(jobs.Queue{Name: jobs.OptionsOf(j).Queue})
	}
}

// Attach makes the queue app's job runner and PubSub, before the app's
// routes are added: the jobs the app and its sub-apps register (such as
// auth's mail job) are then the queue's, enqueues land in it, and Drain
// runs them.
//
//	q := loomtest.Jobs(t, loomtest.UseDB(db))
//	app := loom.New()
//	app.UseDB(db)
//	q.Attach(app)
//	app.Mount("/auth", auth.New(auth.Config{}))
func (q *JobQueue) Attach(app *loom.App) {
	q.t.Helper()
	app.UseJobRunner(q.runner)
}

// Cron adds a cron entry, as app.Cron does. Advance enqueues its ticks.
func (q *JobQueue) Cron(expr string, job jobs.Job) {
	q.t.Helper()
	if err := q.runner.Cron(expr, job); err != nil {
		q.t.Fatal(err)
	}
}

// Store is the queue's store: in memory unless WithStore gave another.
func (q *JobQueue) Store() jobs.Store { return q.store }

// DB is the store's database: pass it to code that enqueues jobs,
// which does so in its transactions (q.DB().Tx(ctx, ...)).
func (q *JobQueue) DB() *data.DB { return q.store.DB() }

// Clock is the fake clock. Use Advance to move it.
func (q *JobQueue) Clock() *jobs.FakeClock { return q.clock }

// Runner is the job runner, for its introspection methods.
func (q *JobQueue) Runner() *jobs.Runner { return q.runner }

// PubSub carries the jobs' Broadcasts. Connect pages to it with
// Page.Listen.
func (q *JobQueue) PubSub() pubsub.PubSub { return q.ps }

// Enqueue enqueues a job in a transaction of its own and returns its id.
func (q *JobQueue) Enqueue(job jobs.Job, opts ...jobs.Option) int64 {
	q.t.Helper()
	var res jobs.Result
	err := q.DB().Tx(context.Background(), func(tx data.Tx) error {
		var err error
		res, err = jobs.Enqueue(tx, job, opts...)
		return err
	})
	if err != nil {
		q.t.Fatal(err)
	}
	return res.ID
}

// Advance moves the clock forward by d, enqueueing each cron tick it
// passes, at that tick's time. Jobs don't run until Drain.
func (q *JobQueue) Advance(d time.Duration) {
	q.t.Helper()
	target := q.clock.Now().Add(d)
	for {
		next := q.runner.NextCron(q.clock.Now(), false)
		if next.IsZero() || next.After(target) {
			break
		}
		q.clock.Set(next)
		if err := q.runner.EnqueueCron(context.Background(), false); err != nil {
			q.t.Fatal(err)
		}
	}
	q.clock.Set(target)
}

// Drain runs every due job, one at a time on the calling goroutine, until
// none is due, and returns how many attempts ran. Jobs that fail are
// retried after a backoff of one minute per attempt, so they run again
// only after Advance. Failures don't fail the test; assert the outcome.
func (q *JobQueue) Drain(t testing.TB) int {
	t.Helper()
	n, err := q.runner.RunDue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// All returns every job, newest first.
func (q *JobQueue) All() []*jobs.Record {
	recs, err := q.store.List(context.Background(), jobs.Filter{Limit: 1 << 30})
	if err != nil {
		q.t.Fatal(err)
	}
	return recs
}

// Matching returns the jobs matching job, newest first.
func (q *JobQueue) Matching(job jobs.Job) []*jobs.Record {
	q.t.Helper()
	var out []*jobs.Record
	for _, r := range q.All() {
		if q.matches(r, job) {
			out = append(out, r)
		}
	}
	return out
}

// matches reports whether r is of job's type and has every non-zero
// field of job.
func (q *JobQueue) matches(r *jobs.Record, job jobs.Job) bool {
	if r.Type != jobs.TypeName(job) {
		return false
	}
	want := argsMap(q.t, job)
	zero := argsMap(q.t, reflect.Zero(reflect.TypeOf(job)).Interface())
	var got map[string]any
	if err := json.Unmarshal(r.Args, &got); err != nil {
		return false
	}
	for k, v := range want {
		if reflect.DeepEqual(v, zero[k]) {
			continue // a zero field matches anything
		}
		if !reflect.DeepEqual(v, got[k]) {
			return false
		}
	}
	return true
}

func argsMap(t testing.TB, v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	json.Unmarshal(b, &m)
	return m
}

// AssertEnqueued fails the test unless a matching job is waiting to run:
// scheduled, available or retryable.
func (q *JobQueue) AssertEnqueued(t testing.TB, job jobs.Job) {
	t.Helper()
	q.assert(t, job, "enqueued", jobs.Scheduled, jobs.Available, jobs.Retryable)
}

// RefuteEnqueued fails the test if a matching job is waiting to run.
func (q *JobQueue) RefuteEnqueued(t testing.TB, job jobs.Job) {
	t.Helper()
	for _, r := range q.Matching(job) {
		if r.State == jobs.Scheduled || r.State == jobs.Available || r.State == jobs.Retryable {
			t.Errorf("job %d (%s %s) is %s, want no such job enqueued", r.ID, r.Type, r.Args, r.State)
		}
	}
}

// AssertCompleted fails the test unless a matching job completed.
func (q *JobQueue) AssertCompleted(t testing.TB, job jobs.Job) {
	t.Helper()
	q.assert(t, job, "completed", jobs.Completed)
}

// AssertState fails the test unless a matching job is in state.
func (q *JobQueue) AssertState(t testing.TB, job jobs.Job, state jobs.State) {
	t.Helper()
	q.assert(t, job, string(state), state)
}

func (q *JobQueue) assert(t testing.TB, job jobs.Job, what string, states ...jobs.State) {
	t.Helper()
	for _, r := range q.Matching(job) {
		for _, s := range states {
			if r.State == s {
				return
			}
		}
	}
	t.Errorf("no %s job matches %s %s\n%s", what, jobs.TypeName(job), mustJSON(t, job), q.describe(jobs.TypeName(job)))
}

// describe lists the jobs of a type, for failure messages.
func (q *JobQueue) describe(typ string) string {
	var b strings.Builder
	for _, r := range q.All() {
		if r.Type != typ {
			continue
		}
		fmt.Fprintf(&b, "  job %d %s %s, attempt %d", r.ID, r.Args, r.State, r.Attempt)
		if n := len(r.Errors); n > 0 {
			fmt.Fprintf(&b, ", last error: %s", r.Errors[n-1].Error)
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return fmt.Sprintf("  (there are no %s jobs)", typ)
	}
	return "jobs of that type:\n" + b.String()
}

func mustJSON(t testing.TB, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Broadcasts returns the messages jobs have published, oldest first,
// leaving out the runner's own announcements on jobs.Topic.
func (q *JobQueue) Broadcasts() []pubsub.Message {
	q.ps.mu.Lock()
	defer q.ps.mu.Unlock()
	var out []pubsub.Message
	for _, m := range q.ps.sent {
		if m.Topic != jobs.Topic {
			out = append(out, m)
		}
	}
	return out
}

// AssertBroadcast fails the test unless a job published event on topic.
func (q *JobQueue) AssertBroadcast(t testing.TB, topic, event string) {
	t.Helper()
	for _, m := range q.Broadcasts() {
		if m.Topic == topic && m.Event == event {
			return
		}
	}
	t.Errorf("no %q broadcast on %q; broadcasts: %v", event, topic, q.Broadcasts())
}

// recorder is a pubsub.Local that records what is published.
type recorder struct {
	*pubsub.Local
	mu   sync.Mutex
	sent []pubsub.Message
}

func (r *recorder) Publish(m pubsub.Message) error {
	r.mu.Lock()
	r.sent = append(r.sent, m)
	r.mu.Unlock()
	return r.Local.Publish(m)
}
