// Package e2e drives examples/todo in a headless Chrome. It is its own
// module so that Loom itself has no dependencies.
//
//	cd e2e && go test ./...
//
// The test finds Chrome on the PATH, or uses $LOOM_CHROME. It skips if
// there is no browser. The app stores todos in a fresh Postgres database
// from LOOM_TEST_DATABASE_URL, and the tests skip without one.
package e2e

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/examples/todo"
	"github.com/moomerman/loom/kit"
	"github.com/moomerman/loom/loomtest"
	"github.com/moomerman/loom/render"
)

// todoApp returns the todo app on a database of its own, migrated from
// examples/todo/migrations.
func todoApp(t *testing.T) *loom.App {
	app, _ := todoAppDB(t)
	return app
}

// todoAppDB is todoApp, with its database.
func todoAppDB(t *testing.T) (*loom.App, *loom.DB) {
	db := loomtest.DB(t, loomtest.Committed(), loomtest.Migrations("../examples/todo/migrations"))
	app := loom.New()
	app.UseDB(db)
	todo.Setup(app)
	return app, db
}

func browser(t *testing.T, flags ...chromedp.ExecAllocatorOption) context.Context {
	t.Helper()
	path := os.Getenv("LOOM_CHROME")
	if path == "" {
		for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome"} {
			if p, err := exec.LookPath(name); err == nil {
				path = p
				break
			}
		}
	}
	if path == "" {
		t.Skip("no Chrome found; set LOOM_CHROME to run the browser tests")
	}
	// A cold Chrome on a fresh CI runner can take longer than chromedp's
	// default 20s to print its DevTools URL.
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(path), chromedp.NoSandbox, chromedp.WSURLReadTimeout(time.Minute))
	opts = append(opts, flags...)
	ctx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(cancel)
	// Drop chromedp's log of protocol events it can't decode: newer Chrome
	// versions add enum values its generated types don't know.
	ctx, cancel = chromedp.NewContext(ctx, chromedp.WithErrorf(func(string, ...any) {}))
	t.Cleanup(cancel)
	// Start Chrome now, so the test's own timeout below doesn't include it.
	if err := chromedp.Run(ctx); err != nil {
		t.Fatalf("starting Chrome: %v", err)
	}
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// eval runs JavaScript and stores its result in out.
func eval(js string, out any) chromedp.Action { return chromedp.Evaluate(js, out) }

// waitJS polls until the JavaScript expression is true, across page
// loads too. It polls from Go rather than with chromedp.Poll, whose
// promise can be left waiting in a page that is still starting (one
// that loads heavy scripts as it connects) or that navigates away.
func waitJS(js string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		for {
			var ok bool
			if err := chromedp.Evaluate(`!!(`+js+`)`, &ok).Do(ctx); err == nil && ok {
				return nil
			}
			select {
			case <-ctx.Done():
				return errors.New("timed out waiting for " + js)
			case <-time.After(20 * time.Millisecond):
			}
		}
	})
}

// The helpers below find elements with JavaScript and then send real
// mouse and keyboard input. chromedp's own DOM queries depend on protocol
// events that recent Chrome versions encode differently.

// waitFor waits until an element matches the selector.
func waitFor(sel string) chromedp.Action {
	return waitJS(`document.querySelector(` + quote(sel) + `) !== null`)
}

// click clicks the middle of the element matching the selector.
func click(sel string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var xy []float64
		js := `(() => { const r = document.querySelector(` + quote(sel) + `).getBoundingClientRect(); return [r.x + r.width/2, r.y + r.height/2]; })()`
		if err := chromedp.Evaluate(js, &xy).Do(ctx); err != nil {
			return err
		}
		return chromedp.MouseClickXY(xy[0], xy[1]).Do(ctx)
	})
}

// typeInto focuses the element and types the text, as keystrokes.
func typeInto(sel, text string) chromedp.Action {
	return chromedp.Tasks{
		click(sel),
		chromedp.KeyEvent(text),
	}
}

// text returns the text content of the element matching the selector.
func text(sel string, out *string) chromedp.Action {
	return eval(`document.querySelector(`+quote(sel)+`).textContent`, out)
}

func quote(s string) string { return strconv.Quote(s) }

// TestTodoInBrowser uses the todo list: events go to the server, and the
// diffs come back on the stream.
func TestTodoInBrowser(t *testing.T) {
	srv := httptest.NewServer(todoApp(t))
	t.Cleanup(srv.Close) // after the browser closes its streams
	ctx := browser(t)
	logConsole(t, ctx)

	var titles string
	var focused, sameNode bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		waitFor("#loom-root.loom-connected .todo-list li"),
		// Tag the first item's element, to check it survives updates.
		eval(`document.querySelector('li[data-loom-key="00000000-0000-0000-0000-000000000001"]').loomTag = "kept"; true`, nil),

		// Add.
		typeInto(".new-todo input", "Buy milk\n"),
		waitJS(`document.querySelectorAll(".todo-list li").length === 3`),
		eval(`[...document.querySelectorAll(".todo-list li label")].map(s => s.textContent.trim()).join("|")`, &titles),
		eval(`document.activeElement === document.querySelector(".new-todo input") && document.querySelector(".new-todo input").value === ""`, &focused),
	)
	if err != nil {
		t.Fatal(err)
	}
	if titles != "Try Loom|Read docs/syntax.md|Buy milk" {
		t.Errorf("titles after add = %q", titles)
	}
	if !focused {
		t.Error("after add, the input should keep focus and be cleared")
	}

	var count string
	err = chromedp.Run(ctx,
		// Toggle.
		click(`li[data-loom-key="00000000-0000-0000-0000-000000000001"] input[type=checkbox]`),
		waitJS(`document.querySelector('li[data-loom-key="00000000-0000-0000-0000-000000000001"]').classList.contains("done")`),
		text(".count", &count),
		eval(`document.querySelector('li[data-loom-key="00000000-0000-0000-0000-000000000001"]').loomTag === "kept"`, &sameNode),
	)
	if err != nil {
		t.Fatal(err)
	}
	if count != "2 items left" {
		t.Errorf("count after toggle = %q", count)
	}
	if !sameNode {
		t.Error("toggling replaced the item's element; keyed items should keep their DOM nodes")
	}

	err = chromedp.Run(ctx,
		// Delete the middle item.
		click(`li[data-loom-key="00000000-0000-0000-0000-000000000002"] button`),
		waitJS(`document.querySelectorAll(".todo-list li").length === 2`),
		eval(`[...document.querySelectorAll(".todo-list li label")].map(s => s.textContent.trim()).join("|")`, &titles),
		eval(`document.querySelector('li[data-loom-key="00000000-0000-0000-0000-000000000001"]').loomTag === "kept"`, &sameNode),
		text(".count", &count),
	)
	if err != nil {
		t.Fatal(err)
	}
	if titles != "Try Loom|Buy milk" {
		t.Errorf("titles after delete = %q", titles)
	}
	if !sameNode {
		t.Error("deleting another item replaced item 1's element")
	}
	if count != "1 item left" {
		t.Errorf("count after delete = %q", count)
	}

	// Filtering, by URL, hides done items.
	err = chromedp.Run(ctx,
		click(`.filters a[href="/?filter=active"]`),
		waitJS(`location.search === "?filter=active" && document.querySelectorAll(".todo-list li").length === 1`),
	)
	if err != nil {
		t.Fatal(err)
	}
}

// logConsole logs the page's console and exceptions, to see why a test
// failed if it does.
func logConsole(t *testing.T, ctx context.Context) {
	chromedp.ListenTarget(ctx, func(ev any) {
		switch e := ev.(type) {
		case *cdpruntime.EventConsoleAPICalled:
			for _, a := range e.Args {
				t.Logf("console: %s %s", a.Value, a.Description)
			}
		case *cdpruntime.EventExceptionThrown:
			t.Logf("exception: %s", e.ExceptionDetails.Error())
		}
	})
}

// TestWatchInBrowser opens the list in two tabs: a change made in one
// shows in the other, and so does a change made straight to the
// database, since the pages watch the todos table.
func TestWatchInBrowser(t *testing.T) {
	app, db := todoAppDB(t)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	one := browser(t)
	logConsole(t, one)
	two, cancel := chromedp.NewContext(one)
	t.Cleanup(cancel)
	open := chromedp.Tasks{chromedp.Navigate(srv.URL), waitFor("#loom-root.loom-connected .todo-list li")}
	if err := chromedp.Run(one, open); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(two, open); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(one, typeInto(".new-todo input", "Buy milk\n")); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(two, waitJS(`document.querySelectorAll(".todo-list li").length === 3`)); err != nil {
		t.Fatal("the other tab didn't show the new todo:", err)
	}
	if _, err := db.Exec(context.Background(), `UPDATE todos SET title = 'Try Loom today' WHERE title = 'Try Loom'`); err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{one, two} {
		if err := chromedp.Run(ctx, waitJS(`document.querySelector(".todo-list li label").textContent.includes("Try Loom today")`)); err != nil {
			t.Fatal("a change in the database didn't show:", err)
		}
	}
}

// counterPage is a live page counting clicks.
type counterPage struct{ n int }

func (p *counterPage) Mount(c *loom.Ctx) {}

func (p *counterPage) Events() loom.Events {
	return loom.Events{"inc": func(c *loom.Ctx, _ loom.Payload) { p.n++ }}
}

func (p *counterPage) Assigns(c *loom.Ctx) kit.ButtonAssigns {
	return kit.ButtonAssigns{Label: "Clicked " + strconv.Itoa(p.n), OnClick: render.Event{Name: "inc"}}
}

// TestReconnect checks that a live page whose stream drops catches up
// when it reconnects, by changing the page while the stream is down.
func TestReconnect(t *testing.T) {
	app := loom.New()
	app.Live("/", &counterPage{}, kit.Button)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	ctx := browser(t)

	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL),
		waitFor("#loom-root.loom-connected button"),
		// Drop every stream connection; the client reconnects after a second.
		chromedp.ActionFunc(func(context.Context) error {
			srv.CloseClientConnections()
			return nil
		}),
		click("button"),
		waitJS(`document.querySelector("button").textContent === "Clicked 1"`),
	)
	if err != nil {
		t.Fatal(err)
	}
}
