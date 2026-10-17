package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moomerman/loom/data"
)

type okJob struct {
	ID string `json:"id"`
}

func (okJob) Perform(context.Context, *Ctx) error { return nil }

type mailJob struct{}

func (mailJob) Options() Options                    { return Options{Queue: "mail"} }
func (mailJob) Perform(context.Context, *Ctx) error { return nil }

type chanJob struct{ C chan int }

func (chanJob) Perform(context.Context, *Ctx) error { return nil }

type depJob struct {
	C chan int `json:"-"` // a dependency, not an argument
}

func (depJob) Perform(context.Context, *Ctx) error { return nil }

type ptrJob struct{}

func (*ptrJob) Perform(context.Context, *Ctx) error { return nil }

type panicJob struct{}

func (panicJob) Perform(context.Context, *Ctx) error { panic("kaboom") }

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return fmt.Sprintf("not a jobs error: %v", err)
}

func TestCron(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse("2006-01-02 15:04", s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct{ expr, from, want string }{
		{"0 3 * * *", "2026-01-04 02:58", "2026-01-04 03:00"},
		{"0 3 * * *", "2026-01-04 03:00", "2026-01-05 03:00"},
		{"*/15 * * * *", "2026-01-04 10:07", "2026-01-04 10:15"},
		{"0 9 * * 1-5", "2026-01-03 12:00", "2026-01-05 09:00"}, // Saturday -> Monday
		{"0 0 * * 7", "2026-01-05 00:00", "2026-01-11 00:00"},   // 7 is Sunday
		{"30 12 1,15 * *", "2026-01-02 00:00", "2026-01-15 12:30"},
		{"0 0 1 * 1", "2026-01-02 00:00", "2026-01-05 00:00"}, // day of month OR Monday
		{"0 0 29 2 *", "2026-01-01 00:00", "2028-02-29 00:00"},
		{"5-10/5 * * * *", "2026-01-01 00:06", "2026-01-01 00:10"},
		{"0 0 1 1 *", "2026-12-31 23:59", "2027-01-01 00:00"},
	}
	for _, tc := range tests {
		s, err := ParseCron(tc.expr)
		if err != nil {
			t.Fatalf("%q: %v", tc.expr, err)
		}
		if got := s.Next(at(tc.from)); !got.Equal(at(tc.want)) {
			t.Errorf("%q after %s = %s, want %s", tc.expr, tc.from, got.Format("2006-01-02 15:04"), tc.want)
		}
	}
	never, _ := ParseCron("0 0 31 2 *")
	if got := never.Next(at("2026-01-01 00:00")); !got.IsZero() {
		t.Errorf("Feb 31 fired at %v", got)
	}
	for _, bad := range []string{"* * * *", "60 * * * *", "* * * * MON", "*/0 * * * *", "5-1 * * * *", "@daily", "x * * * *"} {
		_, err := ParseCron(bad)
		if code(err) != JB504 {
			t.Errorf("%q: %v, want JB504", bad, err)
		}
	}
	_, err := ParseCron("* * * * MON")
	if !strings.Contains(err.Error(), "names like MON or JAN aren't supported") || !strings.Contains(err.Error(), "fix: use five fields") {
		t.Errorf("error text: %v", err)
	}
}

func TestRegister(t *testing.T) {
	r := NewRunner(NewMemoryStore(MemoryOptions{}))
	if err := r.Register(okJob{}, depJob{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(okJob{}); code(err) != JB502 || !strings.Contains(err.Error(), "registered twice") {
		t.Errorf("duplicate: %v", err)
	}
	if err := r.Register(&ptrJob{}); code(err) != JB502 || !strings.Contains(err.Error(), "pass it by value: ptrJob{...}") {
		t.Errorf("pointer: %v", err)
	}
	if err := r.Register(chanJob{}); code(err) != JB503 || !strings.Contains(err.Error(), "json:\"-\"") {
		t.Errorf("chan field: %v", err)
	}
	if err := r.Cron("bad", okJob{}); code(err) != JB504 {
		t.Errorf("bad cron: %v", err)
	}
	if err := r.Queues(Queue{}); code(err) != JB512 {
		t.Errorf("unnamed queue: %v", err)
	}
}

func TestStartValidation(t *testing.T) {
	r := NewRunner(NewMemoryStore(MemoryOptions{}))
	r.Register(mailJob{})
	err := r.Start()
	if code(err) != JB510 || !strings.Contains(err.Error(), `app.Queues(jobs.Queue{Name: "mail", Concurrency: 10})`) {
		t.Fatalf("missing queue: %v", err)
	}
	r = NewRunner(NewMemoryStore(MemoryOptions{}))
	r.Cron("0 3 * * *", okJob{})
	if err := r.Start(); code(err) != JB501 || !strings.Contains(err.Error(), "app.Jobs(okJob{})") {
		t.Fatalf("unregistered cron job: %v", err)
	}
	r = NewRunner(NewMemoryStore(MemoryOptions{}))
	r.Register(mailJob{})
	r.Queues(Queue{Name: "mail"})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if err := r.Queues(Queue{Name: "late"}); code(err) != JB512 {
		t.Errorf("queues after start: %v", err)
	}
	r.Shutdown(context.Background())
}

func TestEnqueueValidation(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{})
	ctx := context.Background()
	var kept data.Tx
	s.DB().Tx(ctx, func(tx data.Tx) error {
		kept = tx
		if _, err := Enqueue(tx, chanJob{C: make(chan int)}); code(err) != JB503 {
			t.Errorf("chan args: %v", err)
		}
		if _, err := Enqueue(tx, &ptrJob{}); code(err) != JB502 {
			t.Errorf("pointer: %v", err)
		}
		return nil
	})
	if o := OptionsOf(okJob{}); o != (Options{Queue: "default", MaxAttempts: 20, Timeout: time.Minute}) {
		t.Errorf("defaults: %+v", o)
	}
	// A database with no store bound.
	data.NewLocal().Tx(ctx, func(tx data.Tx) error {
		if _, err := Enqueue(tx, okJob{}); code(err) != JB514 {
			t.Errorf("unbound DB: %v", err)
		}
		return nil
	})
	_ = kept
}

type hiddenJob struct{ userID string }

func (hiddenJob) Perform(ctx context.Context, c *Ctx) error { return nil }

type handleJob struct {
	ID string
	//lint:ignore U1000 a dependency tagged json:"-" is allowed, which the test checks
	db *data.DB `json:"-"`
}

func (handleJob) Perform(ctx context.Context, c *Ctx) error { return nil }

// Mistakes that would lose a job silently fail the enqueue instead.
func TestEnqueueChecksTheRunner(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{})
	ctx := context.Background()
	// No runner bound: any job type may be enqueued, for another node.
	s.DB().Tx(ctx, func(tx data.Tx) error {
		if _, err := Enqueue(tx, okJob{ID: "x"}); err != nil {
			t.Errorf("no runner: %v", err)
		}
		return nil
	})
	r := NewRunner(s)
	if err := r.Register(handleJob{}); err != nil {
		t.Errorf("a tagged unexported field: %v", err)
	}
	if err := r.Register(hiddenJob{}); code(err) != JB516 {
		t.Errorf("Register, unexported field: %v", err)
	}
	s.DB().Tx(ctx, func(tx data.Tx) error {
		if _, err := Enqueue(tx, okJob{ID: "x"}); code(err) != JB501 {
			t.Errorf("unregistered: %v", err)
		}
		if _, err := Enqueue(tx, hiddenJob{userID: "u1"}); code(err) != JB516 {
			t.Errorf("Enqueue, unexported field: %v", err)
		}
		if _, err := Enqueue(tx, handleJob{ID: "x"}); err != nil {
			t.Errorf("registered: %v", err)
		}
		return nil
	})
}

type fatalTB struct{ msg string }

func (f *fatalTB) Helper() {}
func (f *fatalTB) Fatalf(format string, args ...any) {
	f.msg = fmt.Sprintf(format, args...)
	panic(f)
}

func TestPerformNowPanic(t *testing.T) {
	tb := &fatalTB{}
	func() {
		defer func() { recover() }()
		PerformNow(tb, panicJob{})
	}()
	if !strings.Contains(tb.msg, "jobs.panicJob: Perform panicked: kaboom") || !strings.Contains(tb.msg, "jobs.panicJob.Perform") {
		t.Errorf("message: %s", tb.msg)
	}
}

// TestCodesDocumented keeps docs/errors.md in sync with the JB codes.
func TestCodesDocumented(t *testing.T) {
	data, err := os.ReadFile("../docs/errors.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^### (JB\d{3})$`).FindAllStringSubmatch(string(data), -1) {
		documented[m[1]] = true
	}
	src, err := os.ReadFile("errors.go")
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\t(JB\d{3}) = "JB\d{3}"`).FindAllStringSubmatch(string(src), -1) {
		known[m[1]] = true
		if !documented[m[1]] {
			t.Errorf("%s is not documented in docs/errors.md", m[1])
		}
	}
	for c := range documented {
		if !known[c] {
			t.Errorf("docs/errors.md documents %s, which errors.go doesn't define", c)
		}
	}
}

// slowFetchStore holds its first Fetch until released, so a test can
// begin Shutdown while a fetch is in flight. entered is buffered, so the
// first Fetch signals even if it arrives before the test waits for it.
type slowFetchStore struct {
	*MemoryStore
	first            sync.Once
	entered, release chan struct{}
}

func (s *slowFetchStore) Fetch(ctx context.Context, req FetchRequest) ([]*Record, error) {
	s.first.Do(func() {
		s.entered <- struct{}{}
		<-s.release
	})
	return s.MemoryStore.Fetch(ctx, req)
}

// TestShutdownDuringFetch checks that jobs a fetch returns after Shutdown
// began go back to their queue unrun.
func TestShutdownDuringFetch(t *testing.T) {
	mem := NewMemoryStore(MemoryOptions{})
	s := &slowFetchStore{MemoryStore: mem, entered: make(chan struct{}, 1), release: make(chan struct{})}
	r := NewRunner(s)
	r.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	r.PollInterval = time.Hour
	if err := r.Register(okJob{}); err != nil {
		t.Fatal(err)
	}
	var res Result
	mem.DB().Tx(context.Background(), func(tx data.Tx) error {
		var err error
		res, err = Enqueue(tx, okJob{ID: "x"})
		return err
	})
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	<-s.entered // the dispatcher's first fetch is in the store
	done := make(chan error)
	go func() { done <- r.Shutdown(context.Background()) }()
	for {
		r.mu.Lock()
		stopping := r.stopping
		r.mu.Unlock()
		if stopping {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(s.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	rec, err := mem.Get(context.Background(), res.ID)
	if err != nil || rec.State != Available || rec.Attempt != 0 {
		t.Fatalf("job after shutdown: %+v, %v; want available, never attempted", rec, err)
	}
}

// flakyFinish fails Finish a number of times, like a dropped connection.
type flakyFinish struct {
	*MemoryStore
	fails int
}

func (s *flakyFinish) Finish(ctx context.Context, id int64, f Finish) error {
	if s.fails > 0 {
		s.fails--
		return errors.New("connection reset")
	}
	return s.MemoryStore.Finish(ctx, id, f)
}

// A store error recording an outcome is retried, so a brief outage
// doesn't leave the job executing until it is rescued and run again.
func TestFinishRetried(t *testing.T) {
	defer func(d []time.Duration) { finishRetries = d }(finishRetries)
	finishRetries = []time.Duration{0, 0}
	s := &flakyFinish{MemoryStore: NewMemoryStore(MemoryOptions{}), fails: 2}
	r := NewRunner(s)
	r.Register(okJob{})
	var id int64
	s.DB().Tx(context.Background(), func(tx data.Tx) error {
		res, err := Enqueue(tx, okJob{})
		id = res.ID
		return err
	})
	if _, err := r.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec, _ := s.Get(context.Background(), id); rec.State != Completed {
		t.Fatalf("job is %s after two failed finishes, want completed", rec.State)
	}
}

// countingStore counts the calls the runner makes to the store.
type countingStore struct {
	*MemoryStore
	mu               sync.Mutex
	inserts, fetches int
}

func (s *countingStore) Insert(tx data.Tx, j NewJob) (Result, error) {
	s.mu.Lock()
	s.inserts++
	s.mu.Unlock()
	return s.MemoryStore.Insert(tx, j)
}

func (s *countingStore) Fetch(ctx context.Context, r FetchRequest) ([]*Record, error) {
	s.mu.Lock()
	s.fetches++
	s.mu.Unlock()
	return s.MemoryStore.Fetch(ctx, r)
}

func (s *countingStore) counts() (inserts, fetches int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inserts, s.fetches
}

// The leader evaluates cron every PollInterval, but enqueues each tick
// once: a tick it already enqueued costs no store call.
func TestLeaderEnqueuesEachCronTickOnce(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, 1, 4, 3, 0, 0, 0, time.UTC))
	s := &countingStore{MemoryStore: NewMemoryStore(MemoryOptions{Clock: clock})}
	r := NewRunner(s)
	r.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := r.Register(okJob{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Cron("*/5 * * * *", okJob{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 3 {
		r.lead(ctx)
		clock.Advance(time.Second)
	}
	if n, _ := s.counts(); n != 2 { // the 03:00 tick and the hourly prune's
		t.Fatalf("%d inserts after three passes in one tick, want 2", n)
	}
	clock.Advance(5 * time.Minute)
	r.lead(ctx)
	if n, _ := s.counts(); n != 3 {
		t.Fatalf("%d inserts after the next tick, want 3", n)
	}
	// An explicit EnqueueCron still checks the store.
	if err := r.EnqueueCron(ctx, false); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.counts(); n != 4 {
		t.Fatalf("%d inserts after EnqueueCron, want 4", n)
	}
}

// An idle queue looks for jobs every IdlePollInterval, not every
// PollInterval, and a new job still wakes it at once.
func TestIdleQueuePollsSlowly(t *testing.T) {
	s := &countingStore{MemoryStore: NewMemoryStore(MemoryOptions{})}
	r := NewRunner(s)
	r.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	r.PollInterval = 5 * time.Millisecond
	r.IdlePollInterval = time.Hour
	if err := r.Register(okJob{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Shutdown(context.Background())
	time.Sleep(100 * time.Millisecond)
	if _, n := s.counts(); n != 1 {
		t.Fatalf("idle default queue fetched %d times, want once", n)
	}
	var res Result
	s.DB().Tx(context.Background(), func(tx data.Tx) error {
		var err error
		res, err = Enqueue(tx, okJob{ID: "now"})
		return err
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec, err := s.Get(context.Background(), res.ID)
		if err == nil && rec.State == Completed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job not run after enqueue: %+v, %v", rec, err)
		}
		time.Sleep(time.Millisecond)
	}
}
