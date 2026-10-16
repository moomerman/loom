package loom

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom/internal/hxtest"
)

// chromeState is a live layout: a counter and a toast every page shows.
type chromeState struct {
	count  int
	toast  string
	mounts int // how many times this copy was mounted
	news   []string
}

func (l *chromeState) Mount(c *Ctx) {
	l.mounts++
	c.Subscribe("news")
}

func (l *chromeState) Assigns(c *Ctx) hxtest.ChromeAssigns {
	return hxtest.ChromeAssigns{Count: l.count, Toast: l.toast}
}

func (l *chromeState) Events() Events {
	return Events{
		"bump":    func(c *Ctx, _ Payload) { l.count++ },
		"dismiss": func(c *Ctx, _ Payload) { l.toast = "" },
	}
}

func (l *chromeState) HandleInfo(c *Ctx, msg Message) { l.news = append(l.news, msg.Event) }

// toastPage reads and changes its live layout's state.
type toastPage struct{ seen int }

func (p *toastPage) Mount(c *Ctx) { p.seen = LayoutOf[*chromeState](c).count }

func (p *toastPage) Assigns(c *Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "seen " + strconv.Itoa(p.seen), Events: bound(p.Events())}
}

func (p *toastPage) Events() Events {
	return Events{"save": func(c *Ctx, _ Payload) {
		LayoutOf[*chromeState](c).toast = "Saved"
	}}
}

func newLiveLayoutApp(t *testing.T) *App {
	t.Helper()
	app, _ := newTestApp(t)
	g := app.Group(LiveLayout(hxtest.Chrome, &chromeState{count: 7}))
	g.Live("/toast", &toastPage{}, hxtest.Counter)
	return app
}

func mountView(t *testing.T, app *App, path string, layouts bool) *View {
	t.Helper()
	v, err := app.NewView(httptest.NewRequest("GET", path, nil), "test", layouts)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLiveLayout(t *testing.T) {
	app := newLiveLayoutApp(t)
	v := mountView(t, app, "/toast", true)
	if got := v.HTML(); !strings.Contains(got, `<button class="bump" on:click="bump">7</button>`) || !strings.Contains(got, `<span class="badge">seen 7</span>`) {
		t.Fatalf("first render: %s", got)
	}

	// The layout's event goes to the layout; the page has no handler for it.
	d, err := v.Event("bump", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, d); got != `{"d":{"0":"8"}}` {
		t.Errorf("bump diff: %s", got)
	}

	// The page changes the layout's state through LayoutOf.
	if _, err := v.Event("save", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.HTML(), `<span class="toast" on:click="dismiss">Saved</span>`) {
		t.Errorf("no toast: %s", v.HTML())
	}
	if _, err := v.Event("dismiss", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v.HTML(), "Saved") {
		t.Errorf("toast not dismissed: %s", v.HTML())
	}
	if la, ok := v.LayoutAssigns().(hxtest.ChromeAssigns); !ok || la.Count != 8 {
		t.Errorf("LayoutAssigns: %#v", v.LayoutAssigns())
	}

	// Each page load gets its own copy, mounted once.
	other := mountView(t, app, "/toast", true)
	if !strings.Contains(other.HTML(), `<button class="bump" on:click="bump">7</button>`) {
		t.Errorf("another page load shares the layout's state: %s", other.HTML())
	}
	if l := v.ctx.layout.(*chromeState); l.mounts != 1 {
		t.Errorf("mounted %d times", l.mounts)
	}
}

func TestLiveLayoutInfo(t *testing.T) {
	app := newLiveLayoutApp(t)
	v := mountView(t, app, "/toast", true)
	if got := v.Topics(); len(got) != 1 || got[0] != "news" {
		t.Fatalf("topics %q", got)
	}
	// The layout subscribed, so the message is its: the page has no
	// HandleInfo and needs none.
	if _, err := v.Info(Message{Topic: "news", Event: "hello"}); err != nil {
		t.Fatal(err)
	}
	if l := v.ctx.layout.(*chromeState); len(l.news) != 1 || l.news[0] != "hello" {
		t.Errorf("news %q", l.news)
	}
}

func TestLiveLayoutWithoutRendering(t *testing.T) {
	// loomtest.NoLayout renders the page alone, but its layout's state is
	// still mounted, so LayoutOf and the layout's events work.
	app := newLiveLayoutApp(t)
	v := mountView(t, app, "/toast", false)
	if got := v.HTML(); got != `<span class="badge">seen 7</span><menu hidden><button on:click="save">event</button></menu>` {
		t.Errorf("got %s", got)
	}
	if _, err := v.Event("bump", nil); err != nil {
		t.Fatal(err)
	}
	if l := v.ctx.layout.(*chromeState); l.count != 8 {
		t.Errorf("count %d", l.count)
	}
}

func TestLiveLayoutOverHTTP(t *testing.T) {
	app, srv := newTestApp(t)
	app.Group(LiveLayout(hxtest.Chrome, &chromeState{})).Live("/toast", &toastPage{}, hxtest.Counter)
	sid, _ := loadPath(t, srv, "/toast")
	ch := stream(t, srv, sid, "1")
	post(t, srv, `{"sid":"`+sid+`","event":"bump"}`)
	if ev := next(t, ch); ev.event != "diff" || ev.data != `{"diff":{"d":{"0":"1"}},"v":2}` {
		t.Errorf("got %+v", ev)
	}
	app.PubSub().Publish(Message{Topic: "news", Event: "hi"})
	post(t, srv, `{"sid":"`+sid+`","event":"bump"}`) // the message and this event run in order
	if ev := next(t, ch); ev.data != `{"diff":{"d":{"0":"2"}},"v":3}` {
		t.Errorf("got %+v", ev)
	}
}

// clashPage handles the layout's "bump" itself.
type clashPage struct{ toastPage }

func (p *clashPage) Events() Events { return Events{"bump": func(*Ctx, Payload) {}} }

// pageForWrongLayout asks for a layout type it isn't in.
type pageForWrongLayout struct{ toastPage }

func (p *pageForWrongLayout) Mount(c *Ctx) { LayoutOf[*chromeState](c) }

// badChrome returns the wrong assigns.
type badChrome struct{ chromeState }

func (l *badChrome) Assigns(c *Ctx) hxtest.ShellAssigns { return hxtest.ShellAssigns{} }

// quietChrome handles none of its markup's events.
type quietChrome struct{}

func (l *quietChrome) Mount(c *Ctx) {}

func (l *quietChrome) Assigns(c *Ctx) hxtest.ChromeAssigns { return hxtest.ChromeAssigns{} }

func TestLiveLayoutErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    func()
		code string
		msg  string
	}{
		{"both handle an event", func() {
			New().Group(LiveLayout(hxtest.Chrome, &chromeState{})).Live("/", &clashPage{}, hxtest.Counter)
		}, RT434, `*loom.clashPage and its live layout *loom.chromeState both handle "bump"`},
		{"nobody handles an event", func() {
			New().Group(LiveLayout(hxtest.Chrome, &quietChrome{})).Live("/", &toastPage{}, hxtest.Counter)
		}, RT422, `which *loom.toastPage and its live layout *loom.quietChrome has no handler for (they handle save)`},
		{"wrong assigns", func() { LiveLayout(hxtest.Chrome, &badChrome{}) },
			RT412, "live layout *loom.badChrome has Assigns of type func(*loom.Ctx) hxtest.ShellAssigns, but its component hxtest.Chrome needs hxtest.ChromeAssigns"},
		{"not a pointer", func() { LiveLayout(hxtest.Chrome, chromeValue{}) }, RT406, "a live layout's state must be a pointer to a struct"},
		{"root layout", func() { LiveLayout(hxtest.Root, &chromeState{}) }, RT413, "hxtest.Root is a root layout"},
	} {
		func() {
			defer func() {
				e, ok := recover().(*Error)
				if !ok || e.Code != tc.code || !strings.Contains(e.Msg, tc.msg) || e.Fix == "" {
					t.Errorf("%s: got %#v", tc.name, e)
				}
			}()
			tc.f()
		}()
	}
}

// chromeValue is a live layout passed by value, which can't work.
type chromeValue struct{}

func (chromeValue) Mount(c *Ctx) {}

func TestLayoutOfWrongLayout(t *testing.T) {
	app, _ := newTestApp(t)
	app.Group(Layout(hxtest.Shell, shellAssigns)).Live("/wrong", &pageForWrongLayout{}, hxtest.Counter)
	_, err := app.NewView(httptest.NewRequest("GET", "/wrong", nil), "test", true)
	e, ok := err.(*Error)
	if !ok || e.Code != RT435 || !strings.Contains(e.Msg, "LayoutOf[*loom.chromeState] asked for the page's live layout, but the page's app layout isn't a live layout") || !strings.Contains(e.Where, "live_layout_test.go") {
		t.Errorf("got %#v", err)
	}
}

func TestLayoutEventFixSuggestsLiveLayout(t *testing.T) {
	defer func() {
		e, ok := recover().(*Error)
		if !ok || e.Code != RT422 || !strings.Contains(e.Fix, "make hxtest.Chrome a loom.LiveLayout") {
			t.Errorf("got %#v", e)
		}
	}()
	New().Group(Layout(hxtest.Chrome, func(*Ctx) hxtest.ChromeAssigns { return hxtest.ChromeAssigns{} })).Live("/", &toastPage{}, hxtest.Counter)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// timedToastPage shows a toast and has the layout dismiss it later.
type timedToastPage struct{ toastPage }

func (p *timedToastPage) Events() Events {
	return Events{"save": func(c *Ctx, _ Payload) {
		LayoutOf[*chromeState](c).toast = "Saved"
		c.SendAfter(time.Second, "dismiss", nil) // the layout's event
	}}
}

func TestLiveLayoutTimer(t *testing.T) {
	app, _ := newTestApp(t)
	app.Group(LiveLayout(hxtest.Chrome, &chromeState{})).Live("/timed", &timedToastPage{}, hxtest.Counter)
	v := mountView(t, app, "/timed", true)
	now := time.Now()
	v.SetClock(func() time.Time { return now })
	if _, err := v.Event("save", nil); err != nil {
		t.Fatal(err)
	}
	if fs := v.Failures(); len(fs) > 0 {
		t.Fatalf("SendAfter to the layout's event failed: %v", fs)
	}
	if ts := v.Timers(); len(ts) != 1 || ts[0].Event != "dismiss" {
		t.Fatalf("timers %v", ts)
	}
	now = now.Add(2 * time.Second)
	if _, err := v.RunTimers(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v.HTML(), "Saved") {
		t.Errorf("the timer didn't run the layout's dismiss: %s", v.HTML())
	}
}

// newCarryApp serves /toast and /other in one live layout, /missing
// (whose Mount calls NotFound) in it too, and /elsewhere in another
// live layout of the same type.
func newCarryApp(t *testing.T) (*App, *httptest.Server) {
	t.Helper()
	app, srv := newTestApp(t)
	g := app.Group(LiveLayout(hxtest.Chrome, &chromeState{count: 7}))
	g.Live("/toast", &toastPage{}, hxtest.Counter)
	g.Live("/other", &toastPage{}, hxtest.Counter)
	g.Live("/missing", &navPage{missing: true}, hxtest.Counter)
	app.Group(LiveLayout(hxtest.Chrome, &chromeState{count: 100})).Live("/elsewhere", &toastPage{}, hxtest.Counter)
	return app, srv
}

// A live navigation between pages of one live layout moves the layout's
// state, mounted once, with its subscriptions; the page it left does
// nothing more.
func TestLiveLayoutAcrossNavigation(t *testing.T) {
	app, _ := newCarryApp(t)
	v := mountView(t, app, "/toast", true)
	if _, err := v.Event("bump", nil); err != nil {
		t.Fatal(err)
	}
	next, err := app.NewViewFrom(v, httptest.NewRequest("GET", "/other", nil), "test", true)
	if err != nil {
		t.Fatal(err)
	}
	state := LayoutOf[*chromeState](next.ctx)
	if got := next.HTML(); state.count != 8 || state.mounts != 1 || !strings.Contains(got, `<span class="badge">seen 8</span>`) {
		t.Errorf("after navigating: count %d, mounted %d times:\n%s", state.count, state.mounts, got)
	}
	if got := strings.Join(next.Topics(), ","); got != "news" {
		t.Errorf("the next page's topics are %q, want the layout's news", got)
	}
	if d, err := v.Event("bump", nil); d != nil || err != nil || state.count != 8 {
		t.Errorf("the page left behind ran an event: %v, %v, count %d", d, err, state.count)
	}
	if _, err := next.Info(Message{Topic: "news", Event: "hello"}); err != nil || len(state.news) != 1 {
		t.Errorf("the layout's topic after navigating: %v, news %q", err, state.news)
	}

	// Another live layout starts afresh, and the page keeps its own.
	far, err := app.NewViewFrom(next, httptest.NewRequest("GET", "/elsewhere", nil), "test", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := LayoutOf[*chromeState](far.ctx).count; got != 100 {
		t.Errorf("another live layout has count %d, want its own 100", got)
	}
	if _, err := next.Event("bump", nil); err != nil || state.count != 9 {
		t.Errorf("the page kept its layout: %v, count %d", err, state.count)
	}

	// A page that doesn't mount gives the layout back.
	if _, err := app.NewViewFrom(next, httptest.NewRequest("GET", "/missing", nil), "test", true); err != ErrPageNotFound {
		t.Fatalf("navigating to /missing: %v", err)
	}
	if _, err := next.Event("bump", nil); err != nil || state.count != 10 {
		t.Errorf("after a failed navigation: %v, count %d", err, state.count)
	}
}

// Over HTTP, the state moves with the swap, and the session left ends.
func TestLiveLayoutAcrossNavigationHTTP(t *testing.T) {
	app, srv := newCarryApp(t)
	res, err := http.Get(srv.URL + "/toast")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	sid := sidRE.FindStringSubmatch(string(b))[1]
	if !app.session(sid).call(func() { LayoutOf[*chromeState](app.session(sid).view.ctx).count = 42 }) {
		t.Fatal("no session")
	}
	res, body := navigate(t, srv, "/other", sid)
	if res.Header.Get("X-Loom-Navigate") != "swap" || !strings.Contains(body, `<span class="badge">seen 42</span>`) {
		t.Fatalf("navigating to /other = %s:\n%s", res.Header.Get("X-Loom-Navigate"), body)
	}
	if app.session(sid) != nil {
		t.Error("the session left is still running")
	}
}
