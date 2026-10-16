package e2e

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/render"
)

// TestLiveNavigation follows links in examples/todo between pages with
// the same root layout: each swaps the live root, so the document (and a
// value set on window) survives, the new page's stylesheet applies, and
// its events work. The back button
// navigates live too. A link to a page with another root layout (the
// jobs dashboard) loads it as an ordinary page.
func TestLiveNavigation(t *testing.T) {
	srv := httptest.NewServer(todoApp(t))
	t.Cleanup(srv.Close)
	ctx := browser(t)
	logConsole(t, ctx)

	kept := `window.kept === "yes"`
	var title, maxWidth string
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		waitFor("#loom-root.loom-connected .todo-list li"),
		eval(`window.kept = "yes"; true`, nil),

		// The filter link is to the same route, and the page has
		// HandleParams, so it patches the page, keeping its session.
		eval(`window.sid = document.getElementById("loom-root").dataset.sid; true`, nil),
		click(`.filters a[href="/?filter=active"]`),
		waitJS(`location.search === "?filter=active" && document.querySelector('.filters a[href="/?filter=active"]').classList.contains("current")`),
		waitJS(kept+` && document.getElementById("loom-root").dataset.sid === window.sid`),
		// The page's events still work.
		click(`li[data-loom-key="00000000-0000-0000-0000-000000000001"] input[type=checkbox]`),
		waitJS(`document.querySelectorAll(".todo-list li").length === 1 && !document.querySelector('li[data-loom-key="00000000-0000-0000-0000-000000000001"]')`),

		// The About page, outside the app layout.
		click(`.topnav a[href="/about"]`),
		waitJS(`location.pathname === "/about" && document.querySelector("section.about") !== null`),
		waitJS(kept),
		eval(`document.title`, &title),
		eval(`getComputedStyle(document.querySelector("section.about")).maxWidth`, &maxWidth),
	)
	if err != nil {
		t.Fatal(err)
	}
	if title != "About" {
		t.Errorf("title on /about = %q; want About", title)
	}
	if maxWidth != "512px" {
		t.Errorf("section.about max-width = %q; want 512px, from the About page's stylesheet", maxWidth)
	}

	var n int
	err = chromedp.Run(ctx,
		// Back to the list.
		eval(`history.back(); true`, nil),
		waitJS(`location.search === "?filter=active" && document.querySelector(".todo-list") !== null`),
		waitJS(kept),
		typeInto(".new-todo input", "Walk\n"),
		waitJS(`document.querySelectorAll(".todo-list li").length === 2`),
		eval(`document.querySelectorAll("link[rel=stylesheet]").length`, &n),
	)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d stylesheets after navigating; want the page's one", n)
	}

	// Another root layout: an ordinary page load.
	err = chromedp.Run(ctx,
		eval(`const a = document.createElement("a"); a.href = "/admin/jobs"; document.body.append(a); a.click(); true`, nil),
		waitJS(`location.pathname === "/admin/jobs" && window.kept === undefined`),
	)
	if err != nil {
		t.Fatal(err)
	}
}

// goPage is a badge that navigates to /sort on "go".
type goPage struct{}

func (p *goPage) Mount(c *loom.Ctx) {}

func (p *goPage) Assigns(c *loom.Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "start", Events: []render.Event{{Name: "go"}}}
}

func (p *goPage) Events() loom.Events {
	return loom.Events{"go": func(c *loom.Ctx, _ loom.Payload) { c.Navigate("/sort") }}
}

// TestNavigateFromHandle has a page's c.Navigate take the browser, live,
// to a page with behaviors, whose script it loads and runs.
func TestNavigateFromHandle(t *testing.T) {
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Live("/", &goPage{}, hxtest.Counter)
	app.Live("/sort", &sortPage{}, hxtest.Sortable)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	ctx := browser(t)
	logConsole(t, ctx)

	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		waitJS(`document.getElementById("loom-root").classList.contains("loom-connected")`),
		eval(`window.kept = "yes"; document.querySelector('[on\\:click="go"]').click(); true`, nil),
		waitJS(`location.pathname === "/sort" && document.querySelectorAll("li[data-mounted]").length === 3`),
		waitJS(`window.kept === "yes"`),
	)
	if err != nil {
		t.Fatal(err)
	}
}
