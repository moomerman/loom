package e2e

import (
	"net/http/httptest"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/examples/issues"
	"github.com/moomerman/loom/loomtest"
)

// TestURLPatchInBrowser filters examples/issues' team list: the view
// tabs (links to the same route) and the filter menus (events that call
// c.Patch) change the URL without a page load or a new session, the
// back button steps back through them, a search replaces its history
// entry instead of adding one per keystroke, and reloading the URL
// shows the same list. Another team's link is another path value of the
// route: it swaps in a page mounted for that team, without a page load,
// and back returns to the first team's.
func TestURLPatchInBrowser(t *testing.T) {
	db := loomtest.DB(t, loomtest.Committed(), loomtest.Migrations("../examples/issues/migrations"))
	app := loom.New(loom.Config{BaseURL: "http://localhost"})
	app.UseDB(db)
	issues.Setup(app)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	ctx := browser(t)
	logConsole(t, ctx)

	rows := `document.querySelectorAll(".list a[data-row]").length`
	same := `window.kept === "yes" && document.getElementById("loom-root").dataset.sid === window.sid`
	var history1, history2 int
	err := run(ctx,
		chromedp.EmulateViewport(1280, 900),
		chromedp.Navigate(srv.URL+"/team/ENG"),
		signInAsAda(),
		waitJS(`document.querySelector("#loom-root.loom-connected .list") !== null`),
		chromedp.Navigate(srv.URL+"/team/ENG"),
		waitJS(`location.pathname === "/team/ENG" && document.querySelector("#loom-root.loom-connected .list a[data-row]") !== null`),
		js(`window.kept = "yes"; window.sid = document.getElementById("loom-root").dataset.sid; true`),

		// A tab is a link to the same route: it patches.
		click(`[role=tab][href="/team/ENG?view=active"]`),
		waitJS(`location.search === "?view=active" && `+rows+` === 11`),
		waitJS(same),

		// A filter menu's pick calls c.Patch.
		click("button[aria-label=Priority]"),
		waitFor(`[role=listbox] [role=option][data-value="1"]`),
		click(`[role=listbox] [role=option][data-value="1"]`),
		waitJS(`location.search === "?priority=1&view=active" && `+rows+` === 3`),
		waitJS(same),

		// Back undoes the filter, then the tab, still in the same session.
		js(`history.back(); true`),
		waitJS(`location.search === "?view=active" && `+rows+` === 11`),
		js(`history.back(); true`),
		waitJS(`location.search === "" && `+rows+` === 22`),
		waitJS(same),
		js(`history.forward(); true`),
		waitJS(`location.search === "?view=active" && `+rows+` === 11`),

		// A search replaces its history entry as it goes.
		eval(`history.length`, &history1),
		typeInto("input.search", "notify"),
		waitJS(`location.search === "?q=notify&view=active" && `+rows+` === 1`),
		eval(`history.length`, &history2),
		waitJS(same),

		// The URL holds the state: a reload shows the same list.
		js(`location.reload(); true`),
		waitJS(`window.kept === undefined && document.querySelector("#loom-root.loom-connected .list") !== null`),
		waitJS(`location.search === "?q=notify&view=active" && `+rows+` === 1 && document.querySelector("input.search").value === "notify"`),

		// Another team mounts afresh, in a new session, without a load.
		js(`window.kept = "yes"; window.sid = document.getElementById("loom-root").dataset.sid; true`),
		click(`nav[aria-label=Teams] a[href="/team/DES"]`),
		waitJS(`location.pathname === "/team/DES" && location.search === "" && `+rows+` === 8`),
		waitJS(`window.kept === "yes" && document.querySelector("#loom-root.loom-connected").dataset.sid !== window.sid`),
		js(`history.back(); true`),
		waitJS(`location.pathname === "/team/ENG" && location.search === "?q=notify&view=active" && `+rows+` === 1`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if history2 != history1 {
		t.Errorf("typing a search grew the history from %d to %d entries; want it replaced", history1, history2)
	}
}
