package loom

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom/client"
	"github.com/moomerman/loom/form"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/render"
)

// counterPage renders hxtest.Counter with a counter, panics on "boom" and
// reports an error with c.Fail on "fail".
type counterPage struct {
	n      int
	prefix string // set on the prototype, copied to every page
}

func (p *counterPage) Mount(c *Ctx) {}

func (p *counterPage) Events() Events {
	return Events{
		"inc":  func(c *Ctx, _ Payload) { p.n++ },
		"boom": func(c *Ctx, _ Payload) { panic("boom") },
		"fail": func(c *Ctx, _ Payload) { c.Fail(errors.New("the store is down")) },
		"noop": func(c *Ctx, _ Payload) {},
	}
}

func (p *counterPage) Assigns(c *Ctx) hxtest.CounterAssigns { return p.assigns() }

func (p *counterPage) assigns() hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: p.prefix + strings.Repeat("|", p.n), Events: bound(p.Events())}
}

// bound returns a page's events for hxtest.Counter's buttons: the
// runtime refuses events no element on the page binds (RT438), and the
// tests send each of them.
func bound(events Events) []render.Event {
	var names []render.Event
	for name := range events {
		names = append(names, render.Event{Name: name})
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name < names[j].Name })
	return names
}

func newTestApp(t *testing.T) (*App, *httptest.Server) {
	app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Live("/", &counterPage{prefix: "n:"}, hxtest.Counter)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return app, srv
}

var sidRE = regexp.MustCompile(`data-sid="([0-9a-f]+)"`)

func load(t *testing.T, srv *httptest.Server) (sid, body string) {
	t.Helper()
	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	m := sidRE.FindStringSubmatch(string(b))
	if res.StatusCode != 200 || m == nil {
		t.Fatalf("GET / = %d:\n%s", res.StatusCode, b)
	}
	return m[1], string(b)
}

type sse struct {
	event, data string
}

// stream connects to the SSE stream and returns a channel of its events.
func stream(t *testing.T, srv *httptest.Server, sid, v string) <-chan sse {
	t.Helper()
	res, err := http.Get(srv.URL + "/_loom/stream?sid=" + sid + "&v=" + v)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	ch := make(chan sse, 16)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(res.Body)
		var ev sse
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			case line == "" && ev.event != "":
				ch <- ev
				ev = sse{}
			}
		}
	}()
	return ch
}

func next(t *testing.T, ch <-chan sse) sse {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("stream closed")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
	}
	return sse{}
}

func post(t *testing.T, srv *httptest.Server, body string) int {
	t.Helper()
	res, err := http.Post(srv.URL+"/_loom/event", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestPageRendersHTMLAndTree(t *testing.T) {
	_, srv := newTestApp(t)
	_, body := load(t, srv)
	for _, want := range []string{`<span class="badge">n:</span>`, `id="loom-tree"`, `"v":1`, `"p":1`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q:\n%s", want, body)
		}
	}
	// loom.js is at a URL that changes with its content, cached for good.
	src := regexp.MustCompile(`<script src="(/_loom/js/\w+\.js)" defer>`).FindStringSubmatch(body)
	if src == nil {
		t.Fatalf("no loom.js script:\n%s", body)
	}
	res, err := http.Get(srv.URL + src[1])
	if err != nil {
		t.Fatal(err)
	}
	js, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "javascript") || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") || string(js) != string(client.JS) {
		t.Errorf("loom.js: %d %s %s", res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Cache-Control"))
	}
	res, err = http.Get(srv.URL + "/other")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("GET /other = %d, want 404", res.StatusCode)
	}
}

func TestEventSendsDiff(t *testing.T) {
	_, srv := newTestApp(t)
	sid, _ := load(t, srv)
	ch := stream(t, srv, sid, "1")
	if code := post(t, srv, `{"sid":"`+sid+`","event":"inc","payload":{}}`); code != 200 {
		t.Fatalf("POST = %d", code)
	}
	if ev := next(t, ch); ev.event != "diff" || ev.data != `{"diff":{"d":{"0":"n:|"}},"v":2}` {
		t.Fatalf("got %+v", ev)
	}
	post(t, srv, `{"sid":"`+sid+`","event":"noop"}`) // no change, no message
	post(t, srv, `{"sid":"`+sid+`","event":"inc"}`)
	if ev := next(t, ch); ev.data != `{"diff":{"d":{"0":"n:||"}},"v":3}` {
		t.Fatalf("got %+v", ev)
	}
}

func TestReconnectResyncs(t *testing.T) {
	_, srv := newTestApp(t)
	sid, _ := load(t, srv)
	post(t, srv, `{"sid":"`+sid+`","event":"inc"}`)
	time.Sleep(50 * time.Millisecond)
	ch := stream(t, srv, sid, "1") // the browser missed version 2
	ev := next(t, ch)
	if ev.event != "full" || !strings.Contains(ev.data, `"v":2`) || !strings.Contains(ev.data, `n:|`) {
		t.Fatalf("got %+v", ev)
	}
}

func TestPanicRestartsPage(t *testing.T) {
	_, srv := newTestApp(t)
	sid, _ := load(t, srv)
	ch := stream(t, srv, sid, "1")
	post(t, srv, `{"sid":"`+sid+`","event":"inc"}`)
	next(t, ch)
	post(t, srv, `{"sid":"`+sid+`","event":"boom"}`)
	if ev := next(t, ch); ev.event != "full" || !strings.Contains(ev.data, `"d":["n:",`) {
		t.Fatalf("after a panic, want a full render of a fresh page; got %+v", ev)
	}
	post(t, srv, `{"sid":"`+sid+`","event":"inc"}`) // the restarted page works
	if ev := next(t, ch); ev.event != "diff" || !strings.Contains(ev.data, "n:|") {
		t.Fatalf("got %+v", ev)
	}
}

func TestRepeatedPanicsStopSession(t *testing.T) {
	app, srv := newTestApp(t)
	sid, _ := load(t, srv)
	ch := stream(t, srv, sid, "1")
	for i := 0; i <= maxRestarts; i++ {
		post(t, srv, `{"sid":"`+sid+`","event":"boom"}`)
	}
	for {
		if ev := next(t, ch); ev.event == "reload" {
			break
		}
	}
	if app.session(sid) != nil {
		t.Error("session still registered")
	}
}

// An event the page has no handler for changes nothing, and doesn't
// restart the page: no handler ran.
func TestUnknownEventIsIgnored(t *testing.T) {
	_, srv := newTestApp(t)
	sid, _ := load(t, srv)
	ch := stream(t, srv, sid, "1")
	post(t, srv, `{"sid":"`+sid+`","event":"delte"}`)
	post(t, srv, `{"sid":"`+sid+`","event":"inc"}`)
	if ev := next(t, ch); ev.event != "diff" {
		t.Fatalf("got %+v, want the diff of inc, not a restart", ev)
	}
}

type badgePage struct{}

func (*badgePage) Mount(*Ctx)                       {}
func (*badgePage) Assigns(*Ctx) hxtest.BadgeAssigns { return hxtest.BadgeAssigns{} }

type wrongAssignsPage struct{}

func (*wrongAssignsPage) Mount(*Ctx)                          {}
func (*wrongAssignsPage) Assigns(*Ctx) hxtest.TodoListAssigns { return hxtest.TodoListAssigns{} }

type todoPage struct{}

func (*todoPage) Mount(*Ctx)                          {}
func (*todoPage) Assigns(*Ctx) hxtest.TodoListAssigns { return hxtest.TodoListAssigns{} }
func (*todoPage) Events() Events                      { return Events{"togle": func(*Ctx, Payload) {}} }

// A page that doesn't fit its template, or misses a handler for an event
// its markup sends, fails when the route is registered.
func TestLiveChecksThePage(t *testing.T) {
	for _, tc := range []struct {
		page Page
		tmpl render.Template
		code string
		want []string
	}{
		{&counterValue{}, hxtest.Badge, RT406, []string{"has no Assigns method", "needs hxtest.BadgeAssigns", "func (p *loom.counterValue) Assigns(c *loom.Ctx) hxtest.BadgeAssigns"}},
		{&wrongAssignsPage{}, hxtest.Badge, RT406, []string{"has Assigns of type func(*loom.Ctx) hxtest.TodoListAssigns", "needs hxtest.BadgeAssigns"}},
		{&badgePage{}, hxtest.Badge, "", nil},
		{&todoPage{}, hxtest.TodoList, RT422, []string{`on:change sends event "toggle"`, "it handles togle", `if the template means "togle"`, `add "toggle": p.toggle`}},
	} {
		func() {
			defer func() {
				p := recover()
				if tc.code == "" {
					if p != nil {
						t.Errorf("%T: %v", tc.page, p)
					}
					return
				}
				e, ok := p.(*Error)
				if !ok || e.Code != tc.code {
					t.Fatalf("%T: got %v, want %s", tc.page, p, tc.code)
				}
				for _, w := range tc.want {
					if !strings.Contains(e.Error(), w) {
						t.Errorf("%T: %q isn't in\n%v", tc.page, w, e)
					}
				}
				if tc.code == RT422 && !strings.HasPrefix(e.Where, "todo_list.hx:") {
					t.Errorf("RT422 where: %q, want the template position", e.Where)
				}
			}()
			New().Live("/", tc.page, tc.tmpl)
		}()
	}
}

func TestUnknownSession(t *testing.T) {
	_, srv := newTestApp(t)
	if code := post(t, srv, `{"sid":"nope","event":"inc"}`); code != 404 {
		t.Errorf("POST = %d, want 404", code)
	}
	if code := post(t, srv, `not json`); code != 400 {
		t.Errorf("POST = %d, want 400", code)
	}
	if ev := next(t, stream(t, srv, "nope", "1")); ev.event != "reload" {
		t.Errorf("got %+v, want reload", ev)
	}
}

func TestIdleSessionsExpire(t *testing.T) {
	app, srv := newTestApp(t)
	app.cfg.SessionTimeout = 50 * time.Millisecond
	sid, _ := load(t, srv)
	deadline := time.Now().Add(3 * time.Second)
	for app.session(sid) != nil {
		if time.Now().After(deadline) {
			t.Fatal("idle session was not stopped")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestViewErrors(t *testing.T) {
	app, _ := newTestApp(t)
	v, err := app.NewView(httptest.NewRequest("GET", "/", nil), "sid", true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.Event("wrong", nil)
	if e, ok := err.(*Error); !ok || e.Code != RT423 || !strings.Contains(e.Msg, "it handles boom, fail, inc, noop") {
		t.Errorf("got %v", err)
	}
	_, err = v.Event("boom", nil)
	if e, ok := err.(*Error); !ok || e.Code != RT402 || !strings.Contains(e.Where, "loom_test.go") {
		t.Errorf("got %#v", err)
	}
	if _, err := newView(&route{prototype: counterValue{}, tmpl: hxtest.Badge, group: app.top}, nil, "", true, nil); err == nil || err.(*Error).Code != RT406 {
		t.Errorf("got %v", err)
	}
	if _, err := app.NewView(httptest.NewRequest("GET", "/nope", nil), "sid", true); err == nil || err.(*Error).Code != RT414 {
		t.Errorf("got %v", err)
	}
	b, _ := json.Marshal(v.Tree())
	if !strings.HasPrefix(string(b), `{"d":["n:",`) || !strings.Contains(string(b), `"s":["\u003cspan class=\"badge\"\u003e"`) {
		t.Errorf("tree: %s", b)
	}
}

type counterValue struct{}

func (counterValue) Mount(*Ctx) {}

type valueValidateInput struct{ Name string }

func (valueValidateInput) Validate(*form.Errors) {}

// A form in a page's assigns whose input a form can't use fails at
// startup, wherever it is in the assigns.
func TestCheckForms(t *testing.T) {
	type nested struct{ F form.Form[valueValidateInput] }
	type assigns struct {
		Title string
		Rows  []nested
	}
	err := checkForms(reflect.TypeOf(assigns{}), map[reflect.Type]bool{})
	if err == nil || err.Code != form.FM802 {
		t.Fatalf("err = %v, want FM802", err)
	}
	if err := checkForms(reflect.TypeOf(hxtest.SignupAssigns{}), map[reflect.Type]bool{}); err != nil {
		t.Errorf("a valid form: %v", err)
	}
}

// hiddenPage handles "inc" but shows its button only to admins.
type hiddenPage struct {
	counterPage
	admin bool
}

func (p *hiddenPage) Assigns(c *Ctx) hxtest.CounterAssigns {
	a := p.counterPage.assigns()
	if !p.admin {
		a.Events = nil
	}
	return a
}

// An event no element on the page binds is refused before its handler
// runs (RT438): hiding a control is enough to keep it from being used.
func TestEventNotOnPage(t *testing.T) {
	app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Live("/", &hiddenPage{counterPage: counterPage{prefix: "n:"}}, hxtest.Counter)
	app.Live("/admin", &hiddenPage{counterPage: counterPage{prefix: "n:"}, admin: true}, hxtest.Counter)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	sid, _ := load(t, srv)
	res, err := http.Post(srv.URL+"/_loom/event", "application/json", strings.NewReader(`{"sid":"`+sid+`","event":"inc"}`))
	if err != nil {
		t.Fatal(err)
	}
	var e Error
	json.NewDecoder(res.Body).Decode(&struct {
		Code *string `json:"code"`
	}{&e.Code})
	res.Body.Close()
	if res.StatusCode != 400 || e.Code != RT438 {
		t.Errorf("hidden control's event: %d %s, want 400 RT438", res.StatusCode, e.Code)
	}
	if v, _ := app.Inspect(sid); !strings.HasPrefix(v.HTML, `<span class="badge">n:</span>`) {
		t.Errorf("the handler ran: %s", v.HTML)
	}
	if errs := app.errors.all(); len(errs) != 1 || errs[0].Code != RT438 {
		t.Errorf("errors = %+v, want the refusal listed", errs)
	}

	// Where the button is rendered, the same event runs.
	res, err = http.Get(srv.URL + "/admin")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	sid = sidRE.FindStringSubmatch(string(b))[1]
	if code := post(t, srv, `{"sid":"`+sid+`","event":"inc"}`); code != 200 {
		t.Errorf("shown control's event: %d", code)
	}
}
