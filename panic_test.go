package loom

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moomerman/loom/internal/hxtest"
)

// App code the session goroutine runs outside a handler (an auth check,
// a rate limit's key, Keep, Events) must not end the process when it
// panics: one database blip under c.UserID would take every page on the
// node with it. Each of these tests crashed the test binary before the
// panic was caught.

// hitPage counts its "hit" events in hits, which outlives the page, so a
// test can tell whether a handler ran even after a restart remounted it.
// spoil makes its Keep panic; eventsPanic makes Events panic, as the
// next copy of the page is bound.
type hitPage struct {
	hits        *atomic.Int32
	eventsPanic *atomic.Bool
	n           int
	kept        struct{ N int }
	spoilt      bool
}

func (p *hitPage) Mount(c *Ctx) {}

func (p *hitPage) Events() Events {
	if p.eventsPanic != nil && p.eventsPanic.Load() {
		panic("Events: the registry is gone")
	}
	return Events{
		"hit":   func(c *Ctx, _ Payload) { p.hits.Add(1); p.n++ },
		"boom":  func(c *Ctx, _ Payload) { panic("boom") },
		"save":  func(c *Ctx, _ Payload) { p.kept.N++ },
		"spoil": func(c *Ctx, _ Payload) { p.spoilt = true },
	}
}

func (p *hitPage) Assigns(c *Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "n:" + strings.Repeat("|", p.n+p.kept.N), Events: bound(p.Events())}
}

// keepingPage is a hitPage that keeps state (Keep), which panics once
// "spoil" ran.
type keepingPage struct{ hitPage }

func (p *keepingPage) Keep() any {
	if p.spoilt {
		panic("Keep: spoilt")
	}
	return &p.kept
}

// startPage loads path and returns its session id.
func startPage(t *testing.T, srv *httptest.Server, path string) string {
	t.Helper()
	res, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	m := sidRE.FindStringSubmatch(string(b))
	if res.StatusCode != 200 || m == nil {
		t.Fatalf("GET %s = %d:\n%s", path, res.StatusCode, b)
	}
	return m[1]
}

// sendEvent sends an event as `loom event` does, failing the test on an error
// sending it (not one the event caused).
func sendEvent(t *testing.T, app *App, sid, event string) *EventResult {
	t.Helper()
	r, err := app.SendEvent(sid, event, nil)
	if err != nil {
		t.Fatalf("SendEvent(%q): %v", event, err)
	}
	return r
}

// wantPanicReported checks errs is the one RT402 a panicking handler
// would be, located in this file.
func wantPanicReported(t *testing.T, errs []ErrorInfo, what string) {
	t.Helper()
	if len(errs) == 0 || errs[0].Code != RT402 || !strings.Contains(errs[0].Message, what+" panicked") || !strings.Contains(errs[0].Where, "panic_test.go") {
		t.Fatalf("errors = %+v, want RT402 for %s, located in panic_test.go", errs, what)
	}
}

func newPanicApp(t *testing.T) *App {
	t.Helper()
	app, _ := newTestApp(t)
	return app
}

func serve(t *testing.T, app *App) *httptest.Server {
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

// An auth check that panics during an event fails closed: the handler
// doesn't run, the panic is RT402 as a handler's is, and the page
// restarts, usable again.
func TestPanickingAuthCheckOnEvent(t *testing.T) {
	app := newPanicApp(t)
	var hits atomic.Int32
	var fail atomic.Bool
	app.Group(Auth(func(c *Ctx) error {
		if fail.CompareAndSwap(true, false) { // once
			panic("auth: the session store is down")
		}
		return nil
	})).Live("/p", &hitPage{hits: &hits}, hxtest.Counter)
	srv := serve(t, app)
	sid := startPage(t, srv, "/p")
	ch := stream(t, srv, sid, "1")

	fail.Store(true)
	r := sendEvent(t, app, sid, "hit")
	if hits.Load() != 0 {
		t.Fatal("the handler ran though its auth check panicked")
	}
	wantPanicReported(t, r.Errors, "an auth check")
	if r.Ended {
		t.Fatal("the session ended; want the page restarted")
	}
	if ev := next(t, ch); ev.event != "full" {
		t.Fatalf("after the panic, want a full render of a fresh page; got %+v", ev)
	}
	if r := sendEvent(t, app, sid, "hit"); hits.Load() != 1 || len(r.Errors) != 0 || !strings.HasPrefix(r.HTML, `<span class="badge">n:|</span>`) {
		t.Fatalf("after the restart: hits %d, %+v", hits.Load(), r)
	}
}

// An auth check that keeps panicking fails the restart's mount too: the
// session closes and the browser reloads, and the page load answers 500
// with the error page, as for a Mount that panics. A plain handler
// behind the same check answers 500 too, rather than dropping the
// connection.
func TestPanickingAuthCheckOnRestartAndLoad(t *testing.T) {
	app := newPanicApp(t)
	var hits atomic.Int32
	var fail atomic.Bool
	g := app.Group(Auth(func(c *Ctx) error {
		if fail.Load() {
			panic("auth: the session store is down")
		}
		return nil
	}))
	g.Live("/p", &hitPage{hits: &hits}, hxtest.Counter)
	g.Handle("GET /plain", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	srv := serve(t, app)
	sid := startPage(t, srv, "/p")
	ch := stream(t, srv, sid, "1")

	fail.Store(true)
	r := sendEvent(t, app, sid, "hit")
	if hits.Load() != 0 {
		t.Fatal("the handler ran though its auth check panicked")
	}
	wantPanicReported(t, r.Errors, "an auth check")
	if !r.Ended || app.session(sid) != nil {
		t.Fatalf("the restart's mount failed too, so the session should have ended: %+v", r)
	}
	if ev := next(t, ch); ev.event != "reload" {
		t.Fatalf("want the browser told to reload; got %+v", ev)
	}
	for _, path := range []string{"/p", "/plain"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err) // the connection dropped
		}
		res.Body.Close()
		if res.StatusCode != http.StatusInternalServerError {
			t.Errorf("GET %s = %d, want 500", path, res.StatusCode)
		}
	}
	if hits.Load() != 0 {
		t.Error("a handler ran though its auth check panicked")
	}
	fail.Store(false)
	startPage(t, srv, "/p") // once the store is back, the page loads
}

// A rate limit's key func that panics fails closed, like an auth check:
// the event is refused as a failure, not let through uncounted.
func TestPanickingLimitKeyOnEvent(t *testing.T) {
	app := newPanicApp(t)
	var hits atomic.Int32
	var fail atomic.Bool
	key := KeyBy("store", func(c *Ctx, event string, _ Payload) string {
		if event == "hit" && fail.CompareAndSwap(true, false) {
			panic("key: the session store is down")
		}
		return "k"
	})
	app.Live("/p", &hitPage{hits: &hits}, hxtest.Counter, RateLimit(Limit{Key: key, Max: 100, Per: time.Minute}))
	srv := serve(t, app)
	sid := startPage(t, srv, "/p")
	ch := stream(t, srv, sid, "1")

	fail.Store(true)
	r := sendEvent(t, app, sid, "hit")
	if hits.Load() != 0 {
		t.Fatal("the handler ran though its rate limit's key panicked")
	}
	wantPanicReported(t, r.Errors, "a rate limit's key func")
	if ev := next(t, ch); ev.event != "full" {
		t.Fatalf("after the panic, want a full render of a fresh page; got %+v", ev)
	}
	if r := sendEvent(t, app, sid, "hit"); hits.Load() != 1 || len(r.Errors) != 0 || r.Ended {
		t.Fatalf("after the restart: hits %d, %+v", hits.Load(), r)
	}
}

// A Keep that panics after a handler fails the event as the handler
// panicking would: the page restarts, bringing back what it kept as of
// its last good render.
func TestPanickingKeep(t *testing.T) {
	app := newPanicApp(t)
	var hits atomic.Int32
	app.Live("/p", &keepingPage{hitPage{hits: &hits}}, hxtest.Counter)
	srv := serve(t, app)
	sid := startPage(t, srv, "/p")
	ch := stream(t, srv, sid, "1")
	sendEvent(t, app, sid, "save")
	sendEvent(t, app, sid, "save")
	next(t, ch)
	next(t, ch)

	r := sendEvent(t, app, sid, "spoil")
	wantPanicReported(t, r.Errors, "*loom.keepingPage.Keep")
	if r.Ended {
		t.Fatal("the session ended; want the page restarted")
	}
	if ev := next(t, ch); ev.event != "full" || !strings.Contains(ev.data, `n:||`) {
		t.Fatalf("want a full render of the page remounted with what it kept; got %+v", ev)
	}
	if r := sendEvent(t, app, sid, "save"); len(r.Errors) != 0 || !strings.HasPrefix(r.HTML, `<span class="badge">n:|||</span>`) {
		t.Fatalf("after the restart: %+v", r)
	}
}

// Events is bound for each copy of the page as it mounts, outside the
// handlers; a panic there during a restart's mount ends the session
// cleanly instead of the process.
func TestPanicMountingOnRestart(t *testing.T) {
	app := newPanicApp(t)
	var hits atomic.Int32
	var eventsPanic atomic.Bool
	app.Live("/p", &hitPage{hits: &hits, eventsPanic: &eventsPanic}, hxtest.Counter)
	srv := serve(t, app)
	sid := startPage(t, srv, "/p")
	ch := stream(t, srv, sid, "1")

	eventsPanic.Store(true)
	r := sendEvent(t, app, sid, "boom") // restarts, and the new copy's Events panics
	if len(r.Errors) != 2 || r.Errors[0].Code != RT402 || r.Errors[1].Code != RT402 || !strings.Contains(r.Errors[1].Message, "mounting /p panicked: Events") {
		t.Fatalf("errors = %+v, want the handler's RT402 then the restart's", r.Errors)
	}
	if !r.Ended {
		t.Fatal("the restart failed, so the session should have ended")
	}
	if ev := next(t, ch); ev.event != "reload" {
		t.Fatalf("want the browser told to reload; got %+v", ev)
	}
	eventsPanic.Store(false)
	startPage(t, srv, "/p")
}

// The session goroutine's last resort: a panic nothing nearer caught
// (here, in a call run on the goroutine for the dev tools) is reported,
// and the session closes so the browser reloads, rather than the
// process ending. A panic in the view outside anything that recovers
// restarts the page, as a handler's does.
func TestSessionLastResort(t *testing.T) {
	app, srv := newTestApp(t)
	sid, _ := load(t, srv)
	ch := stream(t, srv, sid, "1")
	s := app.session(sid)

	// The view's page gone: Event panics looking up the handler, outside
	// any recover of the view's.
	s.call(func() { s.view.page = nil })
	r := sendEvent(t, app, sid, "inc")
	if len(r.Errors) == 0 || r.Errors[0].Code != RT402 || !strings.Contains(r.Errors[0].Message, `the handler of event "inc" panicked`) || r.Ended {
		t.Fatalf("want RT402 and the page restarted; got %+v", r)
	}
	if ev := next(t, ch); ev.event != "full" {
		t.Fatalf("want a full render of a fresh page; got %+v", ev)
	}

	n := app.errors.count()
	done := make(chan bool)
	go func() { done <- s.call(func() { panic("a dev tool's call") }) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the call never returned")
	}
	if ev := next(t, ch); ev.event != "reload" {
		t.Fatalf("want the browser told to reload; got %+v", ev)
	}
	if app.session(sid) != nil {
		t.Error("session still registered")
	}
	var errs []ErrorInfo
	for deadline := time.Now().Add(2 * time.Second); len(errs) == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		errs = app.errors.since(n, sid)
	}
	wantPanicReported(t, errs, "the page's session")
	if b, _ := json.Marshal(errs[0]); !strings.Contains(string(b), "a dev tool's call") {
		t.Errorf("error = %s", b)
	}
	load(t, srv) // the reload mounts afresh
}

// A mount that fails cancels the page's context, so a goroutine Mount
// started on c.Context() stops.
func TestFailedMountCancelsContext(t *testing.T) {
	app, _ := newTestApp(t)
	started := make(chan *Ctx, 1)
	app.Live("/leak", &leakPage{started: started}, hxtest.Counter)
	if _, err := app.NewView(httptest.NewRequest("GET", "/leak", nil), "sid", true); err == nil {
		t.Fatal("want the mount to fail")
	}
	c := <-started
	select {
	case <-c.Context().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the failed page's context was never cancelled")
	}
}

// leakPage starts work on c.Context() in Mount, then fails the mount
// with c.NotFound.
type leakPage struct{ started chan *Ctx }

func (p *leakPage) Mount(c *Ctx) {
	p.started <- c
	c.NotFound()
}

func (p *leakPage) Assigns(c *Ctx) hxtest.CounterAssigns { return hxtest.CounterAssigns{} }
