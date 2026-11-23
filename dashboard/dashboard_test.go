package dashboard_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/dashboard"
	"github.com/moomerman/loom/devmail"
	"github.com/moomerman/loom/jobsweb"
	"github.com/moomerman/loom/loomtest"
	"github.com/moomerman/loom/trace"
)

func allow(c *loom.Ctx) error { return nil }

// HomePage and ItemPage are a host app's pages, for the routes list.
type HomePage struct{}

func (p *HomePage) Mount(c *loom.Ctx)                             {}
func (p *HomePage) Events() loom.Events                           { return nil }
func (p *HomePage) Assigns(c *loom.Ctx) dashboard.SessionsAssigns { return dashboard.SessionsAssigns{} }

// host is an app with two pages of its own, a plain handler, the jobs
// dashboard and devmail mounted, and the dashboard at /admin/loom.
func host(t *testing.T) *loom.App {
	app := loom.New()
	app.Live("/", &HomePage{}, dashboard.Sessions)
	app.Group(loom.SignedIn("/sign-in")).Live("/items/{id}", &HomePage{}, dashboard.Sessions)
	app.Handle("POST /logout", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	admin := app.Group(loom.Auth(allow))
	admin.Mount("/admin/jobs", jobsweb.New(jobsweb.Config{}))
	admin.Mount("/admin/loom", dashboard.New(dashboard.Config{}))
	app.Mount("/dev/mail", devmail.New())
	return app
}

// TestOverview: the overview counts the host's activity, not its own,
// links to the other sub-apps, and refreshes on a timer.
func TestOverview(t *testing.T) {
	app := host(t)
	ctx := context.Background()
	app.Emit(ctx, trace.PageMount, "route", "/", "ms", 2.0, "outcome", "ok")
	app.Emit(ctx, trace.PageEvent, "route", "/", "event", "save", "ms", 3.0, "outcome", "ok")
	app.Emit(ctx, trace.PageEvent, "route", "/admin/loom", "event", "tick", "ms", 1.0, "outcome", "ok")
	app.Emit(ctx, trace.Error, "msg", "boom", "code", "RT402", "route", "/items/{id}")

	p := loomtest.Mount(t, app, "/admin/loom")
	if p.Title() != "Overview" || !strings.Contains(p.Document(), "/admin/loom/_assets/icon.") {
		t.Fatalf("title %q, document:\n%s", p.Title(), p.Document())
	}
	p.AssertHTML(".sidebar nav a.current", 1)
	// jobsweb is linked; devmail is dev-only and this isn't dev.
	p.AssertHTML(`.elsewhere a[href="/admin/jobs"]`, 1)
	p.AssertHTML(`.elsewhere a[href="/dev/mail"]`, 0)
	// The busiest routes are the host's: the dashboard's own tick isn't.
	p.AssertHTML(`tbody a.route[href="/"]`, 1)
	if strings.Contains(p.HTML(), `>/admin/loom<`) {
		t.Fatalf("the dashboard counts itself:\n%s", p.HTML())
	}
	p.AssertText(".alarm .value", "1")
	p.AssertText(".events .detail", "boom")
	p.AssertHTML(".chart rect.bar-page", 1)
	p.AssertAccessible()

	if got := p.Timers()["tick"]; got != 2*time.Second {
		t.Fatalf("tick in %v, want 2s", got)
	}
	app.Emit(ctx, trace.PageMount, "route", "/", "ms", 2.0, "outcome", "ok")
	v := p.Version()
	p.Advance(2 * time.Second)
	if p.Version() == v {
		t.Fatal("the overview didn't refresh")
	}
}

// TestRoutes: every route, grouped by sub-app, with who may open it;
// paths without parameters are links; the search filters them.
func TestRoutes(t *testing.T) {
	app := host(t)
	p := loomtest.Mount(t, app, "/admin/loom/routes")
	p.AssertText("section h2", "Your app")
	p.AssertHTML(`a.path[href="/"]`, 1)
	p.AssertHTML(`a.path[href="/admin/jobs/cron"]`, 1)
	p.AssertText(".path", "/items/{id}")
	p.AssertHTML(`a.path[href="/items/{id}"]`, 0)
	if !strings.Contains(p.HTML(), "POST") || !strings.Contains(p.HTML(), "/logout") {
		t.Fatalf("no handler:\n%s", p.HTML())
	}
	if !strings.Contains(p.HTML(), "signed in") || !strings.Contains(p.HTML(), "restricted") {
		t.Fatalf("no access badges:\n%s", p.HTML())
	}
	p.AssertAccessible()

	p.Input(`input[name="filter"]`, "items")
	p.AssertHTML("tbody tr", 1)
	p.AssertText(".topbar .aside", "1 of")
	p.Input(`input[name="filter"]`, "nothing-like-this")
	p.AssertText(".empty h2", "No route matches.")
}

// TestActivity: the feed shows the host's events by kind, newest first,
// with the kind in the URL.
func TestActivity(t *testing.T) {
	app := host(t)
	ctx := context.Background()
	app.Emit(ctx, trace.PageEvent, "route", "/", "event", "save", "ms", 3.0, "outcome", "ok")
	app.Emit(ctx, trace.JobFinish, "type", "Email", "queue", "mail", "state", "completed", "ms", 12.5)
	app.Emit(ctx, trace.PageEvent, "route", "/admin/loom/queries", "event", "tick", "outcome", "ok")

	p := loomtest.Mount(t, app, "/admin/loom/activity")
	p.AssertHTML(".events tbody tr", 2)
	p.AssertText(".events .subject", "Email")
	p.AssertText(`.tab.current`, "All")
	p.AssertAccessible()

	p.Patch("/admin/loom/activity?kind=job")
	p.AssertHTML(".events tbody tr", 1)
	p.AssertText(".events .ms", "12.5 ms")
	p.Patch("/admin/loom/activity?kind=error")
	p.AssertText(".empty h2", "Nothing has happened yet.")
}

// TestQueriesAndSessions: both render without a database or open pages.
func TestQueriesAndSessions(t *testing.T) {
	app := host(t)
	p := loomtest.Mount(t, app, "/admin/loom/queries")
	p.AssertText(".empty h2", "This app has no database.")
	p.AssertAccessible()
	p = loomtest.Mount(t, app, "/admin/loom/sessions")
	p.AssertText(".note", "0 pages open")
	p.AssertAccessible()
}

// TestRequiresAuth: mounting the dashboard without an auth check fails
// at startup, as it shows the app's SQL and sessions.
func TestRequiresAuth(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("mounting without auth didn't panic")
		}
	}()
	loom.New().Mount("/admin/loom", dashboard.New(dashboard.Config{}))
}
