package loom

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom/internal/hxtest"
)

// tickPage counts ticks of a 20ms poll started by Mount, until "stop";
// "later" sets a timer an hour away, and "boom" panics.
type tickPage struct{ n int }

func (p *tickPage) Mount(c *Ctx) { c.SendAfter(20*time.Millisecond, "tick", nil) }

func (p *tickPage) Events() Events {
	return Events{
		"tick": func(c *Ctx, _ Payload) {
			p.n++
			c.SendAfter(20*time.Millisecond, "tick", nil)
		},
		"stop":  func(c *Ctx, _ Payload) { c.CancelTimer("tick") },
		"later": func(c *Ctx, _ Payload) { c.SendAfter(time.Hour, "later", Payload{"why": "test"}) },
		"boom":  func(c *Ctx, _ Payload) { panic("boom") },
	}
}

func (p *tickPage) Assigns(c *Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: strings.Repeat("|", p.n), Events: bound(p.Events())}
}

func newTickApp(t *testing.T) (*App, *httptest.Server) {
	app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Live("/", &tickPage{}, hxtest.Counter)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return app, srv
}

func TestTimersRunOnTheSession(t *testing.T) {
	_, srv := newTickApp(t)
	sid, _ := load(t, srv)
	ch := stream(t, srv, sid, "1")
	for _, want := range []string{`"0":"|"`, `"0":"||"`} {
		if ev := next(t, ch); ev.event != "diff" || !strings.Contains(ev.data, want) {
			t.Fatalf("got %+v, want a diff with %s", ev, want)
		}
	}
	post(t, srv, `{"sid":"`+sid+`","event":"stop"}`)
	// At most one tick was already queued when stop ran; then nothing.
	time.Sleep(30 * time.Millisecond)
	for len(ch) > 0 {
		<-ch
	}
	select {
	case ev := <-ch:
		t.Fatalf("a cancelled timer ran: %+v", ev)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestRestartResetsTimers(t *testing.T) {
	app, srv := newTickApp(t)
	sid, _ := load(t, srv)
	post(t, srv, `{"sid":"`+sid+`","event":"stop"}`)
	post(t, srv, `{"sid":"`+sid+`","event":"later"}`)
	d, err := app.Inspect(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Timers) != 1 || d.Timers[0].Event != "later" || d.Timers[0].Payload["why"] != "test" || d.Timers[0].InMS < 59*60*1000 {
		t.Fatalf("timers = %+v", d.Timers)
	}
	ch := stream(t, srv, sid, "")
	next(t, ch)                                      // the full tree, as the browser is behind
	post(t, srv, `{"sid":"`+sid+`","event":"boom"}`) // restarts with a fresh Mount, which polls again
	if ev := next(t, ch); ev.event != "full" {
		t.Fatalf("got %+v", ev)
	}
	if ev := next(t, ch); ev.event != "diff" || !strings.Contains(ev.data, `"0":"|"`) {
		t.Fatalf("got %+v, want the restarted page's first tick", ev)
	}
	if d, _ := app.Inspect(sid); len(d.Timers) != 1 || d.Timers[0].Event != "tick" {
		t.Fatalf("after the restart, timers = %+v", d.Timers)
	}
}
