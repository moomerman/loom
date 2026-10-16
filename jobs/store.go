package jobs

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/moomerman/loom/data"
)

// State is a job's place in its life cycle:
//
//	scheduled → available → executing → completed | retryable | discarded | cancelled
//
// A retryable job becomes available again when its backoff ends; a
// snoozed job goes back to scheduled.
type State string

// Job states.
const (
	Scheduled State = "scheduled" // waiting for its scheduled time
	Available State = "available" // ready to run
	Executing State = "executing" // running on a node
	Completed State = "completed" // Perform returned nil
	Retryable State = "retryable" // failed; will run again after a backoff
	Discarded State = "discarded" // failed on its last attempt
	Cancelled State = "cancelled" // stopped by jobs.Cancel or `loom jobs cancel`
)

// States lists every state in life-cycle order.
var States = []State{Scheduled, Available, Executing, Completed, Retryable, Discarded, Cancelled}

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	for _, x := range States {
		if s == x {
			return true
		}
	}
	return false
}

// Finished reports whether s is final.
func (s State) Finished() bool { return s == Completed || s == Discarded || s == Cancelled }

// Record is a stored job.
type Record struct {
	ID          int64           `json:"id"`
	Type        string          `json:"type"`
	Queue       string          `json:"queue"`
	Args        json.RawMessage `json:"args"`
	State       State           `json:"state"`
	Attempt     int             `json:"attempt"` // attempts started, including a running one
	MaxAttempts int             `json:"max_attempts"`
	InsertedAt  time.Time       `json:"inserted_at"`
	ScheduledAt time.Time       `json:"scheduled_at"`
	AttemptedAt *time.Time      `json:"attempted_at"` // start of the latest attempt
	FinishedAt  *time.Time      `json:"finished_at"`  // completed, discarded or cancelled
	AttemptedBy string          `json:"attempted_by"` // node of the latest attempt
	LeaseUntil  *time.Time      `json:"lease_until"`  // while executing: rescued if not renewed by then
	Errors      []AttemptError  `json:"errors"`
	UniqueKey   string          `json:"unique_key"`
	Trace       string          `json:"trace,omitempty"` // the action that enqueued it (docs/events.md)
}

// AttemptError records a failed attempt.
type AttemptError struct {
	Attempt int       `json:"attempt"`
	At      time.Time `json:"at"`
	Code    string    `json:"code,omitempty"` // JB505 for a panic, JB506 for a timeout
	Node    string    `json:"node,omitempty"` // the node the attempt ran on
	Error   string    `json:"error"`
	Stack   string    `json:"stack,omitempty"` // for panics
}

// FetchRequest asks a store for jobs to run.
type FetchRequest struct {
	Queue      string
	Limit      int
	Node       string    // the fetching node, recorded as AttemptedBy
	Now        time.Time // jobs scheduled at or before Now are due
	LeaseUntil time.Time // the lease given to fetched jobs
}

// Finish ends an attempt.
type Finish struct {
	Node    string // must match the job's AttemptedBy
	Attempt int    // must match the job's Attempt
	State   State  // Completed, Retryable, Discarded, Cancelled, Scheduled (snooze) or Available (release)
	// At is the finish time for Completed, Discarded and Cancelled, and
	// the time the job runs again for Retryable, Scheduled and Available.
	At    time.Time
	Error *AttemptError // appended to the job's errors, if set
	// Uncount undoes the attempt (snooze, and release at shutdown).
	Uncount bool
}

// Filter selects jobs to list. Zero fields match everything.
type Filter struct {
	Queue string
	State State
	Type  string
	// Search matches jobs whose args, as JSON text, contain it, ignoring
	// case. It scans the jobs that match the other fields, so narrow it
	// with them on big tables.
	Search string
	// Before lists only jobs with an id below it: keyset pagination,
	// passing the last id of the previous page.
	Before int64
	Limit  int // default 100, newest first
}

// QueueStats counts a queue's jobs by state.
type QueueStats struct {
	Queue  string        `json:"queue"`
	States map[State]int `json:"states"`
	// Waiting is the earliest scheduled_at of the queue's available,
	// scheduled and retryable jobs (zero if none): when it is in the
	// past, the oldest job due to run has waited since then.
	Waiting time.Time `json:"waiting"`
}

// Store persists jobs. Every implementation must behave identically; the
// conformance suite in jobs/jobstest checks it. Times passed in are
// already rounded with Round. Methods must be safe for concurrent use.
type Store interface {
	// Clock is the store's clock, used for every time the runner writes.
	Clock() Clock
	// DB is the database whose transactions the store enqueues jobs in.
	DB() *data.DB
	// Insert adds a valid job in tx, visible to Fetch only once tx
	// commits. A unique job that already exists (see NewJob) is reported
	// as Result{ID: existing, Duplicate: true}; concurrent inserts of the
	// same unique job wait for each other's transactions.
	Insert(tx data.Tx, j NewJob) (Result, error)
	// Fetch moves up to Limit due jobs of a queue to executing, oldest
	// scheduled first, increments their attempt, and returns them. Due
	// means available, or scheduled or retryable with ScheduledAt <= Now.
	// Concurrent fetches never return the same job.
	Fetch(ctx context.Context, r FetchRequest) ([]*Record, error)
	// Finish ends an attempt. It returns ErrLeaseLost if the job is not
	// executing on r.Node at r.Attempt.
	Finish(ctx context.Context, id int64, f Finish) error
	// ExtendLeases renews the leases of jobs executing on node.
	ExtendLeases(ctx context.Context, node string, ids []int64, until time.Time) error
	// Rescue makes executing jobs whose lease ended before now available
	// again, or discarded if they have no attempts left. It returns how
	// many it moved.
	Rescue(ctx context.Context, now time.Time) (int, error)
	// Prune deletes completed, discarded and cancelled jobs finished
	// before before, and returns how many.
	Prune(ctx context.Context, before time.Time) (int, error)
	// Get returns a job, or ErrNotFound.
	Get(ctx context.Context, id int64) (*Record, error)
	// List returns jobs matching f, newest first.
	List(ctx context.Context, f Filter) ([]*Record, error)
	// Retry makes a job that isn't executing or available run now, giving
	// it one more attempt if it has none left. ErrNotFound, or ErrState
	// for an executing or available job.
	Retry(ctx context.Context, id int64, now time.Time) error
	// Cancel cancels a job that isn't finished. ErrNotFound, or ErrState
	// for a finished job.
	Cancel(ctx context.Context, id int64, now time.Time) error
	// Delete deletes a job that isn't executing. ErrNotFound, or ErrState
	// for an executing job (cancel it first).
	Delete(ctx context.Context, id int64) error
	// Stats counts jobs by queue and state, sorted by queue.
	Stats(ctx context.Context) ([]QueueStats, error)
	// TryLead makes node the cluster's leader, or keeps it leader, and
	// reports whether it is. Only one node leads at a time. until is how
	// long the claim lasts without renewal, for stores that use leases.
	TryLead(ctx context.Context, node string, now, until time.Time) (bool, error)
	// Resign gives up leadership if node has it.
	Resign(ctx context.Context, node string) error
	// Heartbeat records n in the node registry (loom_nodes), replacing
	// the node's previous entry.
	Heartbeat(ctx context.Context, n Node) error
	// Nodes returns the node registry, sorted by name.
	Nodes(ctx context.Context) ([]Node, error)
	// RemoveNode deletes a node's entry, if any: the node shut down.
	RemoveNode(ctx context.Context, name string) error
	// PruneNodes deletes the entries whose lease ended before before, and
	// returns how many.
	PruneNodes(ctx context.Context, before time.Time) (int, error)
	// Subscribe calls fn with a queue's name when jobs may have become
	// available in it (a committed insert, a retry), so workers needn't
	// wait for their next poll. fn must not block.
	Subscribe(fn func(queue string)) (cancel func())
	// Close releases the store's resources.
	Close() error
}

// Clock tells the time. Tests use a FakeClock.
type Clock interface{ Now() time.Time }

// RealClock is the system clock.
type RealClock struct{}

// Now returns time.Now().
func (RealClock) Now() time.Time { return time.Now() }

// FakeClock is a clock that only moves when told to.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock returns a FakeClock set to t.
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{now: t} }

// Now returns the clock's time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set sets the clock to t.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
