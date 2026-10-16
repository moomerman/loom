package loomtest

import (
	"errors"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
)

// LimitEpoch is where LimitClock's fake clock starts.
var LimitEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// LimitClock gives the app's rate limits (loom.RateLimit) a fake clock,
// starting at LimitEpoch, and returns it. Nothing moves it but Advance,
// so a test can use up a limit and then step past its window:
//
//	clock := loomtest.LimitClock(t, app)
//	p := loomtest.Mount(t, app, "/sign-in")
//	for range 5 {
//		p.Event("sign_in", loom.Payload{"email": "ada@example.com", "password": "wrong"})
//	}
//	p.AssertLimited("sign_in", loom.Payload{"email": "ada@example.com", "password": "wrong"})
//	clock.Advance(time.Minute)
//	p.Event("sign_in", loom.Payload{"email": "ada@example.com", "password": "right"})
//
// The app's limits count by real time when the test ends.
func LimitClock(t testing.TB, app *loom.App) *jobs.FakeClock {
	clock := jobs.NewFakeClock(LimitEpoch)
	app.SetLimitClock(clock.Now)
	t.Cleanup(func() { app.SetLimitClock(nil) })
	return clock
}

// AssertLimited sends an event and fails the test unless a rate limit
// refuses it (RT436) and the page stays as it was. It returns how long
// until the next attempt would be allowed.
func (p *Page) AssertLimited(name string, payload loom.Payload) time.Duration {
	p.t.Helper()
	before := p.view.HTML()
	_, err := p.view.Event(name, payload)
	var e *loom.LimitError
	if !errors.As(err, &e) {
		p.t.Fatalf("event %q: got %v, want it refused by a rate limit with RT436", name, err)
	}
	if after := p.view.HTML(); after != before {
		p.t.Fatalf("event %q was refused but the page changed:\n%s", name, after)
	}
	return e.RetryAfter
}
