// Package jobstest is the conformance suite every jobs.Store must pass,
// so that the in-memory and Postgres stores behave identically.
//
//	func TestConformance(t *testing.T) {
//		jobstest.Run(t, func(t *testing.T, clock jobs.Clock) func() jobs.Store {
//			backend := newEmptyBackend(t)
//			return func() jobs.Store { return openNode(backend, clock) }
//		})
//	}
//
// The factory makes a fresh, empty backend for each test and returns a
// function that opens a node on it: a store value as a separate process
// would have. Two nodes opened from one backend share its jobs.
package jobstest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/trace"
)

// Factory makes an empty backend on clock and returns its node opener.
type Factory func(t *testing.T, clock jobs.Clock) (open func() jobs.Store)

// start is the fake clock's starting time: a Sunday, 02:58 UTC.
var start = time.Date(2026, 1, 4, 2, 58, 0, 0, time.UTC)

// Run runs the conformance suite.
func Run(t *testing.T, f Factory) {
	tests := []struct {
		name string
		fn   func(t *testing.T, e *env)
	}{
		{"Transactions", testTransactions},
		{"RunAndComplete", testRunAndComplete},
		{"Trace", testTrace},
		{"RetryAndBackoff", testRetryAndBackoff},
		{"Snooze", testSnooze},
		{"Cancel", testCancel},
		{"Unique", testUnique},
		{"UniqueConcurrent", testUniqueConcurrent},
		{"Scheduled", testScheduled},
		{"Timeout", testTimeout},
		{"Panic", testPanic},
		{"UnknownType", testUnknownType},
		{"CrashRecovery", testCrashRecovery},
		{"GracefulShutdown", testGracefulShutdown},
		{"ShutdownReleases", testShutdownReleases},
		{"ConcurrentFetch", testConcurrentFetch},
		{"Workers", testWorkers},
		{"CronLeader", testCronLeader},
		{"ManualRetryAndCancel", testManualRetryAndCancel},
		{"ListStatsPrune", testListStatsPrune},
		{"PagesSearchDelete", testPagesSearchDelete},
		{"DashboardActions", testDashboardActions},
		{"Nodes", testNodes},
		{"NodesLive", testNodesLive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := jobs.NewFakeClock(start)
			open := f(t, clock)
			tc.fn(t, &env{t: t, clock: clock, open: open, probe: newProbe()})
		})
	}
}

type env struct {
	t     *testing.T
	clock *jobs.FakeClock
	open  func() jobs.Store
	probe *probe
}

// runner opens a node and returns a runner on it with the test job
// types registered and quiet logging.
func (e *env) runner(node string) *jobs.Runner {
	e.t.Helper()
	s := e.open()
	r := jobs.NewRunner(s)
	r.Node = node
	r.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	r.Backoff = func(attempt int) time.Duration { return time.Duration(attempt) * time.Minute }
	r.PollInterval = 10 * time.Millisecond
	r.LeaseDuration = 30 * time.Second
	if err := r.Register(Work{probe: e.probe}, Slow{probe: e.probe}, Tick{probe: e.probe}); err != nil {
		e.t.Fatal(err)
	}
	if err := r.Queues(jobs.Queue{Name: "slow", Concurrency: 2}); err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) enqueue(s jobs.Store, job jobs.Job, opts ...jobs.Option) jobs.Result {
	e.t.Helper()
	var res jobs.Result
	err := s.DB().Tx(context.Background(), func(tx data.Tx) error {
		var err error
		res, err = jobs.Enqueue(tx, job, opts...)
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

var errRollback = errors.New("roll back")

// begin starts a transaction on db that stays open until end is called
// with true (commit) or false (roll back), so a test can interleave
// transactions.
func begin(t *testing.T, db *data.DB) (data.Tx, func(commit bool)) {
	t.Helper()
	txc := make(chan data.Tx)
	endc := make(chan bool)
	done := make(chan error)
	go func() {
		done <- db.Tx(context.Background(), func(tx data.Tx) error {
			txc <- tx
			if <-endc {
				return nil
			}
			return errRollback
		})
	}()
	select {
	case tx := <-txc:
		return tx, func(commit bool) {
			endc <- commit
			if err := <-done; err != nil && !errors.Is(err, errRollback) {
				t.Errorf("transaction: %v", err)
			}
		}
	case err := <-done:
		t.Fatalf("begin: %v", err)
	}
	return nil, nil
}

func (e *env) get(s jobs.Store, id int64) *jobs.Record {
	e.t.Helper()
	r, err := s.Get(context.Background(), id)
	if err != nil {
		e.t.Fatalf("get job %d: %v", id, err)
	}
	return r
}

func (e *env) runDue(r *jobs.Runner) int {
	e.t.Helper()
	n, err := r.RunDue(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) wantState(s jobs.Store, id int64, want jobs.State) *jobs.Record {
	e.t.Helper()
	r := e.get(s, id)
	if r.State != want {
		e.t.Fatalf("job %d is %s, want %s (errors: %+v)", id, r.State, want, r.Errors)
	}
	return r
}

// eventually waits up to 5s for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// probe records what test jobs did; jobs reach it through their
// prototype's `json:"-"` field.
type probe struct {
	mu      sync.Mutex
	runs    map[string]int
	started chan string
	release chan struct{}
}

func newProbe() *probe {
	return &probe{runs: map[string]int{}, started: make(chan string, 100), release: make(chan struct{})}
}

func (p *probe) ran(key string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runs[key]
}

// Work does what its Mode says: "ok", "fail", "snooze", "cancel", "panic",
// "fail-until-3".
type Work struct {
	Key   string `json:"key"`
	Mode  string `json:"mode"`
	probe *probe `json:"-"`
}

func (w Work) Options() jobs.Options { return jobs.Options{MaxAttempts: 3} }

func (w Work) Perform(ctx context.Context, c *jobs.Ctx) error {
	w.probe.mu.Lock()
	w.probe.runs[w.Key]++
	w.probe.mu.Unlock()
	switch w.Mode {
	case "fail":
		return fmt.Errorf("attempt %d failed", c.Attempt())
	case "fail-until-3":
		if c.Attempt() < 3 {
			return errors.New("not yet")
		}
	case "snooze":
		if w.probe.ran(w.Key) == 1 {
			return jobs.Snooze(10 * time.Minute)
		}
	case "cancel":
		return jobs.Cancel(errors.New("no longer needed"))
	case "panic":
		panic("boom")
	}
	return nil
}

// Slow runs until released (probe.release closed) or its context ends,
// and returns the context's error if that came first. Its timeout is
// short when Mode is "timeout".
type Slow struct {
	Key   string `json:"key"`
	Mode  string `json:"mode"`
	probe *probe `json:"-"`
}

func (s Slow) Options() jobs.Options {
	o := jobs.Options{Queue: "slow", MaxAttempts: 3, Timeout: time.Minute}
	if s.Mode == "timeout" {
		o.Timeout = 50 * time.Millisecond
	}
	return o
}

func (s Slow) Perform(ctx context.Context, c *jobs.Ctx) error {
	s.probe.mu.Lock()
	s.probe.runs[s.Key]++
	s.probe.mu.Unlock()
	s.probe.started <- s.Key
	select {
	case <-s.probe.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Tick is enqueued by cron.
type Tick struct {
	probe *probe `json:"-"`
}

func (t Tick) Perform(ctx context.Context, c *jobs.Ctx) error {
	t.probe.mu.Lock()
	t.probe.runs["tick"]++
	t.probe.mu.Unlock()
	return nil
}

func testTransactions(t *testing.T, e *env) {
	s := e.open()
	ctx := context.Background()

	tx, end := begin(t, s.DB())
	rolled, err := jobs.Enqueue(tx, Work{Key: "rolled-back"})
	if err != nil {
		t.Fatal(err)
	}
	end(false)
	if _, err := s.Get(ctx, rolled.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("a job enqueued in a rolled-back transaction exists (err %v)", err)
	}

	tx, end = begin(t, s.DB())
	kept, err := jobs.Enqueue(tx, Work{Key: "committed"})
	if err != nil {
		t.Fatal(err)
	}
	// Not visible to fetches before commit.
	recs, err := s.Fetch(ctx, jobs.FetchRequest{Queue: "default", Limit: 10, Node: "n", Now: e.clock.Now(), LeaseUntil: e.clock.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("fetched %d jobs from an uncommitted transaction", len(recs))
	}
	end(true)
	r := e.wantState(s, kept.ID, jobs.Available)
	if r.Type != "jobstest.Work" || r.Queue != "default" || string(r.Args) != `{"key":"committed","mode":""}` || r.MaxAttempts != 3 || r.Attempt != 0 {
		t.Fatalf("stored job is %+v", r)
	}
	if !r.InsertedAt.Equal(jobs.Round(start)) || !r.ScheduledAt.Equal(jobs.Round(start)) {
		t.Fatalf("times: inserted %v scheduled %v, want %v", r.InsertedAt, r.ScheduledAt, start)
	}

	// A panic in the transaction rolls it back too.
	var panicked int64
	func() {
		defer func() { recover() }()
		s.DB().Tx(ctx, func(tx data.Tx) error {
			res, _ := jobs.Enqueue(tx, Work{Key: "panicked"})
			panicked = res.ID
			panic("boom")
		})
	}()
	if _, err := s.Get(ctx, panicked); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("a job enqueued in a panicking transaction exists (err %v)", err)
	}
}

func testRunAndComplete(t *testing.T, e *env) {
	r := e.runner("a")
	res := e.enqueue(r.Store(), Work{Key: "k", Mode: "ok"})
	e.clock.Advance(3 * time.Second)
	if n := e.runDue(r); n != 1 {
		t.Fatalf("ran %d attempts, want 1", n)
	}
	rec := e.wantState(r.Store(), res.ID, jobs.Completed)
	if rec.Attempt != 1 || rec.AttemptedBy != "a" || rec.AttemptedAt == nil || !rec.AttemptedAt.Equal(e.clock.Now()) || rec.FinishedAt == nil || len(rec.Errors) != 0 || rec.LeaseUntil != nil {
		t.Fatalf("completed job is %+v", rec)
	}
	if e.probe.ran("k") != 1 {
		t.Fatalf("Perform ran %d times", e.probe.ran("k"))
	}
	if n := e.runDue(r); n != 0 {
		t.Fatalf("a completed job ran again")
	}
}

// A job keeps the trace id of the transaction that enqueued it, and its
// attempt's events carry it (docs/events.md).
func testTrace(t *testing.T, e *env) {
	r := e.runner("a")
	s := r.Store()
	ctx := trace.WithTrace(context.Background(), "click-1")
	var res jobs.Result
	err := s.DB().Tx(ctx, func(tx data.Tx) error {
		var err error
		res, err = jobs.Enqueue(tx, Work{Key: "traced", Mode: "ok"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.wantState(s, res.ID, jobs.Available); rec.Trace != "click-1" {
		t.Fatalf("stored trace %q, want click-1", rec.Trace)
	}
	untraced := e.enqueue(s, Work{Key: "untraced", Mode: "ok"})
	if rec := e.wantState(s, untraced.ID, jobs.Available); rec.Trace != "" {
		t.Fatalf("a job enqueued with no trace has %q", rec.Trace)
	}
	e.runDue(r)
	var names []string
	for _, ev := range r.Stream().Since(0, trace.Filter{Trace: "click-1"}) {
		names = append(names, ev.Name)
	}
	if got := strings.Join(names, " "); got != "job.enqueue job.start job.finish" {
		t.Fatalf("events of trace click-1: %s", got)
	}
}

func testRetryAndBackoff(t *testing.T, e *env) {
	r := e.runner("a")
	s := r.Store()
	res := e.enqueue(s, Work{Key: "k", Mode: "fail"})
	e.runDue(r)
	rec := e.wantState(s, res.ID, jobs.Retryable)
	if rec.Attempt != 1 || len(rec.Errors) != 1 || rec.Errors[0].Error != "attempt 1 failed" || rec.Errors[0].Attempt != 1 {
		t.Fatalf("after one failure: %+v", rec)
	}
	if want := e.clock.Now().Add(time.Minute); !rec.ScheduledAt.Equal(want) {
		t.Fatalf("retry scheduled at %v, want %v (backoff for attempt 1)", rec.ScheduledAt, want)
	}
	// Not due before the backoff ends.
	e.clock.Advance(59 * time.Second)
	if n := e.runDue(r); n != 0 {
		t.Fatalf("ran during backoff")
	}
	e.clock.Advance(time.Second)
	e.runDue(r)
	rec = e.wantState(s, res.ID, jobs.Retryable)
	if want := e.clock.Now().Add(2 * time.Minute); rec.Attempt != 2 || !rec.ScheduledAt.Equal(want) {
		t.Fatalf("after two failures: attempt %d at %v, want 2 at %v", rec.Attempt, rec.ScheduledAt, want)
	}
	e.clock.Advance(2 * time.Minute)
	e.runDue(r)
	rec = e.wantState(s, res.ID, jobs.Discarded)
	if rec.Attempt != 3 || len(rec.Errors) != 3 || rec.FinishedAt == nil {
		t.Fatalf("discarded job: %+v", rec)
	}

	// A job that succeeds on its third attempt completes.
	ok := e.enqueue(s, Work{Key: "k2", Mode: "fail-until-3"})
	for i := 0; i < 3; i++ {
		e.runDue(r)
		e.clock.Advance(time.Hour)
	}
	e.wantState(s, ok.ID, jobs.Completed)
	if e.probe.ran("k2") != 3 {
		t.Fatalf("ran %d times, want 3", e.probe.ran("k2"))
	}

	// The default backoff grows and has jitter.
	for attempt := 1; attempt < 25; attempt++ {
		d := jobs.DefaultBackoff(attempt)
		base := 15 * time.Second << (attempt - 1)
		if base > 24*time.Hour || attempt > 20 {
			base = 24 * time.Hour
		}
		if d < base || d > base+base/10 {
			t.Fatalf("DefaultBackoff(%d) = %v, want %v plus up to 10%%", attempt, d, base)
		}
	}
}

func testSnooze(t *testing.T, e *env) {
	r := e.runner("a")
	s := r.Store()
	res := e.enqueue(s, Work{Key: "k", Mode: "snooze"})
	e.runDue(r)
	rec := e.wantState(s, res.ID, jobs.Scheduled)
	if rec.Attempt != 0 || len(rec.Errors) != 0 || !rec.ScheduledAt.Equal(e.clock.Now().Add(10*time.Minute)) {
		t.Fatalf("snoozed job: %+v", rec)
	}
	e.clock.Advance(9 * time.Minute)
	if n := e.runDue(r); n != 0 {
		t.Fatal("a snoozed job ran early")
	}
	e.clock.Advance(time.Minute)
	e.runDue(r)
	rec = e.wantState(s, res.ID, jobs.Completed)
	if rec.Attempt != 1 {
		t.Fatalf("attempt %d after snooze and success, want 1 (snoozes don't count)", rec.Attempt)
	}
}

func testCancel(t *testing.T, e *env) {
	r := e.runner("a")
	s := r.Store()
	res := e.enqueue(s, Work{Key: "k", Mode: "cancel"})
	e.runDue(r)
	rec := e.wantState(s, res.ID, jobs.Cancelled)
	if len(rec.Errors) != 1 || !strings.Contains(rec.Errors[0].Error, "no longer needed") || rec.FinishedAt == nil {
		t.Fatalf("cancelled job: %+v", rec)
	}
	e.clock.Advance(24 * time.Hour)
	if n := e.runDue(r); n != 0 || e.probe.ran("k") != 1 {
		t.Fatal("a cancelled job ran again")
	}
}

func testUnique(t *testing.T, e *env) {
	r := e.runner("a")
	s := r.Store()
	first := e.enqueue(s, Work{Key: "u"}, jobs.Unique(time.Hour))
	if first.Duplicate {
		t.Fatal("first unique enqueue is a duplicate")
	}
	dup := e.enqueue(s, Work{Key: "u"}, jobs.Unique(time.Hour))
	if !dup.Duplicate || dup.ID != first.ID {
		t.Fatalf("second enqueue: %+v, want duplicate of %d", dup, first.ID)
	}
	other := e.enqueue(s, Work{Key: "v"}, jobs.Unique(time.Hour))
	if other.Duplicate {
		t.Fatal("different args are a duplicate")
	}
	// Completed jobs still count within the period.
	e.runDue(r)
	if again := e.enqueue(s, Work{Key: "u"}, jobs.Unique(time.Hour)); !again.Duplicate {
		t.Fatal("a completed job within the period didn't count")
	}
	// After the period, a new job is inserted.
	e.clock.Advance(time.Hour + time.Second)
	later := e.enqueue(s, Work{Key: "u"}, jobs.Unique(time.Hour))
	if later.Duplicate {
		t.Fatal("a job outside the period counted as a duplicate")
	}
	// Cancelled and discarded jobs don't count.
	if err := s.Cancel(context.Background(), later.ID, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if res := e.enqueue(s, Work{Key: "u"}, jobs.Unique(time.Hour)); res.Duplicate {
		t.Fatal("a cancelled job counted as a duplicate")
	}
	// Within one transaction, too.
	tx, end := begin(t, s.DB())
	a, _ := jobs.Enqueue(tx, Work{Key: "w"}, jobs.Unique(time.Hour))
	b, _ := jobs.Enqueue(tx, Work{Key: "w"}, jobs.Unique(time.Hour))
	end(true)
	if a.Duplicate || !b.Duplicate || a.ID != b.ID {
		t.Fatalf("same transaction: %+v then %+v", a, b)
	}
	// A job without Unique is never a duplicate.
	if res := e.enqueue(s, Work{Key: "u"}); res.Duplicate {
		t.Fatal("a non-unique enqueue was a duplicate")
	}
}

func testUniqueConcurrent(t *testing.T, e *env) {
	s1, s2 := e.open(), e.open()
	tx1, end1 := begin(t, s1.DB())
	r1, err := jobs.Enqueue(tx1, Work{Key: "c"}, jobs.Unique(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan jobs.Result)
	go func() {
		var r2 jobs.Result
		err := s2.DB().Tx(context.Background(), func(tx data.Tx) error {
			var err error
			r2, err = jobs.Enqueue(tx, Work{Key: "c"}, jobs.Unique(time.Hour))
			return err
		})
		if err != nil {
			t.Error(err)
		}
		done <- r2
	}()
	select {
	case <-done:
		t.Fatal("a concurrent unique insert didn't wait for the first transaction")
	case <-time.After(100 * time.Millisecond):
	}
	end1(true)
	r2 := <-done
	if !r2.Duplicate || r2.ID != r1.ID {
		t.Fatalf("concurrent insert: %+v, want duplicate of %d", r2, r1.ID)
	}

	// If the first transaction rolls back, the second inserts.
	tx1, end1 = begin(t, s1.DB())
	jobs.Enqueue(tx1, Work{Key: "d"}, jobs.Unique(time.Hour))
	go func() {
		var r2 jobs.Result
		s2.DB().Tx(context.Background(), func(tx data.Tx) error {
			r2, _ = jobs.Enqueue(tx, Work{Key: "d"}, jobs.Unique(time.Hour))
			return nil
		})
		done <- r2
	}()
	time.Sleep(50 * time.Millisecond)
	end1(false)
	if r2 := <-done; r2.Duplicate {
		t.Fatal("insert after a rolled-back duplicate was a duplicate")
	}
}

func testScheduled(t *testing.T, e *env) {
	r := e.runner("a")
	s := r.Store()
	in := e.enqueue(s, Work{Key: "in"}, jobs.In(10*time.Minute))
	at := e.enqueue(s, Work{Key: "at"}, jobs.At(start.Add(time.Hour)))
	e.wantState(s, in.ID, jobs.Scheduled)
	if n := e.runDue(r); n != 0 {
		t.Fatal("a scheduled job ran early")
	}
	e.clock.Advance(10 * time.Minute)
	e.runDue(r)
	e.wantState(s, in.ID, jobs.Completed)
	e.wantState(s, at.ID, jobs.Scheduled)
	e.clock.Advance(50 * time.Minute)
	e.runDue(r)
	e.wantState(s, at.ID, jobs.Completed)
}

func testTimeout(t *testing.T, e *env) {
	r := e.runner("a")
	s := r.Store()
	res := e.enqueue(s, Slow{Key: "t", Mode: "timeout"})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Shutdown(context.Background())
	eventually(t, "the timed-out job to be retryable", func() bool { return e.get(s, res.ID).State == jobs.Retryable })
	rec := e.get(s, res.ID)
	if len(rec.Errors) != 1 || rec.Errors[0].Code != jobs.JB506 || !strings.Contains(rec.Errors[0].Error, "timeout") {
		t.Fatalf("timeout errors: %+v", rec.Errors)
	}
}

func testPanic(t *testing.T, e *env) {
	r := e.runner("a")
	s := r.Store()
	res := e.enqueue(s, Work{Key: "p", Mode: "panic"})
	e.runDue(r)
	rec := e.wantState(s, res.ID, jobs.Retryable)
	if len(rec.Errors) != 1 || rec.Errors[0].Code != jobs.JB505 || !strings.Contains(rec.Errors[0].Error, "boom") || !strings.Contains(rec.Errors[0].Stack, "jobstest.Work.Perform") {
		t.Fatalf("panic error: %+v", rec.Errors)
	}
	// The runner keeps working.
	ok := e.enqueue(s, Work{Key: "after"})
	e.runDue(r)
	e.wantState(s, ok.ID, jobs.Completed)
}

func testUnknownType(t *testing.T, e *env) {
	s := e.open()
	var res jobs.Result
	err := s.DB().Tx(context.Background(), func(tx data.Tx) error {
		var err error
		res, err = s.Insert(tx, jobs.NewJob{Type: "gone.OldJob", Queue: "default", Args: []byte(`{}`), MaxAttempts: 2, InsertedAt: jobs.Round(e.clock.Now()), ScheduledAt: jobs.Round(e.clock.Now())})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	r := e.runner("a")
	e.runDue(r)
	rec := e.wantState(s, res.ID, jobs.Retryable)
	if len(rec.Errors) != 1 || rec.Errors[0].Code != jobs.JB501 {
		t.Fatalf("unknown type errors: %+v", rec.Errors)
	}
}

func testCrashRecovery(t *testing.T, e *env) {
	ctx := context.Background()
	dead := e.open()
	res := e.enqueue(dead, Work{Key: "k"})
	now := jobs.Round(e.clock.Now())
	recs, err := dead.Fetch(ctx, jobs.FetchRequest{Queue: "default", Limit: 10, Node: "dead", Now: now, LeaseUntil: now.Add(30 * time.Second)})
	if err != nil || len(recs) != 1 {
		t.Fatalf("fetch: %v %v", recs, err)
	}
	// The node dies without finishing. Before the lease ends, nothing
	// happens; after, the leader rescues the job and runs it.
	live := e.runner("live")
	e.clock.Advance(29 * time.Second)
	if n, _ := live.Store().Rescue(ctx, jobs.Round(e.clock.Now())); n != 0 {
		t.Fatal("rescued a job whose lease hadn't ended")
	}
	e.clock.Advance(2 * time.Second)
	// It waits RescueDelay first, so a job that crashes its node doesn't
	// take the next one down at once.
	if n := e.runDue(live); n != 0 {
		t.Fatal("ran a rescued job before its rescue delay")
	}
	e.wantState(live.Store(), res.ID, jobs.Retryable)
	e.clock.Advance(jobs.RescueDelay(0))
	e.runDue(live)
	rec := e.wantState(live.Store(), res.ID, jobs.Completed)
	if rec.Attempt != 2 || rec.AttemptedBy != "live" || len(rec.Errors) != 1 || rec.Errors[0].Code != jobs.JB511 {
		t.Fatalf("rescued job: %+v", rec)
	}
	// Its history names the node that died holding it.
	if e := rec.Errors[0]; e.Node != "dead" || !strings.Contains(e.Error, "node dead stopped renewing") {
		t.Fatalf("rescue error doesn't name the dead node: %+v", e)
	}
	if d, err := live.Job(ctx, res.ID); err != nil || !slices.ContainsFunc(d.History, func(h jobs.HistoryEntry) bool {
		return strings.Contains(h.Note, "attempt 1 failed: JB511: node dead stopped renewing")
	}) {
		t.Fatalf("history doesn't name the dead node: %+v %v", d, err)
	}
	// The dead node's late finish is rejected.
	err = dead.Finish(ctx, res.ID, jobs.Finish{Node: "dead", Attempt: 1, State: jobs.Completed, At: jobs.Round(e.clock.Now())})
	if !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("late finish: %v, want ErrLeaseLost", err)
	}

	// Leases are renewed while a job runs, so a long job isn't rescued.
	lease := e.enqueue(dead, Work{Key: "l"})
	now = jobs.Round(e.clock.Now())
	dead.Fetch(ctx, jobs.FetchRequest{Queue: "default", Limit: 10, Node: "dead", Now: now, LeaseUntil: now.Add(30 * time.Second)})
	e.clock.Advance(20 * time.Second)
	dead.ExtendLeases(ctx, "dead", []int64{lease.ID}, jobs.Round(e.clock.Now()).Add(30*time.Second))
	e.clock.Advance(20 * time.Second)
	if n, _ := dead.Rescue(ctx, jobs.Round(e.clock.Now())); n != 0 {
		t.Fatal("rescued a job whose lease was renewed")
	}

	// A rescued job with no attempts left is discarded.
	last, err := e.insert(dead, Work{Key: "m"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	e.crash(dead, last.ID)
	if rec := e.wantState(dead, last.ID, jobs.Discarded); rec.Errors[len(rec.Errors)-1].Code != jobs.JB511 {
		t.Fatalf("spent job: %+v", rec.Errors)
	}

	// A job rescued MaxRescues times is discarded with JB517, attempts
	// left or not: it may be what crashes its node.
	crashy, err := e.insert(dead, Work{Key: "n"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < jobs.MaxRescues; i++ {
		e.crash(dead, crashy.ID)
		if i < jobs.MaxRescues-1 {
			e.wantState(dead, crashy.ID, jobs.Retryable)
			e.clock.Advance(jobs.RescueDelay(i))
		}
	}
	rec = e.wantState(dead, crashy.ID, jobs.Discarded)
	if last := rec.Errors[len(rec.Errors)-1]; rec.Attempt != jobs.MaxRescues || last.Code != jobs.JB517 || last.Node != "dead" || !strings.Contains(last.Error, "the last on node dead") {
		t.Fatalf("crashing job: %+v", rec)
	}
}

// insert stores job j with maxAttempts, for tests that need more or
// fewer attempts than its type's Options.
func (e *env) insert(s jobs.Store, j jobs.Job, maxAttempts int) (*jobs.Record, error) {
	args, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	now := jobs.Round(e.clock.Now())
	var id int64
	err = s.DB().Tx(context.Background(), func(tx data.Tx) error {
		res, err := s.Insert(tx, jobs.NewJob{Type: jobs.TypeName(j), Queue: "default", Args: args, MaxAttempts: maxAttempts, InsertedAt: now, ScheduledAt: now})
		id = res.ID
		return err
	})
	if err != nil {
		return nil, err
	}
	return e.get(s, id), nil
}

// crash fetches job id on node "dead", which dies holding it, and
// rescues it once the lease ends.
func (e *env) crash(s jobs.Store, id int64) {
	e.t.Helper()
	ctx := context.Background()
	now := jobs.Round(e.clock.Now())
	recs, err := s.Fetch(ctx, jobs.FetchRequest{Queue: "default", Limit: 10, Node: "dead", Now: now, LeaseUntil: now.Add(30 * time.Second)})
	if err != nil || !slices.ContainsFunc(recs, func(r *jobs.Record) bool { return r.ID == id }) {
		e.t.Fatalf("fetch %d: %v %v", id, recs, err)
	}
	e.clock.Advance(31 * time.Second)
	if _, err := s.Rescue(ctx, jobs.Round(e.clock.Now())); err != nil {
		e.t.Fatal(err)
	}
}

func testGracefulShutdown(t *testing.T, e *env) {
	r := e.runner("a")
	s := r.Store()
	r.ShutdownGrace = 5 * time.Second
	res := e.enqueue(s, Slow{Key: "s"})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	<-e.probe.started
	done := make(chan error)
	go func() { done <- r.Shutdown(context.Background()) }()
	// Shutdown waits for the running job.
	select {
	case <-done:
		t.Fatal("Shutdown returned while a job was running")
	case <-time.After(50 * time.Millisecond):
	}
	// No new jobs start once shutdown begins.
	later := e.enqueue(s, Work{Key: "later"})
	close(e.probe.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	e.wantState(s, res.ID, jobs.Completed)
	e.wantState(s, later.ID, jobs.Available)
}

func testShutdownReleases(t *testing.T, e *env) {
	r := e.runner("a")
	s := r.Store()
	r.ShutdownGrace = 50 * time.Millisecond
	res := e.enqueue(s, Slow{Key: "s"})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	<-e.probe.started
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec := e.wantState(s, res.ID, jobs.Available)
	if rec.Attempt != 0 || len(rec.Errors) != 0 || rec.LeaseUntil != nil {
		t.Fatalf("released job: %+v", rec)
	}
	// Another node picks it up.
	r2 := e.runner("b")
	r2.ShutdownGrace = time.Second
	close(e.probe.release)
	e.runDue(r2)
	e.wantState(s, res.ID, jobs.Completed)
}

func testConcurrentFetch(t *testing.T, e *env) {
	s := e.open()
	ctx := context.Background()
	const n = 50
	s.DB().Tx(ctx, func(tx data.Tx) error {
		for i := 0; i < n; i++ {
			jobs.Enqueue(tx, Work{Key: fmt.Sprint(i)})
		}
		return nil
	})
	var mu sync.Mutex
	seen := map[int64]string{}
	var wg sync.WaitGroup
	for w := 0; w < 5; w++ {
		node := fmt.Sprintf("n%d", w)
		st := e.open()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				now := jobs.Round(e.clock.Now())
				recs, err := st.Fetch(ctx, jobs.FetchRequest{Queue: "default", Limit: 3, Node: node, Now: now, LeaseUntil: now.Add(time.Minute)})
				if err != nil {
					t.Error(err)
					return
				}
				if len(recs) == 0 {
					return
				}
				mu.Lock()
				for _, r := range recs {
					if prev, dup := seen[r.ID]; dup {
						t.Errorf("job %d fetched by %s and %s", r.ID, prev, node)
					}
					seen[r.ID] = node
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != n {
		t.Fatalf("fetched %d jobs, want %d", len(seen), n)
	}
}

func testWorkers(t *testing.T, e *env) {
	// The "slow" queue runs two at a time; the third waits for a slot.
	r := e.runner("a")
	s := r.Store()
	var ids []int64
	for i := 0; i < 3; i++ {
		ids = append(ids, e.enqueue(s, Slow{Key: fmt.Sprint(i)}).ID)
	}
	// Poll rarely: workers must be woken by notifications.
	r.PollInterval = time.Hour
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Shutdown(context.Background())
	<-e.probe.started
	<-e.probe.started
	select {
	case k := <-e.probe.started:
		t.Fatalf("job %s started beyond the queue's concurrency of 2", k)
	case <-time.After(100 * time.Millisecond):
	}
	close(e.probe.release)
	for _, id := range ids {
		eventually(t, fmt.Sprintf("job %d to complete", id), func() bool { return e.get(s, id).State == jobs.Completed })
	}
	// A job enqueued while the runner is idle runs promptly, woken by the
	// store's notification rather than the (hourly) poll.
	res := e.enqueue(s, Work{Key: "prompt"})
	eventually(t, "the new job to complete", func() bool { return e.get(s, res.ID).State == jobs.Completed })
}

func testCronLeader(t *testing.T, e *env) {
	ctx := context.Background()
	a, b := e.runner("a"), e.runner("b")
	for _, r := range []*jobs.Runner{a, b} {
		if err := r.Cron("0 3 * * *", Tick{}); err != nil {
			t.Fatal(err)
		}
	}
	now, until := jobs.Round(e.clock.Now()), jobs.Round(e.clock.Now()).Add(30*time.Second)
	if ok, err := a.Store().TryLead(ctx, "a", now, until); !ok || err != nil {
		t.Fatalf("a didn't become leader: %v", err)
	}
	if ok, _ := b.Store().TryLead(ctx, "b", now, until); ok {
		t.Fatal("b became leader while a leads")
	}
	if ok, _ := a.Store().TryLead(ctx, "a", now, until); !ok {
		t.Fatal("a lost leadership on renewal")
	}

	// At 03:00 both nodes evaluate cron, as their maintenance loops would;
	// only the leader enqueues, and once.
	e.clock.Set(time.Date(2026, 1, 4, 3, 0, 30, 0, time.UTC))
	for _, r := range []*jobs.Runner{a, b, a} {
		now := jobs.Round(e.clock.Now())
		if ok, _ := r.Store().TryLead(ctx, r.Node, now, now.Add(30*time.Second)); ok {
			r.EnqueueCron(ctx, false)
		}
	}
	list, _ := a.Store().List(ctx, jobs.Filter{Type: "jobstest.Tick"})
	if len(list) != 1 {
		t.Fatalf("%d tick jobs, want 1", len(list))
	}
	// A duplicate tick (a second leader, briefly) is deduplicated.
	b.EnqueueCron(ctx, false)
	if list, _ = a.Store().List(ctx, jobs.Filter{Type: "jobstest.Tick"}); len(list) != 1 {
		t.Fatalf("%d tick jobs after a duplicate tick, want 1", len(list))
	}

	// When the leader resigns, the other node takes over and catches up
	// on a tick missed within CronCatchUp.
	if err := a.Store().Resign(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	e.clock.Set(time.Date(2026, 1, 5, 3, 2, 0, 0, time.UTC))
	now = jobs.Round(e.clock.Now())
	if ok, _ := b.Store().TryLead(ctx, "b", now, now.Add(30*time.Second)); !ok {
		t.Fatal("b didn't take over after a resigned")
	}
	b.EnqueueCron(ctx, false)
	if list, _ = a.Store().List(ctx, jobs.Filter{Type: "jobstest.Tick"}); len(list) != 2 {
		t.Fatalf("%d tick jobs after the next day's tick, want 2", len(list))
	}
	b.RunDue(ctx)
	if e.probe.ran("tick") != 2 {
		t.Fatalf("tick ran %d times, want 2", e.probe.ran("tick"))
	}
	// A tick missed by more than CronCatchUp is skipped.
	e.clock.Set(time.Date(2026, 1, 6, 3, 10, 0, 0, time.UTC))
	b.EnqueueCron(ctx, false)
	if list, _ = a.Store().List(ctx, jobs.Filter{Type: "jobstest.Tick"}); len(list) != 2 {
		t.Fatalf("a tick 10 minutes late was enqueued")
	}

	// With both runners started, exactly one leads.
	a.PollInterval, b.PollInterval = 5*time.Millisecond, 5*time.Millisecond
	a.Start()
	b.Start()
	time.Sleep(50 * time.Millisecond)
	if a.IsLeader() == b.IsLeader() {
		t.Fatalf("leaders: a %v, b %v; want exactly one", a.IsLeader(), b.IsLeader())
	}
	a.Shutdown(ctx)
	b.Shutdown(ctx)
}

func testManualRetryAndCancel(t *testing.T, e *env) {
	ctx := context.Background()
	r := e.runner("a")
	s := r.Store()
	res := e.enqueue(s, Work{Key: "k", Mode: "fail"})
	for i := 0; i < 3; i++ {
		e.runDue(r)
		e.clock.Advance(time.Hour)
	}
	e.wantState(s, res.ID, jobs.Discarded)
	if err := r.Retry(ctx, res.ID); err != nil {
		t.Fatal(err)
	}
	rec := e.wantState(s, res.ID, jobs.Available)
	if rec.MaxAttempts != 4 || rec.FinishedAt != nil {
		t.Fatalf("retried job: %+v", rec)
	}
	var je *jobs.Error
	if err := r.Retry(ctx, res.ID); !errors.As(err, &je) || je.Code != jobs.JB508 {
		t.Fatalf("retrying an available job: %v, want JB508", err)
	}
	if err := r.Cancel(ctx, res.ID); err != nil {
		t.Fatal(err)
	}
	e.wantState(s, res.ID, jobs.Cancelled)
	if err := r.Cancel(ctx, res.ID); !errors.As(err, &je) || je.Code != jobs.JB508 {
		t.Fatalf("cancelling a cancelled job: %v, want JB508", err)
	}
	if err := r.Retry(ctx, 99999); !errors.As(err, &je) || je.Code != jobs.JB507 {
		t.Fatalf("retrying a missing job: %v, want JB507", err)
	}
	if _, err := r.Job(ctx, 99999); !errors.As(err, &je) || je.Code != jobs.JB507 {
		t.Fatalf("inspecting a missing job: %v, want JB507", err)
	}

	// Cancelling an executing job makes its worker's finish fail.
	ex := e.enqueue(s, Work{Key: "x"})
	now := jobs.Round(e.clock.Now())
	s.Fetch(ctx, jobs.FetchRequest{Queue: "default", Limit: 10, Node: "a", Now: now, LeaseUntil: now.Add(time.Minute)})
	if err := r.Cancel(ctx, ex.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, ex.ID, jobs.Finish{Node: "a", Attempt: 1, State: jobs.Completed, At: now}); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("finishing a cancelled job: %v, want ErrLeaseLost", err)
	}

	// Enqueue from JSON, as `loom jobs enqueue` does.
	er, err := r.EnqueueJSON(ctx, "Work", []byte(`{"key":"json","mode":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := e.get(s, er.ID); string(got.Args) != `{"key":"json","mode":"ok"}` {
		t.Fatalf("args %s", got.Args)
	}
	if _, err := r.EnqueueJSON(ctx, "Work", []byte(`{"nope":1}`)); !errors.As(err, &je) || je.Code != jobs.JB509 {
		t.Fatalf("bad args: %v, want JB509", err)
	}
	if _, err := r.EnqueueJSON(ctx, "Nope", nil); !errors.As(err, &je) || je.Code != jobs.JB501 {
		t.Fatalf("unknown type: %v, want JB501", err)
	}
}

func testListStatsPrune(t *testing.T, e *env) {
	ctx := context.Background()
	r := e.runner("a")
	s := r.Store()
	done := e.enqueue(s, Work{Key: "done"})
	e.runDue(r)
	e.enqueue(s, Work{Key: "later"}, jobs.In(time.Hour))
	slow := e.enqueue(s, Slow{Key: "slow"})

	list, err := s.List(ctx, jobs.Filter{})
	if err != nil || len(list) != 3 || list[0].ID != slow.ID {
		t.Fatalf("list: %d jobs, err %v; want 3, newest first", len(list), err)
	}
	if list, _ = s.List(ctx, jobs.Filter{Queue: "slow"}); len(list) != 1 {
		t.Fatalf("queue filter: %d", len(list))
	}
	if list, _ = s.List(ctx, jobs.Filter{State: jobs.Completed}); len(list) != 1 || list[0].ID != done.ID {
		t.Fatalf("state filter: %d", len(list))
	}
	if list, _ = s.List(ctx, jobs.Filter{Limit: 2}); len(list) != 2 {
		t.Fatalf("limit: %d", len(list))
	}
	stats, err := s.Stats(ctx)
	if err != nil || len(stats) != 2 || stats[0].Queue != "default" || stats[0].States[jobs.Completed] != 1 || stats[0].States[jobs.Scheduled] != 1 || stats[1].States[jobs.Available] != 1 {
		t.Fatalf("stats: %+v, %v", stats, err)
	}
	// Waiting is the earliest scheduled_at of jobs waiting to run.
	if !stats[0].Waiting.Equal(jobs.Round(e.clock.Now().Add(time.Hour))) || !stats[1].Waiting.Equal(e.get(s, slow.ID).ScheduledAt) {
		t.Fatalf("waiting: %v and %v, want the scheduled job's time and the slow job's", stats[0].Waiting, stats[1].Waiting)
	}

	// Prune keeps jobs until the retention period has passed.
	if n, _ := s.Prune(ctx, jobs.Round(e.clock.Now())); n != 0 {
		t.Fatalf("pruned %d jobs finished at the cutoff", n)
	}
	e.clock.Advance(8 * 24 * time.Hour)
	close(e.probe.release) // let the slow job finish
	if _, err := r.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	n, err := s.Prune(ctx, jobs.Round(e.clock.Now().Add(-7*24*time.Hour)))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d jobs (err %v), want 1", n, err)
	}
	if _, err := s.Get(ctx, done.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatal("the pruned job still exists")
	}
}

func testPagesSearchDelete(t *testing.T, e *env) {
	ctx := context.Background()
	r := e.runner("a")
	s := r.Store()
	var ids []int64
	for _, k := range []string{"alpha", "Beta", "gamma", "beta_2", "delta"} {
		ids = append(ids, e.enqueue(s, Work{Key: k}).ID)
	}

	// Keyset pagination: newest first, two at a time.
	var got []int64
	var before int64
	for {
		page, err := s.List(ctx, jobs.Filter{Limit: 2, Before: before})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, rec := range page {
			got = append(got, rec.ID)
		}
		before = page[len(page)-1].ID
	}
	if len(got) != 5 || got[0] != ids[4] || got[4] != ids[0] {
		t.Fatalf("pages: %v, want %v reversed", got, ids)
	}

	// Search matches args text, ignoring case; % and _ are literal.
	if page, _ := s.List(ctx, jobs.Filter{Search: "beta"}); len(page) != 2 {
		t.Fatalf("search beta: %d jobs, want 2", len(page))
	}
	if page, _ := s.List(ctx, jobs.Filter{Search: "a_2"}); len(page) != 1 || page[0].ID != ids[3] {
		t.Fatalf("search a_2: %d jobs, want beta_2", len(page))
	}
	if page, _ := s.List(ctx, jobs.Filter{Search: "%"}); len(page) != 0 {
		t.Fatalf("search %%: %d jobs, want 0", len(page))
	}

	// Delete removes any job that isn't executing.
	if err := r.Delete(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, ids[0]); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("deleted job: %v", err)
	}
	var je *jobs.Error
	if err := r.Delete(ctx, ids[0]); !errors.As(err, &je) || je.Code != jobs.JB507 {
		t.Fatalf("deleting a missing job: %v, want JB507", err)
	}
	now := jobs.Round(e.clock.Now())
	s.Fetch(ctx, jobs.FetchRequest{Queue: "default", Limit: 1, Node: "a", Now: now, LeaseUntil: now.Add(time.Minute)})
	running, _ := s.List(ctx, jobs.Filter{State: jobs.Executing})
	if len(running) != 1 {
		t.Fatalf("executing: %d", len(running))
	}
	if err := r.Delete(ctx, running[0].ID); !errors.As(err, &je) || je.Code != jobs.JB508 {
		t.Fatalf("deleting an executing job: %v, want JB508", err)
	}
}

func testDashboardActions(t *testing.T, e *env) {
	ctx := context.Background()
	r := e.runner("a")
	if err := r.Cron("0 3 * * *", Tick{probe: e.probe}); err != nil {
		t.Fatal(err)
	}
	s := r.Store()
	a := e.enqueue(s, Work{Key: "a", Mode: "fail"})
	b := e.enqueue(s, Work{Key: "b", Mode: "fail"})
	for i := 0; i < 3; i++ {
		e.runDue(r)
		e.clock.Advance(time.Hour)
	}
	e.wantState(s, a.ID, jobs.Discarded)

	fails, err := r.RecentFailures(ctx, 10)
	if err != nil || len(fails) != 2 {
		t.Fatalf("recent failures: %d, %v", len(fails), err)
	}
	d, err := r.Job(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(d.History); n != 4 || d.History[0].State != jobs.Available || d.History[n-1].State != jobs.Discarded || !strings.Contains(d.History[1].Note, "attempt 1 failed") {
		t.Fatalf("history: %+v", d.History)
	}

	n, err := r.RetryDiscarded(ctx, "")
	if err != nil || n != 2 {
		t.Fatalf("retried %d discarded jobs (%v), want 2", n, err)
	}
	e.wantState(s, a.ID, jobs.Available)
	e.wantState(s, b.ID, jobs.Available)

	cron, _ := r.CronInfo(ctx)
	idx := -1
	for _, c := range cron {
		if !c.Builtin {
			idx = c.Index
		}
	}
	res, err := r.RunCron(ctx, idx)
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.wantState(s, res.ID, jobs.Available); rec.Type != jobs.TypeName(Tick{}) {
		t.Fatalf("cron run now: %+v", rec)
	}
	var je *jobs.Error
	if _, err := r.RunCron(ctx, 99); !errors.As(err, &je) || je.Code != jobs.JB515 {
		t.Fatalf("unknown cron entry: %v, want JB515", err)
	}
}

// testNodes checks the node registry: heartbeats record and replace a
// node's entry, a node goes stale when its lease ends (the moment the
// jobs it holds are rescued), and stale nodes are pruned.
func testNodes(t *testing.T, e *env) {
	ctx := context.Background()
	a, b := e.runner("a"), e.runner("b")
	s := a.Store()
	if nodes, err := a.Nodes(ctx); err != nil || len(nodes) != 0 {
		t.Fatalf("empty registry: %+v %v", nodes, err)
	}
	if err := b.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(5 * time.Second)
	if err := a.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	nodes, err := a.Nodes(ctx)
	if err != nil || len(nodes) != 2 || nodes[0].Name != "a" || nodes[1].Name != "b" {
		t.Fatalf("nodes: %+v %v", nodes, err)
	}
	now := jobs.Round(e.clock.Now())
	na := nodes[0]
	if na.State != jobs.NodeUp || !na.Self || nodes[1].Self || !na.SeenAt.Equal(now) || !na.StartedAt.Equal(now) || !na.LeaseUntil.Equal(now.Add(30*time.Second)) {
		t.Fatalf("node a: %+v", na)
	}
	if len(na.Queues) != 1 || na.Queues[0] != (jobs.NodeQueue{Name: "slow", Concurrency: 2}) {
		t.Fatalf("node a's queues: %+v", na.Queues)
	}

	// A heartbeat replaces the entry, keeping when the node started.
	e.clock.Advance(10 * time.Second)
	a.Queues(jobs.Queue{Name: "mail", Concurrency: 3})
	if err := a.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	nodes, _ = a.Nodes(ctx)
	if n := nodes[0]; !n.StartedAt.Equal(now) || !n.SeenAt.Equal(now.Add(10*time.Second)) || len(n.Queues) != 2 || n.Queues[0].Name != "mail" {
		t.Fatalf("node a after its second heartbeat: %+v", n)
	}

	// A node holding a job, on the same lease, dies. It is up until its
	// lease ends; then it is stale, no longer leads, and its job is
	// rescued, naming it.
	dead := e.open()
	res := e.enqueue(dead, Work{Key: "held"})
	now = jobs.Round(e.clock.Now())
	if _, err := dead.Fetch(ctx, jobs.FetchRequest{Queue: "default", Limit: 1, Node: "dead", Now: now, LeaseUntil: now.Add(30 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	err = dead.Heartbeat(ctx, jobs.Node{Name: "dead", Leader: true, Queues: []jobs.NodeQueue{{Name: "default", Concurrency: 10, Running: 1}}, StartedAt: now, SeenAt: now, LeaseUntil: now.Add(30 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	state := func(name string) jobs.NodeInfo {
		t.Helper()
		nodes, err := a.Nodes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range nodes {
			if n.Name == name {
				return n
			}
		}
		t.Fatalf("no node %s in %+v", name, nodes)
		return jobs.NodeInfo{}
	}
	e.clock.Advance(30 * time.Second)
	if n := state("dead"); n.State != jobs.NodeUp || !n.Leader || n.Queues[0].Running != 1 {
		t.Fatalf("dead node at the end of its lease: %+v", n)
	}
	if n, _ := s.Rescue(ctx, jobs.Round(e.clock.Now())); n != 0 {
		t.Fatal("rescued a job at the end of its lease")
	}
	e.clock.Advance(time.Second)
	if n := state("dead"); n.State != jobs.NodeStale || n.Leader {
		t.Fatalf("dead node after its lease: %+v", n)
	}
	if n, _ := s.Rescue(ctx, jobs.Round(e.clock.Now())); n != 1 {
		t.Fatalf("rescued %d jobs after the lease, want 1", n)
	}
	if rec := e.get(s, res.ID); rec.Errors[0].Node != "dead" {
		t.Fatalf("rescued job: %+v", rec.Errors)
	}

	// Pruning deletes nodes stale since before the cut-off, only.
	if err := a.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneNodes(ctx, now.Add(30*time.Second)); err != nil || n != 1 { // b, last seen long before
		t.Fatalf("pruned %d nodes at the dead node's lease end, want 1 (%v)", n, err)
	}
	if n, err := s.PruneNodes(ctx, now.Add(31*time.Second)); err != nil || n != 1 {
		t.Fatalf("pruned %d nodes after the dead node's lease end, want 1 (%v)", n, err)
	}
	if nodes, _ := a.Nodes(ctx); len(nodes) != 1 || nodes[0].Name != "a" {
		t.Fatalf("after pruning: %+v", nodes)
	}
	if err := s.RemoveNode(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveNode(ctx, "nobody"); err != nil {
		t.Fatal(err)
	}
	if nodes, _ := a.Nodes(ctx); len(nodes) != 0 {
		t.Fatalf("after removing a: %+v", nodes)
	}
}

// testNodesLive checks that started runners keep their entries: the
// leader marked, running jobs counted, and removed at shutdown.
func testNodesLive(t *testing.T, e *env) {
	ctx := context.Background()
	a, b := e.runner("a"), e.runner("b")
	s := a.Store()
	for _, r := range []*jobs.Runner{a, b} {
		r.PollInterval = 5 * time.Millisecond
		r.LeaseDuration = 30 * time.Millisecond // heartbeats every 10ms
		r.ShutdownGrace = time.Second
	}
	e.enqueue(s, Slow{Key: "busy"})
	a.Start()
	b.Start()
	<-e.probe.started
	eventually(t, "both nodes registered, one leading, one job running", func() bool {
		nodes, err := a.Nodes(ctx)
		if err != nil || len(nodes) != 2 {
			return false
		}
		leaders, running := 0, 0
		for _, n := range nodes {
			if n.Leader {
				leaders++
			}
			for _, q := range n.Queues {
				if q.Name == "slow" {
					running += q.Running
				}
			}
		}
		return leaders == 1 && running == 1
	})
	if self := a.Self(); self.Name != "a" || self.SeenAt.IsZero() || len(self.Queues) != 2 {
		t.Fatalf("a.Self(): %+v", self)
	}
	close(e.probe.release)
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if nodes, err := s.Nodes(ctx); err != nil || len(nodes) != 0 {
		t.Fatalf("nodes after shutdown: %+v %v", nodes, err)
	}
}
