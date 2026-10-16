package jobsweb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
)

// The types below are what the templates render. Templates can only use
// types declared in their own package, with string, int, float64 and
// bool fields, so each page turns jobs.Record and friends into these
// rows in Go, formatting times and numbers there. Keeping templates to
// plain data is what lets them compile to JavaScript later.

// JobRow is a job in a list.
type JobRow struct {
	ID          string
	Type        string
	Queue       string
	State       string
	Attempt     int
	MaxAttempts int
	Args        string // redacted, compact JSON
	When        string // when it ran or will run, e.g. "3m ago"
	Error       string // the latest error, if it failed
}

// Option is an <option> of a filter, or a tab.
type Option struct {
	Value    string
	Label    string
	Selected bool
	Href     string // a tab's link; "" for an <option>
}

// confirmation is an action waiting for the operator to confirm it.
// Destructive actions take two clicks: the first stores the action in
// the page's state and shows a confirm bar; "confirm" runs it. It needs
// no JavaScript, and the server, not the browser, decides what runs.
type confirmation struct {
	action string // the event to run, e.g. "delete"
	prompt string // e.g. "Delete job 12?"
}

// row converts a job record to a row.
func (a *App) row(rec *jobs.Record, now time.Time) JobRow {
	r := JobRow{
		ID: strconv.FormatInt(rec.ID, 10), Type: rec.Type, Queue: rec.Queue, State: string(rec.State),
		Attempt: rec.Attempt, MaxAttempts: rec.MaxAttempts, Args: compact(a.args(rec.Type, rec.Args)),
	}
	switch {
	case rec.FinishedAt != nil:
		r.When = ago(now, *rec.FinishedAt)
	case rec.State == jobs.Executing && rec.AttemptedAt != nil:
		r.When = "started " + ago(now, *rec.AttemptedAt)
	default:
		r.When = ago(now, rec.ScheduledAt)
	}
	if rec.State == jobs.Retryable || rec.State == jobs.Discarded {
		if n := len(rec.Errors); n > 0 {
			r.Error = rec.Errors[n-1].Error
		}
	}
	return r
}

// ago formats t relative to now: "12s ago", "in 5m".
func ago(now, t time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		return "in " + short(-d)
	}
	if d < time.Second {
		return "just now"
	}
	return short(d) + " ago"
}

func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// stamp formats a time in full, in UTC.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 UTC") }

// compact removes insignificant space from JSON for one-line display.
func compact(s string) string {
	var b bytes.Buffer
	if json.Compact(&b, []byte(s)) != nil {
		return s
	}
	return b.String()
}

// indent pretty-prints JSON.
func indent(s string) string {
	var b bytes.Buffer
	if json.Indent(&b, []byte(s), "", "  ") != nil {
		return s
	}
	return b.String()
}

// errorMessage turns an action's error into a message for the flash:
// jobs errors say what went wrong in plain English already.
func errorMessage(err error) string {
	if je, ok := err.(*jobs.Error); ok {
		return je.Msg
	}
	return err.Error()
}

// section tells the app layout which nav item to highlight.
func section(c *loom.Ctx, name string) { c.Set("section", name) }
