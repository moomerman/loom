package jobs

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Error is a jobs error. Like Loom's other errors it says what went
// wrong, where, and how to fix it, and has a stable code documented in
// docs/errors.md.
type Error struct {
	Code  string // e.g. "JB501"
	Msg   string
	Where string // file:line of the user code involved, if known
	Fix   string
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "error %s: %s", e.Code, e.Msg)
	if e.Where != "" {
		fmt.Fprintf(&b, "\n  at %s", e.Where)
	}
	if e.Fix != "" {
		fmt.Fprintf(&b, "\n  fix: %s", e.Fix)
	}
	return b.String()
}

// Jobs error codes.
const (
	JB501 = "JB501" // job type is not registered
	JB502 = "JB502" // invalid job type passed to Register
	JB503 = "JB503" // job args can't be encoded as JSON
	JB504 = "JB504" // invalid cron expression
	JB505 = "JB505" // Perform panicked
	JB506 = "JB506" // Perform ran past its timeout
	JB507 = "JB507" // no job with that id
	JB508 = "JB508" // the job's state doesn't allow the operation
	JB509 = "JB509" // args JSON doesn't fit the job type
	JB510 = "JB510" // a job's queue has no workers
	JB511 = "JB511" // the job's lease was lost before it finished
	JB512 = "JB512" // invalid queue or runner configuration
	JB513 = "JB513" // the job store failed (dev tools)
	JB514 = "JB514" // no jobs store bound to the transaction's database
	JB515 = "JB515" // no cron entry with that index
	JB516 = "JB516" // a job type has an unexported field, which isn't in its args
	JB517 = "JB517" // the job lost its lease MaxRescues times and was discarded
)

// ErrNotFound is returned by stores for an unknown job id.
var ErrNotFound = errors.New("job not found")

// ErrLeaseLost is returned by Store.Finish when the job is no longer held
// by the caller: its lease expired and it was rescued, or it was
// cancelled.
var ErrLeaseLost = errors.New("job lease lost")

// ErrState is returned by Store.Retry and Store.Cancel when the job's
// state doesn't allow the operation.
var ErrState = errors.New("job state doesn't allow this")

// cancelError stops a job without retrying it.
type cancelError struct{ err error }

func (e *cancelError) Error() string { return "cancelled: " + e.err.Error() }
func (e *cancelError) Unwrap() error { return e.err }

// Cancel returns an error that makes Perform's job cancelled: it stops
// and is not retried.
//
//	if user == nil {
//		return jobs.Cancel(errors.New("user was deleted"))
//	}
func Cancel(err error) error {
	if err == nil {
		err = errors.New("cancelled")
	}
	return &cancelError{err}
}

// snoozeError reschedules a job without counting the attempt.
type snoozeError struct{ d time.Duration }

func (e *snoozeError) Error() string { return "snoozed for " + e.d.String() }

// Snooze returns an error that makes Perform's job run again after d,
// without counting this attempt towards MaxAttempts.
//
//	if !apiAvailable() {
//		return jobs.Snooze(time.Minute)
//	}
func Snooze(d time.Duration) error { return &snoozeError{d} }
