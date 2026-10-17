package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	mrand "math/rand/v2"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/pubsub"
	"github.com/moomerman/loom/trace"
)

// Queue configures a queue's workers.
type Queue struct {
	Name        string `json:"name"`
	Concurrency int    `json:"concurrency"` // jobs run at once on each node; default 10
}

// DefaultConcurrency is a queue's concurrency when none is given.
const DefaultConcurrency = 10

// Runner runs jobs from a store: a supervised pool of workers per queue,
// lease renewal, rescue of jobs held by dead nodes, cron, and pruning. A
// Loom app has one (loom.App.Jobs); set its fields before Start.
type Runner struct {
	// Node names this process in AttemptedBy. Defaults to host-pid-random.
	Node string
	// PubSub carries Ctx.Broadcast messages. The app sets it to its own.
	PubSub pubsub.PubSub
	// Events receives the runner's events (docs/events.md): job.start,
	// job.finish, cron.tick, jobs.leader, jobs.rescue, and an error
	// event for each failure. The app sets it to its own stream. Left
	// nil, the runner makes one that logs to Logger, when it first
	// emits.
	Events *trace.Stream
	// Logger receives job failures and runner errors when Events is nil.
	Logger *slog.Logger
	// OnError, if set, is also called with each job failure.
	OnError func(err error)
	// PollInterval is how often workers look for due jobs when no
	// notification wakes them, and how often the leader runs cron and
	// rescues jobs. Default 1s.
	PollInterval time.Duration
	// IdlePollInterval is how often a queue whose last fetch found
	// nothing looks again when nothing wakes it. New jobs, manual retries and
	// rescues wake their queue at once (Store.Subscribe), so this only
	// bounds how late a scheduled job or a retry after its backoff can
	// start on an idle queue. Default 5s.
	IdlePollInterval time.Duration
	// LeaseDuration is how long a fetched job is held without renewal
	// before another node may rescue it. Leases are renewed every third
	// of it. Default 30s.
	LeaseDuration time.Duration
	// ShutdownGrace is how long Shutdown waits for running jobs before
	// releasing them back to their queue. Default 15s.
	ShutdownGrace time.Duration
	// PruneHooks run with the built-in Prune job, given the time now,
	// each deleting other old bookkeeping by its own retention. The app
	// adds its own.
	PruneHooks []func(ctx context.Context, now time.Time) error
	// Retention is how long completed, discarded and cancelled jobs are
	// kept before the built-in prune job deletes them. Default 7 days.
	Retention time.Duration
	// NodeRetention is how long a stale node stays in the node registry
	// before the built-in prune job deletes it. Default 1h.
	NodeRetention time.Duration
	// CronCatchUp is how late a cron tick may still be enqueued, e.g.
	// after a leader change. Older missed ticks are skipped. Default 5m.
	CronCatchUp time.Duration
	// Backoff is the delay before retrying after a failed attempt.
	// Defaults to exponential backoff with jitter (DefaultBackoff).
	Backoff func(attempt int) time.Duration
	// ChangeInterval coalesces the announcements of job changes on Topic:
	// at most one per interval. Zero announces each change at once, as
	// tests want. Default 250ms.
	ChangeInterval time.Duration
	// StatsMaxAge is how long QueueInfo may reuse the store's job counts,
	// so dashboards rendering at once share one count query. Each
	// announcement on Topic also starts a fresh count. Zero counts on
	// every call. Default 1s.
	StatsMaxAge time.Duration
	// Manual makes Start validate the configuration but start nothing:
	// no workers, no polling, no cron. Jobs run only when the caller
	// calls EnqueueCron and RunDue, as loomtest.Jobs does, so a test's
	// jobs never run behind its back, on another goroutine.
	Manual bool

	store Store
	clock Clock

	eventsOnce sync.Once
	events     *trace.Stream // Events, or one on Logger (stream)

	mu       sync.Mutex
	types    map[string]Job // by type name: the prototype given to Register
	queues   []*queue
	cron     []*cronEntry
	running  map[int64]*runningJob
	started  bool
	stopping bool
	leader   bool
	stop     chan struct{} // closed to stop fetching and background loops
	// startedAt and seenAt are this node's registry times: its first and
	// latest successful heartbeats.
	startedAt time.Time
	seenAt    time.Time
	loops     sync.WaitGroup
	workers   sync.WaitGroup
	unsub     func()

	changeMu    sync.Mutex
	changedIDs  map[int64]bool // changed since the last announcement; 0 for unknown
	changeTimer bool           // an announcement is scheduled

	statsMu   sync.Mutex
	stats     []QueueStats
	statsTime time.Time // wall time of stats
}

type queue struct {
	Queue
	wake    chan struct{}
	running int
	// recent finishes on this node, for throughput
	finished []finish
}

type finish struct {
	at time.Time
	ok bool
}

type runningJob struct {
	rec      *Record
	cancel   context.CancelFunc
	released bool // released at shutdown; its outcome is not recorded
}

type cronEntry struct {
	sched   *Schedule
	job     Job
	typ     string
	args    []byte
	opts    Options
	builtin bool

	lastTick time.Time
	lastID   int64
}

// NewRunner returns a runner for store with default settings. It
// registers and schedules the built-in Prune job.
func NewRunner(store Store) *Runner {
	r := &Runner{
		Node:             defaultNode(),
		Logger:           slog.New(slog.NewTextHandler(os.Stderr, nil)),
		PollInterval:     time.Second,
		IdlePollInterval: 5 * time.Second,
		LeaseDuration:    30 * time.Second,
		ShutdownGrace:    DefaultShutdownGrace,
		Retention:        7 * 24 * time.Hour,
		NodeRetention:    DefaultNodeRetention,
		CronCatchUp:      5 * time.Minute,
		Backoff:          DefaultBackoff,
		ChangeInterval:   250 * time.Millisecond,
		StatsMaxAge:      time.Second,
		store:            store,
		clock:            store.Clock(),
		types:            map[string]Job{},
		running:          map[int64]*runningJob{},
		stop:             make(chan struct{}),
	}
	r.types[TypeName(Prune{})] = Prune{runner: r}
	r.cron = append(r.cron, &cronEntry{sched: mustCron("0 * * * *"), job: Prune{}, typ: TypeName(Prune{}), args: []byte("{}"), opts: OptionsOf(Prune{}), builtin: true})
	if db := store.DB(); db != nil {
		db.SetValue(runnerKey{}, r) // so Enqueue can check the type is registered
	}
	return r
}

type runnerKey struct{}

// registered checks, for Enqueue, that the runner bound to tx's database
// (if any) runs jobs of job's type. A job no node runs would be retried
// until discarded, so the mistake is reported where it is made.
func registered(db *data.DB, job Job) error {
	r, ok := db.Value(runnerKey{}).(*Runner)
	if !ok {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.types[TypeName(job)]; ok {
		return nil
	}
	return unregistered(TypeName(job), r.typeNames())
}

func defaultNode() string {
	host, _ := os.Hostname()
	b := make([]byte, 3)
	rand.Read(b)
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(b))
}

func mustCron(expr string) *Schedule {
	s, err := ParseCron(expr)
	if err != nil {
		panic(err)
	}
	return s
}

// DefaultBackoff waits 15s after the first failed attempt, doubling with
// each attempt up to a day, plus up to 10% random jitter so failing jobs
// don't retry in lockstep.
func DefaultBackoff(attempt int) time.Duration {
	d := 15 * time.Second * time.Duration(math.Pow(2, float64(min(attempt, 20)-1)))
	if d > 24*time.Hour || d <= 0 {
		d = 24 * time.Hour
	}
	return d + time.Duration(mrand.Int64N(int64(d/10)+1))
}

// Store returns the runner's store.
func (r *Runner) Store() Store { return r.store }

// Clock returns the runner's clock: its store's.
func (r *Runner) Clock() Clock { return r.clock }

// Register makes job types runnable on this node. Each value is the type's
// prototype: fields tagged `json:"-"` (a database handle, say) are copied
// into every job of the type before Perform. Errors are JB502 and JB503.
func (r *Runner) Register(jobs ...Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, j := range jobs {
		if err := checkType(j); err != nil {
			return err
		}
		if _, err := encodeArgs(j); err != nil {
			return err
		}
		name := TypeName(j)
		if _, dup := r.types[name]; dup && name != TypeName(Prune{}) {
			return &Error{Code: JB502, Msg: fmt.Sprintf("job type %s is registered twice", name), Fix: "register each job type once, at startup"}
		}
		r.types[name] = j
	}
	return nil
}

// Queues configures queues. Jobs in a queue that isn't configured never
// run, so Start fails with JB510 if a registered type uses one. The
// "default" queue always exists (concurrency 10 unless configured).
func (r *Runner) Queues(qs ...Queue) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return &Error{Code: JB512, Msg: "queues must be configured before the runner starts", Fix: "call Queues before Start (app.Queues before app.Run)"}
	}
	for _, q := range qs {
		if q.Name == "" {
			return &Error{Code: JB512, Msg: "a queue has no name", Fix: `name every queue, e.g. jobs.Queue{Name: "mail", Concurrency: 10}`}
		}
		if q.Concurrency < 0 {
			return &Error{Code: JB512, Msg: fmt.Sprintf("queue %q has negative concurrency", q.Name), Fix: "use a positive Concurrency, or 0 for the default of 10"}
		}
		if q.Concurrency == 0 {
			q.Concurrency = DefaultConcurrency
		}
		if existing := r.queue(q.Name); existing != nil {
			existing.Concurrency = q.Concurrency
			continue
		}
		r.queues = append(r.queues, &queue{Queue: q, wake: make(chan struct{}, 1)})
	}
	return nil
}

func (r *Runner) queue(name string) *queue {
	for _, q := range r.queues {
		if q.Name == name {
			return q
		}
	}
	return nil
}

// Cron enqueues job on a schedule (see ParseCron), once per cluster: the
// leader node enqueues it as a unique job for each tick, so a tick missed
// during a leader change, or seen by two nodes, runs once. job's type must
// be registered. Errors are JB504.
func (r *Runner) Cron(expr string, job Job) error {
	sched, err := ParseCron(expr)
	if err != nil {
		return err
	}
	if err := checkType(job); err != nil {
		return err
	}
	args, err := encodeArgs(job)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cron = append(r.cron, &cronEntry{sched: sched, job: job, typ: TypeName(job), args: args, opts: OptionsOf(job)})
	return nil
}

// Validate checks the configuration as Start does, without starting
// anything: every registered type's queue has workers (JB510) and every
// cron entry's type is registered (JB501). loomtest.Validate runs it, so
// tests catch what Start would.
func (r *Runner) Validate() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.validate()
}

// validate checks the configuration before the runner starts.
func (r *Runner) validate() error {
	if r.queue(DefaultQueue) == nil {
		r.queues = append(r.queues, &queue{Queue: Queue{Name: DefaultQueue, Concurrency: DefaultConcurrency}, wake: make(chan struct{}, 1)})
	}
	names := make([]string, 0, len(r.types))
	for name := range r.types {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		q := OptionsOf(r.types[name]).Queue
		if r.queue(q) == nil {
			return &Error{
				Code: JB510,
				Msg:  fmt.Sprintf("job type %s uses queue %q, which has no workers; its jobs would never run", name, q),
				Fix:  fmt.Sprintf("configure the queue: app.Queues(jobs.Queue{Name: %q, Concurrency: 10})", q),
			}
		}
	}
	for _, e := range r.cron {
		if _, ok := r.types[e.typ]; !ok {
			return unregistered(e.typ, r.typeNames())
		}
	}
	return nil
}

func unregistered(name string, known []string) *Error {
	return &Error{
		Code: JB501,
		Msg:  fmt.Sprintf("job type %s is not registered on this node (registered: %s)", name, strings.Join(known, ", ")),
		Fix:  fmt.Sprintf("register it at startup: app.Jobs(%s{})", shortName(name)),
	}
}

func shortName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

func (r *Runner) typeNames() []string {
	names := []string{}
	for n := range r.types {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Start validates the configuration and starts the workers and
// background loops. Stop them with Shutdown.
func (r *Runner) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return nil
	}
	if err := r.validate(); err != nil {
		return err
	}
	if r.Manual {
		return nil
	}
	r.started = true
	r.unsub = r.store.Subscribe(func(queue string) {
		r.wakeQueue(queue)
		r.changed()
	})
	for _, q := range r.queues {
		r.loops.Add(1)
		go r.dispatch(q)
	}
	r.loops.Add(2)
	go r.heartbeat()
	go r.maintain()
	return nil
}

// wakeQueue makes a queue's dispatcher look for jobs now.
func (r *Runner) wakeQueue(name string) {
	r.mu.Lock()
	q := r.queue(name)
	r.mu.Unlock()
	if q != nil {
		select {
		case q.wake <- struct{}{}:
		default:
		}
	}
}

// dispatch fetches jobs for a queue whenever it has free slots and is
// woken: by a notification, a finished job, or the poll timer. The timer
// waits PollInterval after a fetch that found jobs and IdlePollInterval
// after one that found none, so idle queues don't query the store every
// second on every node.
func (r *Runner) dispatch(q *queue) {
	defer r.loops.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-q.wake:
		case <-timer.C:
		}
		wait := r.PollInterval
		if !r.fetch(q) {
			wait = max(r.IdlePollInterval, r.PollInterval)
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
	}
}

// storeCtx bounds one call to the store: a third of the lease, at least
// five seconds. A hung connection then fails that call, which is retried
// on the next tick, instead of stopping the queue's fetches, every
// running job's heartbeat or the leader's rescue and cron for good.
func (r *Runner) storeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), max(r.LeaseDuration/3, 5*time.Second))
}

// fetch claims due jobs for q's free slots and starts them. It reports
// whether the queue may have more to do: false only when the store had no
// due jobs for it.
func (r *Runner) fetch(q *queue) bool {
	r.mu.Lock()
	free := q.Concurrency - q.running
	stopping := r.stopping
	r.mu.Unlock()
	if free <= 0 || stopping {
		return true
	}
	now := Round(r.clock.Now())
	ctx, cancel := r.storeCtx()
	defer cancel()
	recs, err := r.store.Fetch(ctx, FetchRequest{Queue: q.Name, Limit: free, Node: r.Node, Now: now, LeaseUntil: now.Add(r.LeaseDuration)})
	if err != nil {
		r.fail(ctx, "loom jobs: fetching jobs", "queue", q.Name, "err", err)
		return true
	}
	if len(recs) == 0 {
		return false
	}
	ids := make([]int64, len(recs))
	for i, rec := range recs {
		ids[i] = rec.ID
	}
	r.changed(ids...)
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		// Shutdown began while the store was fetching. No job may start
		// once it has, so put these back unrun.
		for _, rec := range recs {
			ctx, cancel := r.storeCtx()
			err := r.store.Finish(ctx, rec.ID, Finish{Node: r.Node, Attempt: rec.Attempt, State: Available, At: now, Uncount: true})
			cancel()
			if err != nil && !errors.Is(err, ErrLeaseLost) {
				r.fail(ctx, "loom jobs: releasing job at shutdown", "job", rec.ID, "err", err)
			}
		}
		r.changed(ids...)
		return true
	}
	cancels := make([]context.CancelFunc, len(recs))
	ctxs := make([]context.Context, len(recs))
	for i, rec := range recs {
		ctxs[i], cancels[i] = context.WithCancel(context.Background())
		q.running++
		r.running[rec.ID] = &runningJob{rec: rec, cancel: cancels[i]}
	}
	r.workers.Add(len(recs))
	r.mu.Unlock()
	for i, rec := range recs {
		ctx, cancel := ctxs[i], cancels[i]
		go func() {
			defer r.workers.Done()
			defer cancel()
			r.execute(ctx, rec, false)
			r.mu.Lock()
			q.running--
			delete(r.running, rec.ID)
			r.mu.Unlock()
			select {
			case q.wake <- struct{}{}:
			default:
			}
		}()
	}
	return true
}

// heartbeat records this node in the registry at once, then renews the
// leases of its running jobs and its own registry entry, on the same
// lease, every third of LeaseDuration.
func (r *Runner) heartbeat() {
	defer r.loops.Done()
	r.beat()
	t := time.NewTicker(max(r.LeaseDuration/3, time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			r.extendLeases()
			r.beat()
		}
	}
}

func (r *Runner) extendLeases() {
	r.mu.Lock()
	ids := make([]int64, 0, len(r.running))
	for id, j := range r.running {
		if !j.released {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	until := Round(r.clock.Now()).Add(r.LeaseDuration)
	ctx, cancel := r.storeCtx()
	defer cancel()
	if err := r.store.ExtendLeases(ctx, r.Node, ids, until); err != nil {
		r.fail(ctx, "loom jobs: renewing leases", "err", err)
	}
}

// maintain runs the leader's duties: rescue and cron.
func (r *Runner) maintain() {
	defer r.loops.Done()
	t := time.NewTicker(r.PollInterval)
	defer t.Stop()
	for {
		ctx, cancel := r.storeCtx()
		r.lead(ctx)
		cancel()
		select {
		case <-r.stop:
			return
		case <-t.C:
		}
	}
}

// lead claims or renews leadership and, if this node leads, rescues
// expired jobs and enqueues due cron ticks.
func (r *Runner) lead(ctx context.Context) {
	now := Round(r.clock.Now())
	ok, err := r.store.TryLead(ctx, r.Node, now, now.Add(r.LeaseDuration))
	if err != nil {
		r.fail(ctx, "loom jobs: claiming leadership", "err", err)
		return
	}
	r.mu.Lock()
	was := r.leader
	r.leader = ok
	r.mu.Unlock()
	if ok != was {
		r.stream().Emit(ctx, trace.JobsLeader, "node", r.Node, "leader", ok)
		r.beat() // the registry shows the new leader at once
	}
	if !ok {
		return
	}
	if n, err := r.store.Rescue(ctx, now); err != nil {
		r.fail(ctx, "loom jobs: rescuing jobs", "err", err)
	} else if n > 0 {
		r.stream().Emit(ctx, trace.JobsRescue, "msg", "loom jobs: rescued jobs whose node stopped renewing their lease", "count", n)
		r.changed()
	}
	r.enqueueCron(ctx, now, true, false)
}

// IsLeader reports whether this node led the cluster at its last check.
func (r *Runner) IsLeader() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.leader
}

// enqueueCron enqueues the latest tick of each entry within CronCatchUp
// of now, as a unique job per entry and tick. Unless again is set, it
// skips a tick this node already enqueued: the leader runs it every
// PollInterval, and the unique check would otherwise run each time.
func (r *Runner) enqueueCron(ctx context.Context, now time.Time, builtins, again bool) error {
	r.mu.Lock()
	entries := append([]*cronEntry(nil), r.cron...)
	r.mu.Unlock()
	var errs []error
	for _, e := range entries {
		if e.builtin && !builtins {
			continue
		}
		tick := e.sched.latest(now.Add(-r.CronCatchUp), now)
		if tick.IsZero() {
			continue
		}
		r.mu.Lock()
		seen := e.lastTick.Equal(tick)
		r.mu.Unlock()
		if seen && !again {
			continue
		}
		nj := NewJob{
			Type: e.typ, Queue: e.opts.Queue, Args: e.args, MaxAttempts: e.opts.MaxAttempts,
			InsertedAt: now, ScheduledAt: now,
			UniqueKey:   uniqueKey("cron", e.sched.expr, e.typ, e.args, tick.Unix()),
			UniqueSince: tick,
			Trace:       trace.NewID(), // each tick is an action of its own
		}
		tctx := trace.WithTrace(ctx, nj.Trace)
		res, err := r.insert(tctx, nj)
		if err != nil {
			errs = append(errs, err)
			r.fail(tctx, "loom jobs: enqueueing cron job", "cron", e.sched.expr, "type", e.typ, "err", err)
			continue
		}
		r.stream().Emit(tctx, trace.CronTick, "cron", e.sched.expr, "type", e.typ, "id", res.ID, "duplicate", res.Duplicate)
		r.mu.Lock()
		e.lastTick, e.lastID = tick, res.ID
		r.mu.Unlock()
		if !res.Duplicate {
			r.changed(res.ID)
		}
	}
	return errors.Join(errs...)
}

// Stream returns the stream the runner emits to: Events, or the one it
// made on Logger.
func (r *Runner) Stream() *trace.Stream { return r.stream() }

// stream is where the runner emits: Events, or a stream logging to
// Logger, made the first time it is needed so a Logger set after
// NewRunner is the one used.
func (r *Runner) stream() *trace.Stream {
	r.eventsOnce.Do(func() {
		r.events = r.Events
		if r.events == nil {
			r.events = trace.NewStream(trace.Slog(r.Logger))
		}
	})
	return r.events
}

// fail emits an error event for a failure of the runner itself.
func (r *Runner) fail(ctx context.Context, msg string, attrs ...any) {
	r.stream().Emit(ctx, trace.Error, append([]any{"msg", msg}, attrs...)...)
}

// insert enqueues a job in a transaction of its own.
func (r *Runner) insert(ctx context.Context, nj NewJob) (res Result, err error) {
	err = r.store.DB().Tx(ctx, func(tx data.Tx) error {
		res, err = r.store.Insert(tx, nj)
		return err
	})
	return res, err
}

// execute runs one attempt of a fetched job and records its outcome.
// sync runs Perform on the calling goroutine (RunDue).
func (r *Runner) execute(ctx context.Context, rec *Record, sync bool) {
	ctx = trace.WithTrace(ctx, rec.Trace)
	start := time.Now()
	r.stream().Emit(ctx, trace.JobStart, "id", rec.ID, "type", rec.Type, "queue", rec.Queue, "attempt", rec.Attempt)
	r.mu.Lock()
	proto, ok := r.types[rec.Type]
	r.mu.Unlock()
	var job Job
	var err error
	timeout := DefaultTimeout
	if !ok {
		err = unregistered(rec.Type, r.typeNames())
	} else {
		if job, err = decodeJob(proto, rec.Args, false); err != nil {
			err = &Error{Code: JB509, Msg: fmt.Sprintf("the stored args of job %d don't fit %s: %v", rec.ID, rec.Type, err), Fix: "make the job type accept the stored args, or cancel the job with `loom jobs cancel`"}
		} else {
			timeout = OptionsOf(job).Timeout
		}
	}
	if err == nil {
		err = r.perform(ctx, job, rec, timeout, sync)
	}
	if ctx.Err() != nil && r.wasReleased(rec.ID) {
		return // shutdown released it; another node will run it
	}
	r.record(ctx, rec, err, start)
}

// perform runs Perform with the job's timeout. If Perform ignores its
// context and overruns, the attempt fails with JB506 and its goroutine is
// left to finish on its own.
func (r *Runner) perform(ctx context.Context, job Job, rec *Record, timeout time.Duration, sync bool) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := &Ctx{id: rec.ID, attempt: rec.Attempt, maxAttempts: rec.MaxAttempts, clock: r.clock, store: r.store, ps: r.PubSub, trace: rec.Trace, events: r.stream()}
	timedOut := func() error {
		return &Error{Code: JB506, Msg: fmt.Sprintf("Perform ran past its %s timeout", timeout), Fix: "make Perform return when ctx is done, raise Options.Timeout, or split the work into smaller jobs"}
	}
	if sync {
		err := callPerform(ctx, job, c)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && err != nil {
			return timedOut()
		}
		return err
	}
	done := make(chan error, 1)
	go func() { done <- callPerform(ctx, job, c) }()
	select {
	case err := <-done:
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && err != nil {
			return timedOut()
		}
		return err
	case <-ctx.Done():
		select {
		case err := <-done: // returned promptly when cancelled
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && err != nil {
				return timedOut()
			}
			return err
		case <-time.After(100 * time.Millisecond):
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return timedOut()
		}
		return ctx.Err()
	}
}

func (r *Runner) wasReleased(id int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	j := r.running[id]
	return j != nil && j.released
}

// record stores an attempt's outcome, which started at start.
func (r *Runner) record(ctx context.Context, rec *Record, err error, start time.Time) {
	now := Round(r.clock.Now())
	f := Finish{Node: r.Node, Attempt: rec.Attempt}
	var snooze *snoozeError
	var cancel *cancelError
	switch {
	case err == nil:
		f.State, f.At = Completed, now
	case errors.As(err, &snooze):
		f.State, f.At, f.Uncount = Scheduled, Round(now.Add(snooze.d)), true
	case errors.As(err, &cancel):
		f.State, f.At, f.Error = Cancelled, now, attemptError(rec.Attempt, now, err)
	default:
		f.Error = attemptError(rec.Attempt, now, err)
		if rec.Attempt >= rec.MaxAttempts {
			f.State, f.At = Discarded, now
		} else {
			f.State, f.At = Retryable, Round(now.Add(r.Backoff(rec.Attempt)))
		}
	}
	if f.Error != nil {
		f.Error.Node = r.Node
		e := &Error{Code: f.Error.Code, Msg: fmt.Sprintf("job %d (%s) attempt %d/%d failed: %s; now %s", rec.ID, rec.Type, rec.Attempt, rec.MaxAttempts, f.Error.Error, f.State)}
		var je *Error
		switch {
		case errors.As(err, &je):
			e.Fix = je.Fix
		case f.Error.Code == JB505:
			e.Fix = fmt.Sprintf("fix the panic at the top of the stack shown by `loom jobs inspect %d`", rec.ID)
		default:
			e.Fix = fmt.Sprintf("see `loom jobs inspect %d`", rec.ID)
		}
		r.stream().Emit(ctx, trace.Error, "msg", e.Msg, "code", e.Code, "fix", e.Fix, "job", rec.ID, "type", rec.Type)
		if r.OnError != nil {
			r.OnError(e)
		}
	}
	state := string(f.State)
	if f.State == Scheduled {
		state = "snoozed"
	}
	attrs := []any{"id", rec.ID, "type", rec.Type, "queue", rec.Queue, "attempt", rec.Attempt, "ms", time.Since(start), "state", state}
	if f.Error != nil {
		attrs = append(attrs, "code", f.Error.Code)
	}
	r.stream().Emit(ctx, trace.JobFinish, attrs...)
	if err := r.finish(rec.ID, f); err != nil {
		if errors.Is(err, ErrLeaseLost) {
			r.stream().Emit(ctx, trace.Error, "msg", "loom jobs: job finished after losing its lease (it was rescued or cancelled); outcome not recorded", "job", rec.ID, "code", JB511)
		} else {
			r.fail(ctx, "loom jobs: recording job outcome", "job", rec.ID, "err", err)
		}
	}
	r.changed(rec.ID)
	r.mu.Lock()
	if q := r.queue(rec.Queue); q != nil {
		q.finished = append(q.finished, finish{at: now, ok: f.State == Completed || f.State == Scheduled})
		cut := 0
		for cut < len(q.finished) && now.Sub(q.finished[cut].at) > time.Minute {
			cut++
		}
		q.finished = q.finished[cut:]
	}
	r.mu.Unlock()
}

// finishRetries are the waits between tries to record an outcome. A
// store error (a dropped connection, a failover) is usually brief, and
// an outcome not recorded leaves the job executing until its lease ends,
// when it is rescued and runs again.
var finishRetries = []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}

// finish records f, retrying store errors. ErrLeaseLost after a retry
// means an earlier try was recorded and only its reply was lost.
func (r *Runner) finish(id int64, f Finish) error {
	for i := 0; ; i++ {
		ctx, stop := r.storeCtx()
		err := r.store.Finish(ctx, id, f)
		stop()
		switch {
		case err == nil:
			return nil
		case errors.Is(err, ErrLeaseLost):
			if i > 0 {
				return nil
			}
			return err
		case i == len(finishRetries):
			return err
		}
		time.Sleep(finishRetries[i])
	}
}

func attemptError(attempt int, now time.Time, err error) *AttemptError {
	ae := &AttemptError{Attempt: attempt, At: now, Error: err.Error()}
	var p *panicked
	var je *Error
	switch {
	case errors.As(err, &p):
		ae.Code, ae.Stack = JB505, p.stack
	case errors.As(err, &je):
		ae.Code, ae.Error = je.Code, je.Msg
	}
	return ae
}

// Shutdown stops fetching, waits for running jobs until ctx is done or
// ShutdownGrace passes, then cancels the rest and releases them back to
// their queue without counting the attempt. It also gives up leadership.
func (r *Runner) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	if !r.started || r.stopping {
		r.mu.Unlock()
		return nil
	}
	r.stopping = true
	close(r.stop)
	r.mu.Unlock()
	r.loops.Wait()
	if r.unsub != nil {
		r.unsub()
	}

	done := make(chan struct{})
	go func() { r.workers.Wait(); close(done) }()
	grace := time.NewTimer(r.ShutdownGrace)
	defer grace.Stop()
	select {
	case <-done:
	case <-grace.C:
		r.release()
	case <-ctx.Done():
		r.release()
	}
	<-done
	rctx, cancel := r.storeCtx()
	defer cancel()
	err := r.store.Resign(rctx, r.Node)
	r.mu.Lock()
	r.leader = false
	r.mu.Unlock()
	r.leave()
	return err
}

// release puts running jobs back in their queue and cancels them.
func (r *Runner) release() {
	r.mu.Lock()
	var jobs []*runningJob
	for _, j := range r.running {
		j.released = true
		jobs = append(jobs, j)
	}
	r.mu.Unlock()
	now := Round(r.clock.Now())
	for _, j := range jobs {
		ctx, cancel := r.storeCtx()
		err := r.store.Finish(ctx, j.rec.ID, Finish{Node: r.Node, Attempt: j.rec.Attempt, State: Available, At: now, Uncount: true})
		cancel()
		if err != nil && !errors.Is(err, ErrLeaseLost) {
			r.fail(ctx, "loom jobs: releasing job at shutdown", "job", j.rec.ID, "err", err)
		}
		j.cancel()
	}
}

// RunDue runs every due job on the calling goroutine, one at a time,
// until none is due, and returns how many attempts it ran. It doesn't
// need Start. Tests use it (through loomtest.Jobs) for deterministic runs.
func (r *Runner) RunDue(ctx context.Context) (int, error) {
	r.mu.Lock()
	if err := r.validate(); err != nil {
		r.mu.Unlock()
		return 0, err
	}
	queues := append([]*queue(nil), r.queues...)
	r.mu.Unlock()
	n := 0
	for {
		now := Round(r.clock.Now())
		if _, err := r.store.Rescue(ctx, now); err != nil {
			return n, err
		}
		ran := 0
		for _, q := range queues {
			recs, err := r.store.Fetch(ctx, FetchRequest{Queue: q.Name, Limit: 1, Node: r.Node, Now: now, LeaseUntil: now.Add(r.LeaseDuration)})
			if err != nil {
				return n, err
			}
			for _, rec := range recs {
				r.execute(ctx, rec, true)
				ran++
			}
		}
		n += ran
		if ran == 0 {
			return n, nil
		}
		if n > 10000 {
			return n, &Error{Code: JB512, Msg: "jobs kept becoming due: over 10,000 attempts in one run", Fix: "check for a job that snoozes for 0 or enqueues itself"}
		}
	}
}

// EnqueueCron enqueues due cron ticks at the clock's time, as the leader
// does each PollInterval. builtins includes the built-in Prune entry.
func (r *Runner) EnqueueCron(ctx context.Context, builtins bool) error {
	return r.enqueueCron(ctx, Round(r.clock.Now()), builtins, true)
}

// NextCron returns the earliest cron tick after t, or the zero time.
func (r *Runner) NextCron(t time.Time, builtins bool) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	var next time.Time
	for _, e := range r.cron {
		if e.builtin && !builtins {
			continue
		}
		if n := e.sched.Next(t); !n.IsZero() && (next.IsZero() || n.Before(next)) {
			next = n
		}
	}
	return next
}

// DefaultShutdownGrace is a Runner's ShutdownGrace unless set.
const DefaultShutdownGrace = 15 * time.Second

// Prune is the built-in job that deletes completed, discarded and
// cancelled jobs older than the runner's Retention, nodes stale for
// longer than its NodeRetention, and runs the runner's PruneHooks. It
// runs hourly.
type Prune struct {
	runner *Runner `json:"-"`
}

// Perform deletes old finished jobs.
func (p Prune) Perform(ctx context.Context, c *Ctx) error {
	if p.runner == nil {
		return nil
	}
	now := c.Now()
	if _, err := c.Store().Prune(ctx, Round(now.Add(-p.runner.Retention))); err != nil {
		return err
	}
	if _, err := c.Store().PruneNodes(ctx, Round(now.Add(-p.runner.NodeRetention))); err != nil {
		return err
	}
	for _, h := range p.runner.PruneHooks {
		if err := h(ctx, now); err != nil {
			return err
		}
	}
	return nil
}
