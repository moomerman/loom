package loom_test

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/subapptest"
	"github.com/moomerman/loom/loomtest"
)

func get(t *testing.T, client *http.Client, url string) (*http.Response, string) {
	t.Helper()
	res, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func quietApp() *loom.App {
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return app
}

// TestMountTwice mounts one sub-app at two prefixes: each copy's links
// and assets resolve against its own prefix.
func TestMountTwice(t *testing.T) {
	app := quietApp()
	app.Mount("/a", &subapptest.App{})
	app.Group().Mount("/admin/b", &subapptest.App{})
	srv := httptest.NewServer(app)
	defer srv.Close()

	cssRE := regexp.MustCompile(`href="(/[^"]*/_assets/style\.[0-9a-f]{8}\.css)"`)
	for _, prefix := range []string{"/a", "/admin/b"} {
		res, body := get(t, http.DefaultClient, srv.URL+prefix)
		if res.StatusCode != 200 {
			t.Fatalf("GET %s = %d: %s", prefix, res.StatusCode, body)
		}
		for _, want := range []string{`href="` + prefix + `"`, `href="` + prefix + `/items/1"`, `href="` + prefix + `/items/2"`} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s: no %s in\n%s", prefix, want, body)
			}
		}
		m := cssRE.FindStringSubmatch(body)
		if m == nil || !strings.HasPrefix(m[1], prefix+"/_assets/") {
			t.Fatalf("GET %s: no stylesheet under the prefix in\n%s", prefix, body)
		}
		res, css := get(t, http.DefaultClient, srv.URL+m[1])
		if res.StatusCode != 200 || !strings.Contains(css, "font-family") || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/css") {
			t.Errorf("GET %s = %d %v: %q", m[1], res.StatusCode, res.Header, css)
		}
		if res, _ := get(t, http.DefaultClient, srv.URL+prefix+"/_assets/style.css"); res.StatusCode != 404 {
			t.Errorf("unhashed asset = %d, want 404", res.StatusCode)
		}

		_, body = get(t, http.DefaultClient, srv.URL+prefix+"/items/2")
		if !strings.Contains(body, `class="back" href="`+prefix+`"`) || !strings.Contains(body, `href="`+prefix+`/items/2/next"`) {
			t.Errorf("GET %s/items/2:\n%s", prefix, body)
		}
	}
	if res, _ := get(t, http.DefaultClient, srv.URL+"/items/1"); res.StatusCode != 404 {
		t.Errorf("unprefixed route = %d, want 404", res.StatusCode)
	}

	p := loomtest.Mount(t, app, "/admin/b/items/7")
	p.AssertHTML(`a[href="/admin/b"]`, 1)
	if doc := p.Document(); !strings.Contains(doc, "/admin/b/_assets/style.") {
		t.Errorf("document:\n%s", doc)
	}
}

func TestMountRoutes(t *testing.T) {
	app := quietApp()
	mw := func(h http.Handler) http.Handler { return h }
	admin := app.Group(loom.Auth(allowAll), loom.Middleware(mw))
	admin.Mount("/admin/sub", &subapptest.App{NeedsAuth: true}, loom.Middleware(mw))
	routes := app.Routes()
	if len(routes) != 2 {
		t.Fatalf("routes = %+v", routes)
	}
	want := loom.RouteInfo{Path: "/admin/sub", Page: "*subapptest.IndexPage", Component: "subapptest.Index", RootLayout: "subapptest.Root",
		Prefix: "/admin/sub", Mount: "*subapptest.App", Auth: []string{"loom_test.allowAll"}, Middleware: 2, Watch: []string{}, RateLimit: []string{}}
	if !reflect.DeepEqual(routes[0], want) {
		t.Errorf("route = %+v\nwant %+v", routes[0], want)
	}
	if routes[1].Path != "/admin/sub/items/{id}" {
		t.Errorf("route = %+v", routes[1])
	}
}

func allowAll(c *loom.Ctx) error { return nil }

// TestMountRequiresAuth: a sub-app that requires auth can't be mounted
// without a check (RT415), and can be with one anywhere in the chain.
func TestMountRequiresAuth(t *testing.T) {
	err := panicOf(func() { quietApp().Mount("/sub", &subapptest.App{NeedsAuth: true}) })
	var le *loom.Error
	if !errors.As(err, &le) || le.Code != loom.RT415 || !strings.Contains(le.Fix, "loom.Auth") {
		t.Fatalf("panic = %v", err)
	}
	quietApp().Group(loom.Auth(allowAll)).Group().Mount("/sub", &subapptest.App{NeedsAuth: true})
	quietApp().Mount("/sub", &subapptest.App{NeedsAuth: true}, loom.Auth(allowAll))

	for _, prefix := range []string{"sub", "/", "/sub/", "/{x}"} {
		err := panicOf(func() { quietApp().Mount(prefix, &subapptest.App{}) })
		if !errors.As(err, &le) || le.Code != loom.RT416 {
			t.Errorf("Mount(%q) panic = %v", prefix, err)
		}
	}
}

func panicOf(f func()) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err, _ = p.(error)
		}
	}()
	f()
	return nil
}

// TestAuth checks auth on load (403, redirect, allowed), the order of
// nested checks, and a user losing permission mid-session.
func TestAuth(t *testing.T) {
	var allowed atomic.Bool
	var calls []string
	requireUser := func(c *loom.Ctx) error {
		calls = append(calls, "outer")
		if _, err := c.Request.Cookie("user"); err != nil {
			return loom.Redirect("/login")
		}
		return nil
	}
	requireAdmin := func(c *loom.Ctx) error {
		calls = append(calls, "inner")
		if !allowed.Load() {
			return loom.ErrForbidden
		}
		return nil
	}
	header := func(name string) func(http.Handler) http.Handler {
		return func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("X-Order", name)
				h.ServeHTTP(w, r)
			})
		}
	}
	app := quietApp()
	app.Group(loom.Auth(requireUser), loom.Middleware(header("outer"))).
		Mount("/admin", &subapptest.App{NeedsAuth: true}, loom.Auth(requireAdmin), loom.Middleware(header("inner")))
	srv := httptest.NewServer(app)
	defer srv.Close()

	res, _ := get(t, noRedirects, srv.URL+"/admin")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
		t.Errorf("no user: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if got := res.Header.Values("X-Order"); !reflect.DeepEqual(got, []string{"outer", "inner"}) {
		t.Errorf("middleware order = %v", got)
	}

	withUser := func(method, url, body string) *http.Response {
		req, _ := http.NewRequest(method, url, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: "user", Value: "ann"})
		req.AddCookie(&http.Cookie{Name: loom.BrowserCookie, Value: "ann's browser, loaded the page itself"})
		res, err := noRedirects.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	calls = nil
	if res := withUser("GET", srv.URL+"/admin", ""); res.StatusCode != http.StatusForbidden {
		t.Errorf("not admin: %d", res.StatusCode)
	}
	if !reflect.DeepEqual(calls, []string{"outer", "inner"}) {
		t.Errorf("auth order = %v", calls)
	}

	allowed.Store(true)
	res = withUser("GET", srv.URL+"/admin", "")
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("admin: %d", res.StatusCode)
	}
	sid := regexp.MustCompile(`data-sid="([0-9a-f]+)"`).FindStringSubmatch(string(b))[1]

	// The stream is open; the user loses permission; their next event
	// is refused (403) and ends the session, and the browser is told to reload.
	stream := withUser("GET", srv.URL+"/_loom/stream?v=1&sid="+sid, "")
	defer stream.Body.Close()
	allowed.Store(false)
	if res := withUser("POST", srv.URL+"/_loom/event", `{"sid":"`+sid+`","event":"anything"}`); res.StatusCode != http.StatusForbidden {
		t.Fatalf("event: %d", res.StatusCode)
	}
	sc := bufio.NewScanner(stream.Body)
	for sc.Scan() && sc.Text() != "event: reload" {
	}
	if sc.Text() != "event: reload" {
		t.Fatal("the stream ended without a reload")
	}
	if res := withUser("GET", srv.URL+"/admin", ""); res.StatusCode != http.StatusForbidden {
		t.Errorf("reload after losing permission: %d", res.StatusCode)
	}
	if res := withUser("POST", srv.URL+"/_loom/event", `{"sid":"`+sid+`","event":"x"}`); res.StatusCode != http.StatusNotFound {
		t.Errorf("event on the ended session: %d", res.StatusCode)
	}
}

func TestHandle(t *testing.T) {
	var order []string
	mw := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "middleware")
			h.ServeHTTP(w, r)
		})
	}
	requireUser := func(c *loom.Ctx) error {
		order = append(order, "auth")
		if _, err := c.Request.Cookie("user"); err != nil {
			return loom.Redirect("/login")
		}
		return nil
	}
	app := quietApp()
	app.Handle("POST /login", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "user", Value: r.FormValue("user")})
		w.WriteHeader(http.StatusNoContent)
	}))
	g := app.Group(loom.Auth(requireUser), loom.Middleware(mw))
	g.Mount("/api", handlerApp{})
	srv := httptest.NewServer(app)
	defer srv.Close()

	res, err := noRedirects.PostForm(srv.URL+"/login", url.Values{"user": {"ann"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusNoContent || len(res.Cookies()) != 1 || res.Cookies()[0].Value != "ann" {
		t.Errorf("POST /login: %d %v", res.StatusCode, res.Cookies())
	}
	if res, _ := get(t, noRedirects, srv.URL+"/login"); res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /login: %d, want 405", res.StatusCode)
	}

	// In a mounted group, the path is relative to the prefix, and the
	// group's middleware and auth run first.
	res, _ = get(t, noRedirects, srv.URL+"/api/ping")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
		t.Errorf("no user: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if !reflect.DeepEqual(order, []string{"middleware", "auth"}) {
		t.Errorf("order = %v", order)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/ping", nil)
	req.AddCookie(&http.Cookie{Name: "user", Value: "ann"})
	res, err = noRedirects.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(b) != "pong" {
		t.Errorf("GET /api/ping: %d %q", res.StatusCode, b)
	}

	defer func() {
		if e, ok := recover().(*loom.Error); !ok || e.Code != loom.RT416 {
			t.Errorf("a pattern without a method: %v, want RT416", e)
		}
	}()
	app.Handle("/x", http.NotFoundHandler())
}

// handlerApp is a sub-app with a plain handler.
type handlerApp struct{}

func (handlerApp) Routes(g *loom.Group) {
	g.Handle("GET /ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "pong") }))
}
