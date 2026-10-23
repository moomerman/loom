package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/examples/issues"
	"github.com/moomerman/loom/loomtest"
)

// TestIssuesInBrowser signs in to examples/issues and works with the
// keyboard: shortcuts from the hotkeys behavior, the kit's dropdown in a
// property menu, the dialog behavior in the composer, and the toast.
func TestIssuesInBrowser(t *testing.T) {
	db := loomtest.DB(t, loomtest.Committed(), loomtest.Migrations("../examples/issues/migrations"))
	app := loom.New(loom.Config{BaseURL: "http://localhost"})
	app.UseDB(db)
	issues.Setup(app)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	ctx := browser(t)

	var status, toast string
	err := run(ctx,
		chromedp.EmulateViewport(1280, 900), // wide enough for the sidebars
		// Sent to the auth sub-app's sign-in page; signing in comes back.
		chromedp.Navigate(srv.URL+"/team/ENG"),
		signInAsAda(),
		waitJS(`document.querySelector("#loom-root.loom-connected .list") !== null`),

		// G then 2 goes to the second team in the sidebar, Engineering,
		// once the hotkeys behavior has loaded.
		untilJS(chromedp.Steps(chromedp.KeyEvent("g"), chromedp.KeyEvent("2")), `location.pathname === "/team/ENG"`),
		waitJS(`location.pathname === "/team/ENG" && document.querySelector("#loom-root.loom-connected .list a[data-row]") !== null`),

		// J moves through the rows; Enter opens the focused one. The first
		// J may come before the hotkeys behavior has loaded on the new page.
		untilJS(chromedp.KeyEvent("j"), `document.activeElement?.matches("a[data-row]")`),
		chromedp.KeyEvent("j"),
		waitJS(`document.activeElement === document.querySelectorAll("a[data-row]")[1]`),
		chromedp.KeyEvent(kb.Enter),
		waitJS(`location.pathname.startsWith("/issue/ENG-") && document.querySelector("#loom-root.loom-connected aside[aria-label=Properties]") !== null`),

		// The status menu: a click opens it, arrow keys and Enter pick.
		click("button[aria-label=Status]"),
		waitJS(`document.activeElement.getAttribute("role") === "option"`),
		chromedp.KeyEvent(kb.End),
		chromedp.KeyEvent(kb.Enter),
		waitJS(`document.querySelector("[role=listbox]") === null`),
		waitJS(`document.querySelector("button[aria-label=Status]").textContent.includes("Canceled")`),
		text("button[aria-label=Status]", &status),

		// C opens the composer with the title focused; Ctrl+Enter creates.
		chromedp.KeyEvent("c"),
		waitJS(`document.activeElement === document.querySelector("form[role=dialog] input[name=title]")`),
		chromedp.KeyEvent("Typed in a browser"),
		chromedp.KeyEvent("\r", chromedp.KeyModifiers(chromedp.ModifierCtrl)),
		waitJS(`document.querySelector("form[role=dialog]") === null`),
		waitFor(".toast"),
		text(".toast", &toast),
		// The toast behavior dismisses it after a few seconds.
		waitJS(`document.querySelector(".toast") === null`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if status == "" || toast == "" {
		t.Errorf("status %q, toast %q", status, toast)
	}
}

// signInAsAda signs in on the auth sub-app's sign-in page, where a
// signed-in page sends the browser, with the example's demo password.
func signInAsAda() action {
	return chromedp.Steps(
		waitFor("#loom-root.loom-connected input[name=email]"),
		chromedp.SendKeys("input[name=email]", "ada@example.com"),
		chromedp.SendKeys("input[name=password]", "loom-issues"),
		click("button[type=submit]"),
	)
}

// untilJS repeats an action until the JavaScript expression is true.
func untilJS(a action, js string) action {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		for {
			if _, err := a(ctx, t); err != nil {
				return err
			}
			if ok, err := chromedp.Evaluate[bool](js)(ctx, t); err == nil && ok {
				return nil
			}
			select {
			case <-ctx.Done():
				return errors.New("timed out waiting for " + js)
			case <-time.After(100 * time.Millisecond):
			}
		}
	})
}

// run runs the actions in order, saying which one failed.
func run(ctx context.Context, actions ...action) error {
	for i, a := range actions {
		if err := chromedp.Do(ctx, a); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	return nil
}
