package e2e

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/kit"
	"github.com/moomerman/loom/render"
)

// dropdownPage shows the kit's dropdown, choosing a fruit.
type dropdownPage struct {
	open  bool
	value string
}

func (p *dropdownPage) Mount(c *loom.Ctx) {}

func (p *dropdownPage) Events() loom.Events {
	return loom.Events{
		"toggle": func(c *loom.Ctx, _ loom.Payload) { p.open = !p.open },
		"pick":   func(c *loom.Ctx, payload loom.Payload) { p.value, p.open = payload["value"], false },
		"close":  func(c *loom.Ctx, _ loom.Payload) { p.open = false },
	}
}

func (p *dropdownPage) Assigns(c *loom.Ctx) kit.DropdownAssigns {
	label := "Fruit"
	if p.value != "" {
		label = p.value
	}
	return kit.DropdownAssigns{
		Label: label, Open: p.open, Value: p.value,
		OnToggle: render.Event{Name: "toggle"}, OnSelect: render.Event{Name: "pick"}, OnClose: render.Event{Name: "close"},
		InnerBlock: func() *render.Rendered {
			var r []*render.Rendered
			for _, f := range []string{"apple", "pear", "plum"} {
				r = append(r, kit.DropdownOption.Render(nil, kit.DropdownOptionAssigns{Value: f, Label: f}))
			}
			return &render.Rendered{Statics: &render.Statics{Parts: []string{"", "", "", ""}}, Dynamics: []render.Dynamic{r[0], r[1], r[2]}}
		},
	}
}

// dialogPage shows the kit's dialog until it is closed.
type dialogPage struct{ open bool }

func (p *dialogPage) Mount(c *loom.Ctx) { p.open = true }

func (p *dialogPage) Events() loom.Events {
	return loom.Events{"close": func(c *loom.Ctx, _ loom.Payload) { p.open = false }}
}

func (p *dialogPage) Assigns(c *loom.Ctx) kit.DialogAssigns {
	return kit.DialogAssigns{
		Open: p.open, Title: "Delete?", OnClose: render.Event{Name: "close"},
		Actions: func() *render.Rendered {
			return &render.Rendered{Statics: &render.Statics{Parts: []string{`<button id="yes">Yes</button>`}}}
		},
	}
}

// tabsPage shows the kit's tabs, one selected.
type tabsPage struct{ selected string }

func (p *tabsPage) Mount(c *loom.Ctx) { p.selected = "all" }

func (p *tabsPage) Events() loom.Events {
	return loom.Events{"pick": func(c *loom.Ctx, payload loom.Payload) { p.selected = payload["filter"] }}
}

func (p *tabsPage) Assigns(c *loom.Ctx) kit.TabsAssigns {
	return kit.TabsAssigns{Label: "Show", InnerBlock: func() *render.Rendered {
		var r []render.Dynamic
		for _, f := range []string{"all", "active", "done"} {
			r = append(r, kit.Tab.Render(nil, kit.TabAssigns{Label: f, Selected: p.selected == f, OnClick: render.Event{Name: "pick", Vals: []render.Val{{Name: "filter", Value: render.Escape(f)}}}}))
		}
		return &render.Rendered{Statics: &render.Statics{Parts: []string{"", "", "", ""}}, Dynamics: r}
	}}
}

func kitApp(t *testing.T) string {
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Dev: loom.DevOn})
	app.Live("/dropdown", &dropdownPage{}, kit.Dropdown)
	app.Live("/dialog", &dialogPage{}, kit.Dialog)
	app.Live("/tabs", &tabsPage{}, kit.Tabs)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestKitDropdown drives the kit's dropdown with the keyboard: open,
// arrow to an option, select it, reopen on the selected option, and
// close with Escape back to the button.
func TestKitDropdown(t *testing.T) {
	url := kitApp(t)
	ctx := browser(t)
	const active = `document.activeElement.dataset.value`
	err := chromedp.Do(ctx,
		chromedp.Navigate(url+"/dropdown"),
		waitFor(`#loom-root.loom-connected [role=listbox][hidden]`),
		click(".trigger"),
		waitJS(`!document.querySelector("[role=listbox]").hidden && `+active+` === "apple"`),
		chromedp.KeyEvent(kb.ArrowDown),
		waitJS(active+` === "pear"`),
		chromedp.KeyEvent(kb.Enter),
		waitJS(`document.querySelector("[role=listbox]").hidden && document.querySelector(".trigger").textContent === "pear"`),
		click(".trigger"),
		waitJS(active+` === "pear" && document.activeElement.getAttribute("aria-selected") === "true"`),
		chromedp.KeyEvent(kb.Escape),
		waitJS(`document.querySelector("[role=listbox]").hidden && document.activeElement === document.querySelector(".trigger")`),
	)
	if err != nil {
		t.Fatal(err)
	}
}

// TestKitDialog checks the kit's dialog takes focus, keeps Tab inside,
// and closes on Escape.
func TestKitDialog(t *testing.T) {
	url := kitApp(t)
	ctx := browser(t)
	err := chromedp.Do(ctx,
		chromedp.Navigate(url+"/dialog"),
		waitFor(`#loom-root.loom-connected [role=dialog]`),
		waitJS(`document.activeElement.classList.contains("close")`),
		chromedp.KeyEvent(kb.Tab),
		waitJS(`document.activeElement.id === "yes"`),
		chromedp.KeyEvent(kb.Tab),
		waitJS(`document.activeElement.classList.contains("close")`),
		chromedp.KeyEvent(kb.Escape),
		waitJS(`document.querySelector("[role=dialog]") === null`),
	)
	if err != nil {
		t.Fatal(err)
	}
}

// TestKitTabs moves between the kit's tabs with the arrow keys, Home and
// End, selecting each tab it moves to.
func TestKitTabs(t *testing.T) {
	url := kitApp(t)
	ctx := browser(t)
	const selected = `document.querySelector("[aria-selected=true]").textContent`
	err := chromedp.Do(ctx,
		chromedp.Navigate(url+"/tabs"),
		waitFor(`#loom-root.loom-connected [role=tab][aria-selected=true]`),
		click(`[role=tab][aria-selected=true]`),
		chromedp.KeyEvent(kb.ArrowRight),
		waitJS(selected+` === "active" && document.activeElement.textContent === "active" && document.activeElement.tabIndex === 0`),
		chromedp.KeyEvent(kb.End),
		waitJS(selected+` === "done" && document.activeElement.textContent === "done"`),
		chromedp.KeyEvent(kb.Home),
		waitJS(selected+` === "all"`),
	)
	if err != nil {
		t.Fatal(err)
	}
}
