package loom

import (
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/moomerman/loom/internal/hxtest"
)

// tabPage keeps its tab in the URL (?tab=...) and a counter that the URL
// doesn't hold, so a patch can be told from a remount.
type tabPage struct {
	counterPage
	tab    string
	params int // HandleParams calls
}

func (p *tabPage) HandleParams(c *Ctx, query url.Values) {
	p.params++
	p.tab = query.Get("tab")
	if p.tab == "old" {
		c.Patch("?tab=new") // a corrected URL
	}
}

func (p *tabPage) Events() Events {
	e := p.counterPage.Events()
	e["tab"] = func(c *Ctx, payload Payload) { c.Patch("/tabs/" + payload["id"] + "?tab=" + payload["tab"]) }
	e["search"] = func(c *Ctx, payload Payload) { c.Patch("?tab="+payload["tab"], Replace()) }
	e["away"] = func(c *Ctx, payload Payload) { c.Patch("/other") }
	return e
}

func (p *tabPage) Assigns(c *Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: p.tab + ":" + strings.Repeat("|", p.n) + ":" + c.Request.PathValue("id"), Events: bound(p.Events())}
}

// noParamsPage patches without HandleParams.
type noParamsPage struct{ counterPage }

func (p *noParamsPage) Assigns(c *Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "plain", Events: bound(p.Events())}
}

func (p *noParamsPage) Events() Events {
	return Events{"tab": func(c *Ctx, payload Payload) { c.Patch("?tab=x") }}
}

func newPatchApp(t *testing.T) (*App, *httptest.Server) {
	app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Live("/tabs/{id}", &tabPage{}, hxtest.Counter)
	app.Live("/other", &counterPage{prefix: "other:"}, hxtest.Counter)
	app.Live("/plain", &noParamsPage{}, hxtest.Counter)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return app, srv
}

func patchView(t *testing.T, app *App, path string) *View {
	t.Helper()
	v, err := app.NewView(httptest.NewRequest("GET", path, nil), "test", false)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestHandleParamsRunsAfterMount(t *testing.T) {
	app, _ := newPatchApp(t)
	v := patchView(t, app, "/tabs/1?tab=open")
	if !strings.Contains(v.HTML(), "open::1") || v.URL() != "/tabs/1?tab=open" {
		t.Errorf("mounted %s:\n%s", v.URL(), v.HTML())
	}
}

func TestPatchInAnEventKeepsThePage(t *testing.T) {
	app, _ := newPatchApp(t)
	v := patchView(t, app, "/tabs/1?tab=open")
	v.Event("inc", nil)
	d, err := v.Event("tab", Payload{"id": "1", "tab": "closed"})
	if err != nil || d == nil {
		t.Fatalf("tab: %v, diff %v", err, d)
	}
	if !strings.Contains(v.HTML(), "closed:|:1") {
		t.Errorf("after the patch the page shows:\n%s", v.HTML())
	}
	if url, replace := v.Patched(); url != "/tabs/1?tab=closed" || replace {
		t.Errorf("Patched() = %q, %v; want /tabs/1?tab=closed, push", url, replace)
	}
	if url, _ := v.Patched(); url != "" {
		t.Errorf("Patched() again = %q; want none", url)
	}
	if v.URL() != "/tabs/1?tab=closed" {
		t.Errorf("URL() = %q", v.URL())
	}
	v.Event("search", Payload{"tab": "x"})
	if url, replace := v.Patched(); url != "/tabs/1?tab=x" || !replace {
		t.Errorf("Patched() = %q, %v; want /tabs/1?tab=x, replace", url, replace)
	}
}

func TestPatchFromHandleParamsCorrectsTheURL(t *testing.T) {
	app, _ := newPatchApp(t)
	v := patchView(t, app, "/tabs/1")
	if _, err := v.Patch("/tabs/1?tab=old"); err != nil {
		t.Fatal(err)
	}
	if v.URL() != "/tabs/1?tab=new" || !strings.Contains(v.HTML(), "new::1") {
		t.Errorf("URL %s, HTML:\n%s", v.URL(), v.HTML())
	}
	// On a page load, it redirects.
	v = patchView(t, app, "/tabs/1?tab=old")
	if got := v.Navigation(); got != "?tab=new" {
		t.Errorf("a patch on load navigates to %q; want ?tab=new", got)
	}
}

func TestPatchToAnotherRouteNavigates(t *testing.T) {
	app, _ := newPatchApp(t)
	v := patchView(t, app, "/tabs/1")
	v.Event("away", nil)
	failures := v.Failures()
	var e *Error
	if len(failures) != 1 || !errors.As(failures[0], &e) || e.Code != RT426 {
		t.Fatalf("failures = %v; want RT426", failures)
	}
	if got := v.Navigation(); got != "/other" {
		t.Errorf("Navigation() = %q; want /other", got)
	}
	if _, err := v.Patch("/other"); !errors.As(err, &e) || e.Code != RT426 {
		t.Errorf("View.Patch to another route = %v; want RT426", err)
	}
	if _, err := v.Patch("https://example.com/tabs/1"); !errors.As(err, &e) || e.Code != RT426 {
		t.Errorf("View.Patch to another host = %v; want RT426", err)
	}
}

// A patch keeps the page that Mount loaded for its path, so changing a
// path value (/tabs/1 to /tabs/2) can't be a patch: it navigates, and the
// page mounts afresh for the new value.
func TestPatchToAnotherPathValueNavigates(t *testing.T) {
	app, _ := newPatchApp(t)
	v := patchView(t, app, "/tabs/1?tab=open")
	v.Event("tab", Payload{"id": "2", "tab": "closed"})
	failures := v.Failures()
	var e *Error
	if len(failures) != 1 || !errors.As(failures[0], &e) || e.Code != RT426 || !strings.Contains(e.Msg, `{id} from "1" to "2"`) {
		t.Fatalf("failures = %v; want RT426 naming {id}", failures)
	}
	if got := v.Navigation(); got != "/tabs/2?tab=closed" {
		t.Errorf("Navigation() = %q; want /tabs/2?tab=closed", got)
	}
	if v.URL() != "/tabs/1?tab=open" {
		t.Errorf("URL() = %q; want it unchanged", v.URL())
	}
	if _, err := v.Patch("/tabs/2"); !errors.As(err, &e) || e.Code != RT426 {
		t.Errorf("View.Patch to another path value = %v; want RT426", err)
	}
}

func TestPatchWithoutHandleParams(t *testing.T) {
	app, _ := newPatchApp(t)
	v := patchView(t, app, "/plain")
	v.Event("tab", nil)
	failures := v.Failures()
	var e *Error
	if len(failures) != 1 || !errors.As(failures[0], &e) || e.Code != RT427 {
		t.Fatalf("failures = %v; want RT427", failures)
	}
	if got := v.Navigation(); got != "?tab=x" {
		t.Errorf("Navigation() = %q; want ?tab=x", got)
	}
}

type badParamsPage struct{ counterPage }

func (p *badParamsPage) HandleParams(c *Ctx) {}

func TestHandleParamsSignatureIsChecked(t *testing.T) {
	defer func() {
		e, ok := recover().(*Error)
		if !ok || e.Code != RT428 || !strings.Contains(e.Msg, "func(*loom.Ctx)") {
			t.Errorf("got %v; want RT428", e)
		}
	}()
	New().Live("/", &badParamsPage{}, hxtest.Badge)
}

func TestNavigationWithinTheRoutePatchesTheSession(t *testing.T) {
	app, srv := newPatchApp(t)
	sid, _ := loadPath(t, srv, "/tabs/1?tab=open")
	ch := stream(t, srv, sid, "1")
	res, body := navigate(t, srv, "/tabs/1?tab=closed", sid)
	if res.StatusCode != 200 || res.Header.Get("X-Loom-Navigate") != "patch" {
		t.Fatalf("navigating within the route = %d, %q: %s", res.StatusCode, res.Header.Get("X-Loom-Navigate"), body)
	}
	if !strings.Contains(body, `"url":"/tabs/1?tab=closed"`) || !strings.Contains(body, `"v":2`) {
		t.Errorf("answer %s", body)
	}
	if ev := next(t, ch); ev.event != "diff" || !strings.Contains(ev.data, "closed::1") {
		t.Errorf("stream got %+v; want the diff", ev)
	}
	if s := app.Sessions(); len(s) != 1 || s[0].SID != sid || s[0].URL != "/tabs/1?tab=closed" {
		t.Errorf("sessions = %+v; want the patched session at /tabs/1?tab=closed", s)
	}
	// A correction comes back in the answer.
	_, body = navigate(t, srv, "/tabs/1?tab=old", sid)
	if !strings.Contains(body, `"url":"/tabs/1?tab=new"`) {
		t.Errorf("answer %s; want the corrected URL", body)
	}
	// Another route still swaps.
	if res, _ := navigate(t, srv, "/other", sid); res.Header.Get("X-Loom-Navigate") != "swap" {
		t.Errorf("navigating to /other = %q; want swap", res.Header.Get("X-Loom-Navigate"))
	}
}

// A link to another path value of the route mounts the page afresh for
// it, as a link to another route does: the issue tracker's /team/DES to
// /team/ENG once patched, and kept showing DES's issues.
func TestNavigationToAnotherPathValueSwaps(t *testing.T) {
	_, srv := newPatchApp(t)
	sid, _ := loadPath(t, srv, "/tabs/1?tab=open")
	res, body := navigate(t, srv, "/tabs/3?tab=closed", sid)
	if res.Header.Get("X-Loom-Navigate") != "swap" || !strings.Contains(body, "closed::3") {
		t.Errorf("navigating to /tabs/3 = %q: %s; want a swap to a page mounted for 3", res.Header.Get("X-Loom-Navigate"), body)
	}
}

func TestNavigationWithinARouteWithoutHandleParamsSwaps(t *testing.T) {
	_, srv := newPatchApp(t)
	sid, _ := loadPath(t, srv, "/plain")
	if res, _ := navigate(t, srv, "/plain?x=1", sid); res.Header.Get("X-Loom-Navigate") != "swap" {
		t.Errorf("got %q; want swap", res.Header.Get("X-Loom-Navigate"))
	}
}

func TestPatchIsStreamedAfterTheDiff(t *testing.T) {
	_, srv := newPatchApp(t)
	sid, _ := loadPath(t, srv, "/tabs/1?tab=open")
	ch := stream(t, srv, sid, "1")
	if code := post(t, srv, `{"sid":"`+sid+`","event":"tab","payload":{"id":"1","tab":"done"}}`); code != 200 {
		t.Fatalf("event = %d", code)
	}
	if ev := next(t, ch); ev.event != "diff" {
		t.Errorf("first %+v; want the diff", ev)
	}
	if ev := next(t, ch); ev.event != "patch" || ev.data != `{"replace":false,"to":"/tabs/1?tab=done"}` {
		t.Errorf("then %+v; want the patch", ev)
	}
}

func TestRestartMountsAtThePatchedURL(t *testing.T) {
	_, srv := newPatchApp(t)
	sid, _ := loadPath(t, srv, "/tabs/1?tab=open")
	ch := stream(t, srv, sid, "1")
	post(t, srv, `{"sid":"`+sid+`","event":"tab","payload":{"id":"1","tab":"done"}}`)
	next(t, ch)
	next(t, ch)
	post(t, srv, `{"sid":"`+sid+`","event":"boom"}`)
	if ev := next(t, ch); ev.event != "full" || !strings.Contains(ev.data, "done::1") {
		t.Errorf("after a restart got %+v; want the page at ?tab=done", ev)
	}
}
