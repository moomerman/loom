package loomtest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/loomtest"
)

// timerPage autosaves a draft 300ms after typing stops and polls every
// 5s, three times.
type timerPage struct {
	draft, saved string
	polls        int
}

func (p *timerPage) Mount(c *loom.Ctx) { c.SendAfter(5*time.Second, "poll", nil) }

func (p *timerPage) Assigns(c *loom.Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "saved=" + p.saved + " polls=" + strings.Repeat("|", p.polls), Events: bound(p.Events())}
}

func (p *timerPage) Events() loom.Events {
	return loom.Events{
		"typed": func(c *loom.Ctx, payload loom.Payload) {
			p.draft = payload["value"]
			c.SendAfter(300*time.Millisecond, "save", loom.Payload{"draft": p.draft})
		},
		"save": func(c *loom.Ctx, payload loom.Payload) { p.saved = payload["draft"] },
		"poll": func(c *loom.Ctx, _ loom.Payload) {
			if p.polls++; p.polls < 3 {
				c.SendAfter(5*time.Second, "poll", nil)
			}
		},
		"stop": func(c *loom.Ctx, _ loom.Payload) { c.CancelTimer("poll") },
		"typo": func(c *loom.Ctx, _ loom.Payload) { c.SendAfter(time.Second, "svae", nil) },
	}
}

func newTimerApp() *loom.App {
	app := loom.New()
	app.Live("/", &timerPage{}, hxtest.Counter)
	return app
}

func TestSendAfterDebounces(t *testing.T) {
	p := loomtest.Mount(t, newTimerApp(), "/")
	p.Event("typed", loom.Payload{"value": "M"})
	p.Advance(200 * time.Millisecond)
	p.Event("typed", loom.Payload{"value": "Milk"}) // replaces the pending save
	if got := p.Timers()["save"]; got != 300*time.Millisecond {
		t.Errorf("save is due in %s, want 300ms", got)
	}
	p.Advance(200 * time.Millisecond)
	p.AssertText(".badge", "saved= ")
	p.Advance(100 * time.Millisecond)
	p.AssertText(".badge", "saved=Milk ")
	p.AssertDiff(`{"d":{"0":"saved=Milk polls="}}`)
	if _, ok := p.Timers()["save"]; ok {
		t.Error("save is still pending after it ran")
	}
}

func TestSendAfterPollsAndCancels(t *testing.T) {
	p := loomtest.Mount(t, newTimerApp(), "/")
	p.Advance(11 * time.Second) // runs at 5s and 10s
	p.AssertText(".badge", "polls=||")
	p.Advance(time.Hour) // the third poll stops polling
	p.AssertText(".badge", "polls=|||")
	if len(p.Timers()) != 0 {
		t.Errorf("timers = %v, want none", p.Timers())
	}

	p = loomtest.Mount(t, newTimerApp(), "/")
	p.Event("stop", nil)
	p.Advance(time.Hour)
	p.AssertText(".badge", "polls=")
	p.AssertDiff("")
}

func TestSendAfterUnknownEventFails(t *testing.T) {
	ft := &fakeT{}
	p := loomtest.Mount(ft, newTimerApp(), "/")
	p.Event("typo", nil)
	if len(ft.errors) != 1 || !strings.Contains(ft.errors[0], `error RT425: c.SendAfter names event "svae"`) || !strings.Contains(ft.errors[0], `if you mean "save"`) || !strings.Contains(ft.errors[0], "timer_test.go") {
		t.Fatalf("errors = %q", ft.errors)
	}
	if len(p.Timers()) != 1 { // only Mount's poll
		t.Errorf("timers = %v", p.Timers())
	}
}
