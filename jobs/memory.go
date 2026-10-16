package jobs

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moomerman/loom/data"
)

// MemoryStore keeps jobs in memory: for development, tests, and apps that
// keep their data in memory. Jobs are lost when the process exits. It
// behaves exactly like the Postgres store, including transactions: a job
// enqueued in a loom.Tx is kept only if the transaction commits.
//
//	store := jobs.NewMemoryStore(jobs.MemoryOptions{})
//	store.DB().Tx(ctx, func(tx loom.Tx) error {
//		_, err := jobs.Enqueue(tx, SendWelcome{UserID: id})
//		return err
//	})
type MemoryStore struct {
	clock Clock
	db    *data.DB

	mu     sync.Mutex
	nextID int64
	jobs   map[int64]*Record
	txs    map[data.Tx]*memTx // transactions with pending inserts
	// uniqueHeld maps a unique key to a channel closed when the
	// transaction inserting a job with that key ends: the in-memory
	// equivalent of Postgres's transaction-scoped advisory lock.
	uniqueHeld  map[string]chan struct{}
	leader      string
	leaderUntil time.Time
	nodes       map[string]Node
	subs        map[int]func(string)
	nextSub     int
}

// MemoryOptions configure a MemoryStore.
type MemoryOptions struct {
	// DB is the database whose transactions jobs are enqueued in. It may
	// be a real database (jobs then commit and roll back with its
	// transactions) or, by default, a new loom.NewLocalDB().
	DB *data.DB
	// Clock defaults to the real clock.
	Clock Clock
}

// NewMemoryStore returns an empty in-memory store, bound to its DB.
func NewMemoryStore(opts MemoryOptions) *MemoryStore {
	if opts.DB == nil {
		opts.DB = data.NewLocal()
	}
	if opts.Clock == nil {
		opts.Clock = RealClock{}
	}
	s := &MemoryStore{clock: opts.Clock, db: opts.DB, jobs: map[int64]*Record{}, txs: map[data.Tx]*memTx{}, uniqueHeld: map[string]chan struct{}{}, subs: map[int]func(string){}, nodes: map[string]Node{}}
	Bind(opts.DB, s)
	return s
}

// Clock returns the store's clock.
func (s *MemoryStore) Clock() Clock { return s.clock }

// DB returns the store's database.
func (s *MemoryStore) DB() *data.DB { return s.db }

// memTx holds a transaction's inserts until it ends.
type memTx struct {
	mu      sync.Mutex
	pending []*Record
	held    []string // unique keys this transaction holds
}

// Insert adds a job to tx's pending inserts.
func (s *MemoryStore) Insert(tx data.Tx, j NewJob) (Result, error) {
	s.mu.Lock()
	mt := s.txs[tx]
	if mt == nil {
		mt = &memTx{}
		s.txs[tx] = mt
		tx.AfterCommit(func() { s.end(tx, true) })
		tx.AfterRollback(func() { s.end(tx, false) })
	}
	s.mu.Unlock()

	mt.mu.Lock()
	defer mt.mu.Unlock()
	if j.UniqueKey != "" {
		// Wait for any other transaction inserting the same key to end,
		// then check committed jobs and our own pending ones.
		if !slices.Contains(mt.held, j.UniqueKey) {
			for {
				s.mu.Lock()
				ch, busy := s.uniqueHeld[j.UniqueKey]
				if !busy {
					s.uniqueHeld[j.UniqueKey] = make(chan struct{})
					s.mu.Unlock()
					break
				}
				s.mu.Unlock()
				<-ch
			}
			mt.held = append(mt.held, j.UniqueKey)
		}
		s.mu.Lock()
		for _, r := range s.jobs {
			if isDuplicate(r, j) {
				s.mu.Unlock()
				return Result{ID: r.ID, Duplicate: true}, nil
			}
		}
		s.mu.Unlock()
		for _, r := range mt.pending {
			if isDuplicate(r, j) {
				return Result{ID: r.ID, Duplicate: true}, nil
			}
		}
	}
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.mu.Unlock()
	mt.pending = append(mt.pending, recordFor(id, j))
	return Result{ID: id}, nil
}

// end finishes a transaction, keeping its jobs if it committed, and wakes
// workers for queues that gained available jobs.
func (s *MemoryStore) end(tx data.Tx, commit bool) {
	s.mu.Lock()
	mt := s.txs[tx]
	delete(s.txs, tx)
	s.mu.Unlock()
	if mt == nil {
		return
	}
	mt.mu.Lock()
	defer mt.mu.Unlock()
	s.mu.Lock()
	var queues []string
	if commit {
		for _, r := range mt.pending {
			s.jobs[r.ID] = r
			if r.State == Available && !slices.Contains(queues, r.Queue) {
				queues = append(queues, r.Queue)
			}
		}
	}
	for _, k := range mt.held {
		close(s.uniqueHeld[k])
		delete(s.uniqueHeld, k)
	}
	s.mu.Unlock()
	s.notify(queues)
}

// isDuplicate reports whether existing job r makes inserting j a duplicate.
func isDuplicate(r *Record, j NewJob) bool {
	return r.UniqueKey == j.UniqueKey && !r.InsertedAt.Before(j.UniqueSince) && r.State != Discarded && r.State != Cancelled
}

func recordFor(id int64, j NewJob) *Record {
	state := Available
	if j.ScheduledAt.After(j.InsertedAt) {
		state = Scheduled
	}
	return &Record{
		ID: id, Type: j.Type, Queue: j.Queue, Args: append([]byte(nil), j.Args...), State: state,
		MaxAttempts: j.MaxAttempts, InsertedAt: j.InsertedAt, ScheduledAt: j.ScheduledAt,
		Errors: []AttemptError{}, UniqueKey: j.UniqueKey, Trace: j.Trace,
	}
}

func (s *MemoryStore) notify(queues []string) {
	s.mu.Lock()
	fns := make([]func(string), 0, len(s.subs))
	for _, fn := range s.subs {
		fns = append(fns, fn)
	}
	s.mu.Unlock()
	for _, q := range queues {
		for _, fn := range fns {
			fn(q)
		}
	}
}

func due(r *Record, now time.Time) bool {
	switch r.State {
	case Available:
		return true
	case Scheduled, Retryable:
		return !r.ScheduledAt.After(now)
	}
	return false
}

// Fetch claims due jobs.
func (s *MemoryStore) Fetch(ctx context.Context, req FetchRequest) ([]*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ready []*Record
	for _, r := range s.jobs {
		if r.Queue == req.Queue && due(r, req.Now) {
			ready = append(ready, r)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if !ready[i].ScheduledAt.Equal(ready[j].ScheduledAt) {
			return ready[i].ScheduledAt.Before(ready[j].ScheduledAt)
		}
		return ready[i].ID < ready[j].ID
	})
	if len(ready) > req.Limit {
		ready = ready[:req.Limit]
	}
	out := make([]*Record, 0, len(ready))
	for _, r := range ready {
		r.State = Executing
		r.Attempt++
		r.AttemptedAt = ptr(req.Now)
		r.AttemptedBy = req.Node
		r.LeaseUntil = ptr(req.LeaseUntil)
		out = append(out, copyRecord(r))
	}
	return out, nil
}

// Finish ends an attempt.
func (s *MemoryStore) Finish(ctx context.Context, id int64, f Finish) error {
	s.mu.Lock()
	r, ok := s.jobs[id]
	if !ok || r.State != Executing || r.AttemptedBy != f.Node || r.Attempt != f.Attempt {
		s.mu.Unlock()
		return ErrLeaseLost
	}
	r.State = f.State
	r.LeaseUntil = nil
	if f.Error != nil {
		r.Errors = append(r.Errors, *f.Error)
	}
	if f.Uncount {
		r.Attempt--
	}
	if f.State.Finished() {
		r.FinishedAt = ptr(f.At)
	} else {
		r.ScheduledAt = f.At
	}
	queue, available := r.Queue, r.State == Available
	s.mu.Unlock()
	if available {
		s.notify([]string{queue})
	}
	return nil
}

// ExtendLeases renews leases.
func (s *MemoryStore) ExtendLeases(ctx context.Context, node string, ids []int64, until time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if r, ok := s.jobs[id]; ok && r.State == Executing && r.AttemptedBy == node {
			r.LeaseUntil = ptr(until)
		}
	}
	return nil
}

// Rescue frees jobs whose lease expired, after RescueDelay; a job
// rescued MaxRescues times is discarded (JB517).
func (s *MemoryStore) Rescue(ctx context.Context, now time.Time) (int, error) {
	s.mu.Lock()
	n := 0
	var queues []string
	for _, r := range s.jobs {
		if r.State != Executing || r.LeaseUntil == nil || !r.LeaseUntil.Before(now) {
			continue
		}
		n++
		r.LeaseUntil = nil
		rescues := 0
		for _, e := range r.Errors {
			if e.Code == JB511 {
				rescues++
			}
		}
		switch {
		case rescues+1 >= MaxRescues:
			r.State, r.FinishedAt = Discarded, ptr(now)
			r.Errors = append(r.Errors, CrashedError(r.Attempt, now, r.AttemptedBy))
			continue
		case r.Attempt >= r.MaxAttempts:
			r.State, r.FinishedAt = Discarded, ptr(now)
		default:
			r.State, r.ScheduledAt = Retryable, now.Add(RescueDelay(rescues))
			queues = append(queues, r.Queue)
		}
		r.Errors = append(r.Errors, RescuedError(r.Attempt, now, r.AttemptedBy))
	}
	s.mu.Unlock()
	s.notify(queues)
	return n, nil
}

// MaxRescues is how many times a job may lose its lease before it is
// discarded (JB517): a job that crashes its node (out of memory, a
// fatal error) would otherwise take a node down on every attempt.
const MaxRescues = 3

// RescueDelay is how long a rescued job waits before running again,
// given how many times it was rescued before: 15s, then 30s.
func RescueDelay(rescues int) time.Duration { return 15 * time.Second << rescues }

// RescuedError is the error recorded on a job rescued from node, which
// stopped renewing its lease. It names the node, so a job's history
// says which node died holding it.
func RescuedError(attempt int, now time.Time, node string) AttemptError {
	return AttemptError{Attempt: attempt, At: now, Code: JB511, Node: node,
		Error: fmt.Sprintf("node %s stopped renewing this job's lease (it crashed or lost its connection); the job was rescued", node)}
}

// CrashedError is the error recorded on a job discarded because it lost
// its lease MaxRescues times, the last time on node.
func CrashedError(attempt int, now time.Time, node string) AttemptError {
	return AttemptError{Attempt: attempt, At: now, Code: JB517, Node: node,
		Error: fmt.Sprintf("the job lost its lease %d times, the last on node %s, so it may be crashing its node; it was discarded rather than run again", MaxRescues, node)}
}

// Prune deletes old finished jobs.
func (s *MemoryStore) Prune(ctx context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, r := range s.jobs {
		if r.State.Finished() && r.FinishedAt != nil && r.FinishedAt.Before(before) {
			delete(s.jobs, id)
			n++
		}
	}
	return n, nil
}

// Get returns a job.
func (s *MemoryStore) Get(ctx context.Context, id int64) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return copyRecord(r), nil
}

// List returns matching jobs, newest first.
func (s *MemoryStore) List(ctx context.Context, f Filter) ([]*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*Record{}
	for _, r := range s.jobs {
		if (f.Queue == "" || r.Queue == f.Queue) && (f.State == "" || r.State == f.State) && (f.Type == "" || r.Type == f.Type) &&
			(f.Before == 0 || r.ID < f.Before) && (f.Search == "" || strings.Contains(strings.ToLower(string(r.Args)), strings.ToLower(f.Search))) {
			out = append(out, copyRecord(r))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Delete deletes a job that isn't executing.
func (s *MemoryStore) Delete(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if r.State == Executing {
		return ErrState
	}
	delete(s.jobs, id)
	return nil
}

// Retry makes a job run now.
func (s *MemoryStore) Retry(ctx context.Context, id int64, now time.Time) error {
	s.mu.Lock()
	r, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	if r.State == Executing || r.State == Available {
		s.mu.Unlock()
		return ErrState
	}
	r.State, r.ScheduledAt, r.FinishedAt = Available, now, nil
	r.MaxAttempts = max(r.MaxAttempts, r.Attempt+1)
	queue := r.Queue
	s.mu.Unlock()
	s.notify([]string{queue})
	return nil
}

// Cancel cancels an unfinished job.
func (s *MemoryStore) Cancel(ctx context.Context, id int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if r.State.Finished() {
		return ErrState
	}
	r.State, r.FinishedAt, r.LeaseUntil = Cancelled, ptr(now), nil
	return nil
}

// Stats counts jobs by queue and state.
func (s *MemoryStore) Stats(ctx context.Context) ([]QueueStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byQueue := map[string]*QueueStats{}
	for _, r := range s.jobs {
		qs := byQueue[r.Queue]
		if qs == nil {
			qs = &QueueStats{Queue: r.Queue, States: map[State]int{}}
			byQueue[r.Queue] = qs
		}
		qs.States[r.State]++
		if waiting(r.State) && (qs.Waiting.IsZero() || r.ScheduledAt.Before(qs.Waiting)) {
			qs.Waiting = r.ScheduledAt
		}
	}
	out := []QueueStats{}
	for _, qs := range byQueue {
		out = append(out, *qs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Queue < out[j].Queue })
	return out, nil
}

// waiting reports whether a job in state st is waiting to run.
func waiting(st State) bool { return st == Available || st == Scheduled || st == Retryable }

// TryLead claims leadership with a lease.
func (s *MemoryStore) TryLead(ctx context.Context, node string, now, until time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leader == "" || s.leader == node || s.leaderUntil.Before(now) {
		s.leader, s.leaderUntil = node, until
		return true, nil
	}
	return false, nil
}

// Resign gives up leadership.
func (s *MemoryStore) Resign(ctx context.Context, node string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leader == node {
		s.leader = ""
	}
	return nil
}

// Heartbeat records a node in the registry.
func (s *MemoryStore) Heartbeat(ctx context.Context, n Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n.Queues = append([]NodeQueue{}, n.Queues...)
	s.nodes[n.Name] = n
	return nil
}

// Nodes returns the registry, by name.
func (s *MemoryStore) Nodes(ctx context.Context) ([]Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Node{}
	for _, n := range s.nodes {
		n.Queues = append([]NodeQueue{}, n.Queues...)
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RemoveNode deletes a node's entry.
func (s *MemoryStore) RemoveNode(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.nodes, name)
	return nil
}

// PruneNodes deletes entries whose lease ended before before.
func (s *MemoryStore) PruneNodes(ctx context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for name, node := range s.nodes {
		if node.LeaseUntil.Before(before) {
			delete(s.nodes, name)
			n++
		}
	}
	return n, nil
}

// Subscribe registers fn for availability notifications.
func (s *MemoryStore) Subscribe(fn func(string)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSub++
	id := s.nextSub
	s.subs[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subs, id)
	}
}

// Close does nothing; the jobs stay in memory.
func (s *MemoryStore) Close() error { return nil }

func ptr(t time.Time) *time.Time { return &t }

func copyRecord(r *Record) *Record {
	c := *r
	c.Args = append([]byte(nil), r.Args...)
	c.Errors = append([]AttemptError{}, r.Errors...)
	return &c
}
