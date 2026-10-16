package loomtest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom/diff"
	"github.com/moomerman/loom/trace"
)

// Trace returns the trace id of the page's latest action: its mount,
// or the last event, message, timer or re-render (docs/events.md).
func (p *Page) Trace() string { return p.view.Trace() }

// Events returns the events of the page's latest action, oldest first:
// the handler's page.event, its commits, the jobs it enqueued, and what
// those jobs did once run (q.Drain), which carry the same trace.
//
//	p.Click("button.export")
//	q.Drain(t)
//	p.AssertEvent("job.finish", "type", "app.Export", "state", "completed")
func (p *Page) Events() []trace.Event {
	return p.app.Stream().Since(0, trace.Filter{Trace: p.view.Trace()})
}

// AssertEvent fails the test unless the page's latest action emitted an
// event named name whose attributes include attrs, given as alternating
// keys and values compared as text. It returns the first such event.
//
//	p.Submit("form", loom.Payload{"title": "Milk"})
//	p.AssertEvent("tx.commit", "statements", 2)
//	p.AssertEvent("job.enqueue", "type", "todo.Notify")
func (p *Page) AssertEvent(name string, attrs ...any) trace.Event {
	p.t.Helper()
	return assertEvent(p.t, p.Events(), "the page's latest action (trace "+p.view.Trace()+")", name, attrs)
}

// RefuteEvent fails the test if the page's latest action emitted an
// event named name.
func (p *Page) RefuteEvent(name string) {
	p.t.Helper()
	for _, e := range p.Events() {
		if e.Name == name {
			p.t.Fatalf("the page's latest action emitted %s, want none:\n%s", name, eventLines([]trace.Event{e}))
		}
	}
}

// Events returns every event the queue's runner emitted, oldest first:
// job.start and job.finish for each attempt, cron.tick, and an error for
// each failure. Attached to an app (Attach), they are the app's events.
func (q *JobQueue) Events() []trace.Event {
	return q.runner.Stream().Since(0, trace.Filter{Name: "job."})
}

// AssertEvent fails the test unless the queue's runner emitted a job
// event named name whose attributes include attrs, compared as text.
//
//	q.Drain(t)
//	q.AssertEvent(t, "job.finish", "type", "todo.Notify", "state", "completed")
func (q *JobQueue) AssertEvent(t testing.TB, name string, attrs ...any) trace.Event {
	t.Helper()
	return assertEvent(t, q.Events(), "the job queue", name, attrs)
}

func assertEvent(t testing.TB, events []trace.Event, from, name string, attrs []any) trace.Event {
	t.Helper()
	if len(attrs)%2 != 0 {
		t.Fatalf("AssertEvent(%q, ...): attrs must be key, value pairs", name)
	}
	for _, e := range events {
		if e.Name == name && hasAttrs(e, attrs) {
			return e
		}
	}
	want := name
	for i := 0; i < len(attrs); i += 2 {
		want += fmt.Sprintf(" %v=%v", attrs[i], attrs[i+1])
	}
	t.Fatalf("%s emitted no event %s; it emitted:\n%s", from, want, eventLines(events))
	return trace.Event{}
}

// hasAttrs reports whether e has each key with the value, as text, so
// "statements", 2 matches the int64 2 and "ms" can't be matched exactly.
func hasAttrs(e trace.Event, attrs []any) bool {
	for i := 0; i < len(attrs); i += 2 {
		k, _ := attrs[i].(string)
		v, ok := e.Attrs[k]
		if !ok || fmt.Sprint(v) != fmt.Sprint(attrs[i+1]) {
			return false
		}
	}
	return true
}

func eventLines(events []trace.Event) string {
	if len(events) == 0 {
		return "  (nothing)"
	}
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "  %s %v\n", e.Name, e.Attrs)
	}
	return strings.TrimRight(b.String(), "\n")
}

// handled emits the page.* event the runtime emits for an action the
// test ran on the view directly, so tests see the same stream.
func (p *Page) handled(name string, start time.Time, d map[string]any, err error, attrs ...any) {
	bytes := 0
	if d != nil {
		if b, merr := diff.Marshal(d); merr == nil {
			bytes = len(b)
		}
	}
	outcome := "ok"
	if err != nil {
		outcome = "failed"
	}
	attrs = append([]any{"sid", "test", "route", p.view.Route()}, attrs...)
	attrs = append(attrs, "ms", time.Since(start), "diff", d != nil, "bytes", bytes, "outcome", outcome, "trace", p.view.Trace())
	p.app.Emit(context.Background(), name, attrs...)
}
