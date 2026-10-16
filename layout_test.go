package loom

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moomerman/loom/internal/hxtest"
)

// titlePage counts like counterPage, titles itself with the count, and
// sets a layout value without changing its own assigns on "rename".
type titlePage struct{ n int }

func (p *titlePage) Mount(c *Ctx) { c.Title("n=0") }

func (p *titlePage) Assigns(c *Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "page", Events: bound(p.Events())}
}

func (p *titlePage) Events() Events {
	return Events{
		"inc": func(c *Ctx, _ Payload) {
			p.n++
			c.Title("n=" + strings.Repeat("|", p.n))
		},
		"rename": func(c *Ctx, payload Payload) { c.Set("name", payload["name"]) },
		"noop":   func(c *Ctx, _ Payload) {},
	}
}

func shellAssigns(c *Ctx) hxtest.ShellAssigns {
	name, _ := c.Get("name").(string)
	if name == "" {
		name = "Ada"
	}
	return hxtest.ShellAssigns{User: hxtest.User{Name: name}}
}

func rootAssigns(c *Ctx) hxtest.RootAssigns {
	return hxtest.RootAssigns{Title: c.TitleOr(""), AppName: "Test"}
}

func newLayoutApp(t *testing.T) (*App, *httptest.Server) {
	app, srv := newTestApp(t) // "/" has the default root layout
	app.cfg.Title = "Counter"
	g := app.Group(Layout(hxtest.Shell, shellAssigns), RootLayout(hxtest.Root, rootAssigns))
	g.Live("/shell", &titlePage{}, hxtest.Counter)
	inner := g.Group() // inherits both layouts
	inner.Live("/inner", &titlePage{}, hxtest.Counter)
	admin := g.Group(Layout(hxtest.AdminShell, func(c *Ctx) hxtest.AdminShellAssigns {
		return hxtest.AdminShellAssigns{User: shellAssigns(c).User}
	}))
	admin.Live("/admin", &titlePage{}, hxtest.Counter)
	return app, srv
}

func get(t *testing.T, srv *httptest.Server, path string) string {
	t.Helper()
	res, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLayouts(t *testing.T) {
	_, srv := newLayoutApp(t)
	for _, tc := range []struct {
		path string
		want []string
		not  []string
	}{
		{"/", []string{`<title data-default="Counter">Counter</title>`, `<div id="loom-root" data-sid="`, `<span class="badge">n:</span>`}, []string{"<nav>"}},
		{"/shell", []string{`<html lang="en">`, `<title data-default="Test" data-suffix=" · Loom">n=0 · Loom</title>`, `<nav><span class="user">Ada</span></nav>`, `<main><span class="badge">page</span><menu hidden>`}, []string{"<aside>"}},
		{"/inner", []string{`<html lang="en">`, `<nav><span class="user">Ada</span></nav>`}, nil},
		{"/admin", []string{`<html lang="en">`, "<aside>Admin</aside>", `<nav><span class="user">Ada</span></nav>`}, nil},
	} {
		body := get(t, srv, tc.path)
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %q:\n%s", tc.path, w, body)
			}
		}
		for _, w := range tc.not {
			if strings.Contains(body, w) {
				t.Errorf("%s has %q:\n%s", tc.path, w, body)
			}
		}
	}
}

func TestLiveTitleAndLayoutValues(t *testing.T) {
	_, srv := newLayoutApp(t)
	sid, _ := loadPath(t, srv, "/shell")
	ch := stream(t, srv, sid, "1")

	// A title change with no change to the tree: a diff without "diff".
	post(t, srv, `{"sid":"`+sid+`","event":"inc"}`)
	if ev := next(t, ch); ev.event != "diff" || ev.data != `{"title":"n=|","v":2}` {
		t.Errorf("got %+v", ev)
	}
	// The handler changes no page state but sets a layout value: the layout re-renders.
	post(t, srv, `{"sid":"`+sid+`","event":"rename","payload":{"name":"Bo"}}`)
	if ev := next(t, ch); ev.event != "diff" || ev.data != `{"diff":{"d":{"0":"Bo"}},"v":3}` {
		t.Errorf("got %+v", ev)
	}
	// Nothing changed: nothing is sent, so the next event is v4.
	post(t, srv, `{"sid":"`+sid+`","event":"noop"}`)
	post(t, srv, `{"sid":"`+sid+`","event":"inc"}`)
	if ev := next(t, ch); ev.data != `{"title":"n=||","v":4}` {
		t.Errorf("got %+v", ev)
	}
	// A stale browser gets the full tree and the title.
	ch2 := stream(t, srv, sid, "1")
	if ev := next(t, ch2); ev.event != "full" || !strings.Contains(ev.data, `"title":"n=||"`) || !strings.Contains(ev.data, `"v":4`) {
		t.Errorf("got %+v", ev)
	}
}

func loadPath(t *testing.T, srv *httptest.Server, path string) (sid, body string) {
	t.Helper()
	body = get(t, srv, path)
	m := sidRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("GET %s: no session in\n%s", path, body)
	}
	return m[1], body
}

func TestRoutesShowLayouts(t *testing.T) {
	app, _ := newLayoutApp(t)
	got := map[string]RouteInfo{}
	for _, r := range app.Routes() {
		got[r.Path] = r
	}
	for path, want := range map[string][2]string{
		"/":      {"loom.DefaultRoot", ""},
		"/shell": {"hxtest.Root", "hxtest.Shell"},
		"/inner": {"hxtest.Root", "hxtest.Shell"},
		"/admin": {"hxtest.Root", "hxtest.AdminShell"},
	} {
		if r := got[path]; r.RootLayout != want[0] || r.Layout != want[1] {
			t.Errorf("%s: root %q, layout %q; want %q", path, r.RootLayout, r.Layout, want)
		}
	}
}

func TestLayoutErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    func()
		code string
		msg  string
	}{
		{"wrong assigns type", func() { New().RootLayout(hxtest.Root, shellAssigns) },
			RT412, "the assigns func for layout hxtest.Root is func(*loom.Ctx) hxtest.ShellAssigns, but must be func(*loom.Ctx) hxtest.RootAssigns"},
		{"not a func", func() { Layout(hxtest.Shell, hxtest.ShellAssigns{}) }, RT412, "is hxtest.ShellAssigns"},
		{"app layout as root", func() { New().RootLayout(hxtest.Shell, shellAssigns) }, RT413, "hxtest.Shell is not a root layout"},
		{"root layout as app layout", func() { Layout(hxtest.Root, rootAssigns) }, RT413, "hxtest.Root is a root layout"},
		{"no inner_block", func() {
			Layout(hxtest.Badge, func(*Ctx) hxtest.BadgeAssigns { return hxtest.BadgeAssigns{} })
		}, RT413, "layout hxtest.Badge has no inner_block slot"},
	} {
		func() {
			defer func() {
				e, ok := recover().(*Error)
				if !ok || e.Code != tc.code || !strings.Contains(e.Msg, tc.msg) || e.Fix == "" || !strings.Contains(e.Where, "layout_test.go") {
					t.Errorf("%s: got %#v", tc.name, e)
				}
			}()
			tc.f()
		}()
	}
}
