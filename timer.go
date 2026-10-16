package loom

import (
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"
)

// A page's timers run its own event handlers later, so debouncing,
// polling and autosave live in the page.
// They are kept on the View, which knows only what is due and when; the
// session arms one real timer for the earliest (see session.armTimer),
// and loomtest moves a fake clock instead (Page.Advance).

// SendAfter runs the page's handler for event after d, as if the browser
// had sent event with payload: the handler runs on the page's goroutine,
// then Assigns, and the page re-renders. A page has at most one pending
// timer per event, so calling SendAfter again for an event replaces its
// timer, which is a debounce:
//
//	"typed": func(c *loom.Ctx, payload loom.Payload) {
//		p.draft = payload["value"]
//		c.SendAfter(2*time.Second, "save", nil) // saves once typing stops for 2s
//	},
//
// A handler that calls SendAfter for its own event polls. The event
// must be one of the page's Events; another name is RT425, reported like
// c.Fail, and schedules nothing. Timers are part of the page's memory:
// they end with the session, and a restart or remount starts with none,
// so a Mount that needs one schedules it.
func (c *Ctx) SendAfter(d time.Duration, event string, payload Payload) {
	if c.hasEvent != nil && !c.hasEvent(event) {
		c.failed = append(c.failed, failError(c.unknownTimer(event), callerOutsideLoom()))
		return
	}
	c.timerOps = append(c.timerOps, timerOp{event: event, after: max(d, 0), payload: maps.Clone(payload)})
}

// CancelTimer cancels the page's pending timer for event, if any.
func (c *Ctx) CancelTimer(event string) {
	c.timerOps = append(c.timerOps, timerOp{event: event, cancel: true})
}

func (c *Ctx) unknownTimer(event string) *Error {
	names := c.eventNames()
	fix := fmt.Sprintf(`send one of the page's events, or add "%s" to its Events with func (p *Page) %s(c *loom.Ctx, payload loom.Payload)`, event, methodName(event))
	if n := closestName(event, names); n != "" {
		fix = fmt.Sprintf("if you mean %q, fix the name; otherwise %s", n, fix)
	}
	has := "it handles none"
	if len(names) > 0 {
		has = "it handles " + strings.Join(names, ", ")
	}
	return &Error{Code: RT425,
		Msg: fmt.Sprintf("c.SendAfter names event %q, which the page has no handler for (%s), so the timer was not set", event, has),
		Fix: fix}
}

// timerOp is a SendAfter or CancelTimer, kept until the View settles it.
type timerOp struct {
	event   string
	after   time.Duration
	payload Payload
	cancel  bool
}

// pendingTimer is a timer the View will run when its time comes.
type pendingTimer struct {
	due     time.Time
	payload Payload
	seq     int // order of scheduling, to break ties
}

// Timer is a page's pending timer, as Timers lists it.
type Timer struct {
	Event   string
	Due     time.Time
	Payload Payload
}

// SetClock sets the clock the View's timers use, instead of time.Now.
// loomtest sets a fake clock before anything else, so Page.Advance can
// move it.
func (v *View) SetClock(now func() time.Time) { v.now = now }

func (v *View) clock() time.Time {
	if v.now == nil {
		return time.Now()
	}
	return v.now()
}

// settle turns the SendAfter and CancelTimer calls made since the last
// call into pending timers, timed from now.
func (v *View) settle() {
	ops := v.ctx.timerOps
	v.ctx.timerOps = nil
	if len(ops) == 0 {
		return
	}
	now := v.clock()
	for _, op := range ops {
		if op.cancel {
			delete(v.timers, op.event)
			continue
		}
		if v.timers == nil {
			v.timers = map[string]*pendingTimer{}
		}
		v.timerSeq++
		v.timers[op.event] = &pendingTimer{due: now.Add(op.after), payload: op.payload, seq: v.timerSeq}
	}
}

// Timers returns the page's pending timers, soonest first.
func (v *View) Timers() []Timer {
	v.settle()
	out := []Timer{}
	for _, name := range v.timerOrder() {
		t := v.timers[name]
		out = append(out, Timer{Event: name, Due: t.due, Payload: maps.Clone(t.payload)})
	}
	return out
}

// NextTimer returns when the page's soonest timer is due, and false if
// it has none.
func (v *View) NextTimer() (time.Time, bool) {
	v.settle()
	order := v.timerOrder()
	if len(order) == 0 {
		return time.Time{}, false
	}
	return v.timers[order[0]].due, true
}

// timerOrder returns the events of the pending timers, soonest first.
func (v *View) timerOrder() []string {
	names := make([]string, 0, len(v.timers))
	for name := range v.timers {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := v.timers[names[i]], v.timers[names[j]]
		if !a.due.Equal(b.due) {
			return a.due.Before(b.due)
		}
		return a.seq < b.seq
	})
	return names
}

// RunTimers runs the handlers of the timers that are due, soonest first,
// then Assigns, and re-renders once. It returns the diff to send to the
// client, or nil if nothing changed or nothing was due. Like Event, it
// runs the route's auth checks first. Timers the handlers set run
// later, even if they are already due.
func (v *View) RunTimers() (map[string]any, error) {
	if v.lent {
		return nil, nil
	}
	v.begin("")
	v.settle()
	now := v.clock()
	var due []string
	for _, name := range v.timerOrder() {
		if v.timers[name].due.After(now) {
			break
		}
		due = append(due, name)
	}
	if len(due) == 0 {
		return nil, nil
	}
	if err := authorize(v.auth, v.ctx); err != nil {
		return nil, err
	}
	var assigns, lassign Assigns
	var err error
	what := "a timer"
	ran := false
	for _, name := range due {
		t := v.timers[name]
		if t == nil || t.due.After(now) {
			continue // an earlier handler cancelled or replaced it
		}
		delete(v.timers, name)
		h, owner, herr := v.handler(name)
		if herr != nil {
			return nil, herr // SendAfter checks the name, so this would be a Loom bug
		}
		what = fmt.Sprintf("Events()[%q] (from c.SendAfter)", name)
		payload := t.payload
		if payload == nil {
			payload = Payload{}
		}
		if assigns, lassign, err = v.call(owner, what, func() {
			h(v.ctx, payload)
			v.applyPatches()
		}); err != nil {
			break
		}
		ran = true
		// Timers this handler set are timed from now and kept apart from
		// due, so a handler that polls with SendAfter(0) can't loop.
		v.settle()
	}
	if !ran && err == nil {
		return nil, nil
	}
	return v.update(assigns, lassign, what, err)
}
