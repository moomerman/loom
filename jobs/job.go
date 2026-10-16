// Package jobs runs durable background jobs: typed, retried with backoff,
// scheduled, deduplicated, run on cron, and visible to the loom CLI.
//
//	type SendWelcome struct {
//		UserID string `json:"user_id"`
//	}
//
//	func (SendWelcome) Options() jobs.Options {
//		return jobs.Options{Queue: "mail", MaxAttempts: 5, Timeout: 30 * time.Second}
//	}
//
//	func (j SendWelcome) Perform(ctx context.Context, c *jobs.Ctx) error {
//		return mail.Send(ctx, j.UserID, "Welcome!")
//	}
//
//	jobs.Enqueue(tx, SendWelcome{UserID: id})
//
// A job is enqueued in the same transaction as the data change that caused
// it, so it exists if and only if that transaction commits. Jobs run at
// least once: Perform must be safe to run twice. See docs/jobs.md.
package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/trace"
)

// Job is a job type: a struct whose exported fields are the job's
// arguments, encoded as JSON. Fields tagged `json:"-"` are not arguments;
// they keep the values of the prototype given to Register, so a job can
// carry dependencies such as a database handle.
type Job interface {
	// Perform runs the job. Return nil when done, an error to retry with
	// backoff, Cancel(err) to stop without retrying, or Snooze(d) to run
	// again after d without counting the attempt. ctx is cancelled at the
	// job's timeout and when the runner shuts down.
	Perform(ctx context.Context, c *Ctx) error
}

// Options configure a job type. A job type sets them by having an
// Options method; any zero field takes the default.
type Options struct {
	Queue       string        // default "default"
	MaxAttempts int           // default 20
	Timeout     time.Duration // default 1 minute
}

// Defaults for Options.
const (
	DefaultQueue       = "default"
	DefaultMaxAttempts = 20
	DefaultTimeout     = time.Minute
)

type optioner interface{ Options() Options }

// OptionsOf returns a job type's options with defaults filled in.
func OptionsOf(j Job) Options {
	var o Options
	if op, ok := j.(optioner); ok {
		o = op.Options()
	}
	if o.Queue == "" {
		o.Queue = DefaultQueue
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = DefaultMaxAttempts
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	return o
}

// TypeName is the name a job type is stored and shown under: its Go type
// with the package name, e.g. "accounts.SendWelcome".
func TypeName(j Job) string {
	t := reflect.TypeOf(j)
	if t == nil {
		return "<nil>"
	}
	return t.String()
}

// NewJob is a job ready to insert.
type NewJob struct {
	Type        string
	Queue       string
	Args        json.RawMessage
	MaxAttempts int
	InsertedAt  time.Time
	ScheduledAt time.Time // the job is available from then
	// UniqueKey is set for unique jobs. The insert is a duplicate if a job
	// with the same key was inserted at or after UniqueSince and is not
	// discarded or cancelled.
	UniqueKey   string
	UniqueSince time.Time
	// Trace is the trace id of the action that enqueued the job, from
	// the transaction's context; the job's events carry it.
	Trace string
}

// Result is the outcome of Enqueue.
type Result struct {
	ID        int64 `json:"id"`        // the new job, or the existing one for a duplicate
	Duplicate bool  `json:"duplicate"` // a unique job already existed; nothing was inserted
}

// Option changes how a job is enqueued.
type Option func(*enqueueOptions)

type enqueueOptions struct {
	in     time.Duration
	at     time.Time
	unique time.Duration
}

// In schedules the job to run after d.
func In(d time.Duration) Option { return func(o *enqueueOptions) { o.in = d } }

// At schedules the job to run at t.
func At(t time.Time) Option { return func(o *enqueueOptions) { o.at = t } }

// Unique makes the enqueue do nothing if a job of the same type with the
// same args was enqueued within period and wasn't discarded or cancelled.
func Unique(period time.Duration) Option { return func(o *enqueueOptions) { o.unique = period } }

// Enqueue adds a job in tx, the same transaction as the data change that
// causes it. The job runs once tx commits, and never if it rolls back. It
// goes to the jobs store bound to tx's DB (see Bind); the app binds its
// store to app.DB().
//
//	err := db.Tx(ctx, func(tx loom.Tx) error {
//		// ... insert the user ...
//		_, err := jobs.Enqueue(tx, SendWelcome{UserID: id})
//		return err
//	})
func Enqueue(tx data.Tx, job Job, opts ...Option) (Result, error) {
	if err := checkType(job); err != nil {
		return Result{}, err
	}
	s, err := StoreOf(tx.DB())
	if err != nil {
		return Result{}, err
	}
	if err := registered(tx.DB(), job); err != nil {
		return Result{}, err
	}
	nj, err := newJob(s.Clock().Now(), job, opts...)
	if err != nil {
		return Result{}, err
	}
	nj.Trace = trace.ID(tx.Context())
	res, err := s.Insert(tx, nj)
	if err == nil {
		if r, ok := tx.DB().Value(runnerKey{}).(*Runner); ok {
			r.stream().Emit(tx.Context(), trace.JobEnqueue, "id", res.ID, "type", nj.Type, "queue", nj.Queue, "duplicate", res.Duplicate)
		}
	}
	return res, err
}

type storeKey struct{}

// Bind makes s the store that jobs enqueued in db's transactions go to.
// Stores bind themselves to their DB when created.
func Bind(db *data.DB, s Store) { db.SetValue(storeKey{}, s) }

// StoreOf returns the store bound to db. Errors are JB514.
func StoreOf(db *data.DB) (Store, error) {
	s, ok := db.Value(storeKey{}).(Store)
	if !ok {
		return nil, &Error{
			Code: JB514,
			Msg:  "no jobs store is bound to this transaction's database",
			Fix:  "enqueue in a transaction of app.DB() (app.DB().Tx(ctx, ...)), or of the DB your store was created with",
		}
	}
	return s, nil
}

func newJob(now time.Time, job Job, opts ...Option) (NewJob, error) {
	var o enqueueOptions
	for _, opt := range opts {
		opt(&o)
	}
	if err := checkType(job); err != nil {
		return NewJob{}, err
	}
	args, err := encodeArgs(job)
	if err != nil {
		return NewJob{}, err
	}
	return buildJob(now, TypeName(job), OptionsOf(job), args, o), nil
}

func buildJob(now time.Time, typ string, jo Options, args json.RawMessage, o enqueueOptions) NewJob {
	now = Round(now)
	nj := NewJob{
		Type:        typ,
		Queue:       jo.Queue,
		Args:        args,
		MaxAttempts: jo.MaxAttempts,
		InsertedAt:  now,
		ScheduledAt: now,
	}
	switch {
	case !o.at.IsZero():
		nj.ScheduledAt = Round(o.at)
	case o.in > 0:
		nj.ScheduledAt = Round(now.Add(o.in))
	}
	if o.unique > 0 {
		nj.UniqueKey = uniqueKey(typ, args)
		nj.UniqueSince = now.Add(-o.unique)
	}
	return nj
}

func uniqueKey(parts ...any) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%s\x00", p)
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// encodeArgs encodes a job's arguments: its fields not tagged `json:"-"`.
func encodeArgs(job Job) (json.RawMessage, error) {
	b, err := json.Marshal(job)
	if err != nil {
		return nil, &Error{
			Code: JB503,
			Msg:  fmt.Sprintf("the args of job %s can't be encoded as JSON: %v", TypeName(job), err),
			Fix:  "use JSON-friendly field types (strings, numbers, bools, slices, maps, structs), and tag dependencies such as database handles `json:\"-\"`",
		}
	}
	return b, nil
}

// checkType reports whether job is a usable job type: a struct value.
func checkType(job Job) error {
	if job == nil {
		return &Error{Code: JB502, Msg: "job is nil", Fix: "pass a job value, e.g. SendWelcome{UserID: id}"}
	}
	if t := reflect.TypeOf(job); t.Kind() != reflect.Struct {
		name := t.Name()
		if t.Kind() == reflect.Pointer {
			name = t.Elem().Name()
		}
		return &Error{
			Code: JB502,
			Msg:  fmt.Sprintf("job type %s is a %s, not a struct", t, t.Kind()),
			Fix:  fmt.Sprintf("make the job a struct with a value-receiver Perform method, and pass it by value: %s{...}", name),
		}
	}
	t := reflect.TypeOf(job)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.IsExported() || f.Anonymous || f.Tag.Get("json") == "-" {
			continue
		}
		return &Error{
			Code: JB516,
			Msg:  fmt.Sprintf("job type %s has unexported field %s, which JSON leaves out of the args: Perform sees the value registered with app.Jobs, never the one enqueued", t, f.Name),
			Fix:  fmt.Sprintf("export it (%s%s) if it's an arg; tag it `json:\"-\"` if it's a dependency set on the registered value", strings.ToUpper(f.Name[:1]), f.Name[1:]),
		}
	}
	return nil
}

// Round truncates t to the microsecond, in UTC: the precision every store
// keeps, so times read back equal the times written.
func Round(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }
