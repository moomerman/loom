package loom

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/moomerman/loom/internal/hxtest"
)

// navPage navigates on "go", and in Mount when its prototype says so.
type navPage struct {
	counterPage
	mountTo string
	missing bool // Mount calls NotFound
}

func (p *navPage) Mount(c *Ctx) {
	if p.missing {
		c.NotFound()
		return
	}
	if p.mountTo != "" {
		c.Navigate(p.mountTo)
	}
}

func (p *navPage) Assigns(c *Ctx) hxtest.CounterAssigns {
	a := p.counterPage.assigns()
	a.Events = bound(p.Events())
	return a
}

func (p *navPage) Events() Events {
	e := p.counterPage.Events()
	e["go"] = func(c *Ctx, payload Payload) { c.Navigate(payload["to"]) }
	e["lost"] = func(c *Ctx, payload Payload) { c.NotFound() }
	return e
}

// newNavApp serves pages at / and /other in the default root layout,
// /rooted in another root layout, and a plain handler at /plain that
// counts its calls.
func newNavApp(t *testing.T) (*App, *httptest.Server, *int) {
	app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Live("/", &navPage{counterPage: counterPage{prefix: "n:"}}, hxtest.Counter)
	app.Live("/other", &counterPage{prefix: "other:"}, hxtest.Counter)
	app.Live("/away", &navPage{mountTo: "/other"}, hxtest.Counter)
	app.Live("/missing", &navPage{missing: true}, hxtest.Counter)
	app.Group(RootLayout(hxtest.Root, rootAssigns)).Live("/rooted", &counterPage{prefix: "rooted:"}, hxtest.Counter)
	plain := 0
	app.Handle("GET /plain", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { plain++ }))
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return app, srv, &plain
}

// navigate fetches path as navigate.js does, leaving session from.
func navigate(t *testing.T, srv *httptest.Server, path, from string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	req.Header.Set("X-Loom-Navigate", from)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func TestNavigateSwapsPagesWithTheSameRootLayout(t *testing.T) {
	app, srv, _ := newNavApp(t)
	sid, _ := load(t, srv)
	res, body := navigate(t, srv, "/other", sid)
	if res.StatusCode != 200 || res.Header.Get("X-Loom-Navigate") != "swap" {
		t.Fatalf("navigating to /other = %d, X-Loom-Navigate %q; want 200 and swap", res.StatusCode, res.Header.Get("X-Loom-Navigate"))
	}
	m := sidRE.FindStringSubmatch(body)
	if m == nil || m[1] == sid || !strings.Contains(body, `<span class="badge">other:</span>`) {
		t.Fatalf("navigating to /other served:\n%s", body)
	}
	if app.session(sid) != nil {
		t.Error("the session the browser left is still running")
	}
	if app.session(m[1]) == nil {
		t.Error("the new page has no session")
	}
}

// A remount (no session to leave) swaps any page; the browser tells
// root layouts apart by the fingerprint in the page's loom-tree.
func TestRemount(t *testing.T) {
	_, srv, _ := newNavApp(t)
	root := regexp.MustCompile(`"root":"([\w.]+)"`)
	var roots []string
	for _, path := range []string{"/", "/other", "/rooted"} {
		res, body := navigate(t, srv, path, "-")
		if res.StatusCode != 200 || res.Header.Get("X-Loom-Navigate") != "swap" || !root.MatchString(body) {
			t.Fatalf("remounting %s = %d, X-Loom-Navigate %q", path, res.StatusCode, res.Header.Get("X-Loom-Navigate"))
		}
		roots = append(roots, root.FindStringSubmatch(body)[1])
	}
	if roots[0] != roots[1] || roots[1] == roots[2] {
		t.Errorf("root fingerprints of /, /other and /rooted: %v; want the first two equal and the third different", roots)
	}
}

func TestNavigateReloadsWhatItCantSwap(t *testing.T) {
	_, srv, plain := newNavApp(t)
	sid, _ := load(t, srv)
	for _, tc := range []struct{ name, path, from string }{
		{"another root layout", "/rooted", sid},
		{"a plain handler", "/plain", sid},
		{"an asset", "/_loom/loom.js", sid},
		{"no page", "/nowhere", sid},
		{"an unknown session", "/other", "0123"},
		{"a remount of a plain handler", "/plain", "-"},
	} {
		res, body := navigate(t, srv, tc.path, tc.from)
		if res.StatusCode != http.StatusNoContent || res.Header.Get("X-Loom-Navigate") != "reload" || body != "" {
			t.Errorf("%s: navigating to %s = %d, X-Loom-Navigate %q, body %q; want 204 and reload", tc.name, tc.path, res.StatusCode, res.Header.Get("X-Loom-Navigate"), body)
		}
	}
	if *plain != 0 {
		t.Errorf("the plain handler ran %d times; a navigation must not run it", *plain)
	}
	// An ordinary request is unchanged.
	res, err := http.Get(srv.URL + "/other")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("X-Loom-Navigate") != "" || res.Header.Get("Vary") != "X-Loom-Navigate" {
		t.Errorf("GET /other = %d, X-Loom-Navigate %q, Vary %q", res.StatusCode, res.Header.Get("X-Loom-Navigate"), res.Header.Get("Vary"))
	}
}

func TestNavigateFromHandle(t *testing.T) {
	_, srv, _ := newNavApp(t)
	sid, _ := load(t, srv)
	ch := stream(t, srv, sid, "1")
	if code := post(t, srv, `{"sid":"`+sid+`","event":"go","payload":{"to":"/other?x=1"}}`); code != http.StatusOK {
		t.Fatalf("event = %d", code)
	}
	if ev := next(t, ch); ev.event != "navigate" || ev.data != `{"to":"/other?x=1"}` {
		t.Errorf("got %s %s; want navigate {\"to\":\"/other?x=1\"}", ev.event, ev.data)
	}
}

func TestNavigateFromMountRedirects(t *testing.T) {
	_, srv, _ := newNavApp(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(srv.URL + "/away")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/other" {
		t.Errorf("GET /away = %d to %q; want 303 to /other", res.StatusCode, res.Header.Get("Location"))
	}
}

// A page whose Mount calls NotFound answers 404 with the error page, on
// a page load and a live navigation alike, and starts no session.
func TestNotFoundFromMount(t *testing.T) {
	app, srv, _ := newNavApp(t)
	res, err := http.Get(srv.URL + "/missing")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "This page doesn&#39;t exist.") {
		t.Errorf("GET /missing = %d:\n%s", res.StatusCode, body)
	}
	sid, _ := load(t, srv)
	if res, _ := navigate(t, srv, "/missing", sid); res.StatusCode != http.StatusNotFound || res.Header.Get("X-Loom-Navigate") == "swap" {
		t.Errorf("navigating to /missing = %d, %s; want 404 and no swap", res.StatusCode, res.Header.Get("X-Loom-Navigate"))
	}
	app.mu.Lock()
	n := len(app.sessions)
	app.mu.Unlock()
	if n != 1 {
		t.Errorf("%d sessions, want the one at /", n)
	}
	if errs := app.errors.all(); len(errs) != 0 {
		t.Errorf("a missing page is not an error: %+v", errs)
	}
}

// After Mount it is too late for 404: NotFound is reported, RT448.
func TestNotFoundAfterMount(t *testing.T) {
	app, _, _ := newNavApp(t)
	v, err := app.NewView(httptest.NewRequest("GET", "/", nil), "test", true)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if _, err := v.Event("lost", nil); err != nil {
		t.Fatal(err)
	}
	if f := v.Failures(); len(f) != 1 || !strings.Contains(f[0].Error(), RT448) {
		t.Errorf("failures = %v, want RT448", f)
	}
}

// The error page renders in the root layout of the route that failed,
// and the app's for a URL no route matches.
func TestErrorPageInTheRoutesRootLayout(t *testing.T) {
	app := New(Config{Logger: quiet(), Dev: DevOff})
	app.ErrorPage(hxtest.Badge, func(e PageError) hxtest.BadgeAssigns { return hxtest.BadgeAssigns{Label: e.Title} })
	app.Group(RootLayout(hxtest.Root, rootAssigns)).Live("/gone", &navPage{missing: true}, hxtest.Counter)
	srv := httptest.NewServer(app)
	defer srv.Close()
	body, res := fetch(t, srv.URL+"/gone")
	if res.StatusCode != 404 || !strings.Contains(body, " · Loom</title>") || !strings.Contains(body, `<span class="badge">This page doesn&#39;t exist.</span>`) {
		t.Errorf("GET /gone = %d:\n%s", res.StatusCode, body)
	}
	body, res = fetch(t, srv.URL+"/nowhere")
	if res.StatusCode != 404 || strings.Contains(body, " · Loom</title>") || !strings.Contains(body, `class="badge"`) {
		t.Errorf("GET /nowhere = %d:\n%s", res.StatusCode, body)
	}
}
