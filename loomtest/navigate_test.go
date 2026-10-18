package loomtest_test

import (
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/loomtest"
)

type navPage struct{}

func (p *navPage) Mount(c *loom.Ctx) {}

func (p *navPage) Assigns(c *loom.Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "here", Events: bound(p.Events())}
}

func (p *navPage) Events() loom.Events {
	return loom.Events{"open": func(c *loom.Ctx, payload loom.Payload) { c.Navigate(c.Path("/items/" + payload["id"])) }}
}

func TestNavigated(t *testing.T) {
	app := loom.New()
	app.Live("/", &navPage{}, hxtest.Counter)
	p := loomtest.Mount(t, app, "/")
	if got := p.Navigated(); got != "" {
		t.Errorf("after Mount, Navigated() = %q; want none", got)
	}
	p.Event("open", loom.Payload{"id": "7"})
	if got := p.Navigated(); got != "/items/7" {
		t.Errorf("Navigated() = %q; want /items/7", got)
	}
}

// itemPage shows item 7 and no other.
type itemPage struct{ navPage }

func (p *itemPage) Mount(c *loom.Ctx) {
	if c.Request.PathValue("id") != "7" {
		c.NotFound()
	}
}

func TestAssertNotFound(t *testing.T) {
	app := loom.New()
	app.Live("/items/{id}", &itemPage{}, hxtest.Counter)
	loomtest.AssertNotFound(t, app, "/items/8")
	loomtest.Mount(t, app, "/items/7")
}

// chrome is a live layout counting bumps.
type chrome struct{ count int }

func (l *chrome) Mount(c *loom.Ctx) {}

func (l *chrome) Assigns(c *loom.Ctx) hxtest.ChromeAssigns {
	return hxtest.ChromeAssigns{Count: l.count}
}

func (l *chrome) Events() loom.Events {
	return loom.Events{"bump": func(c *loom.Ctx, _ loom.Payload) { l.count++ }, "dismiss": func(c *loom.Ctx, _ loom.Payload) {}}
}

func TestNavigate(t *testing.T) {
	app := loom.New()
	g := app.Group(loom.LiveLayout(hxtest.Chrome, &chrome{}))
	g.Live("/", &navPage{}, hxtest.Counter)
	g.Live("/items/{id}", &itemPage{}, hxtest.Counter)
	p := loomtest.Mount(t, app, "/")
	p.Click("button.bump")
	p.Click("button.bump")
	item := p.Navigate("/items/7")
	item.AssertText("button.bump", "2")
	item.Click("button.bump")
	item.AssertText("button.bump", "3")
}

// keptChrome is a live layout whose count survives a remount.
type keptChrome struct {
	kept  struct{ Count int }
	toast string
}

func (l *keptChrome) Keep() any { return &l.kept }

func (l *keptChrome) Mount(c *loom.Ctx) { l.toast = "hi" }

func (l *keptChrome) Assigns(c *loom.Ctx) hxtest.ChromeAssigns {
	return hxtest.ChromeAssigns{Count: l.kept.Count, Toast: l.toast}
}

func (l *keptChrome) Events() loom.Events {
	return loom.Events{"bump": func(c *loom.Ctx, _ loom.Payload) { l.kept.Count++ }, "dismiss": func(c *loom.Ctx, _ loom.Payload) { l.toast = "" }}
}

func TestRemount(t *testing.T) {
	app := loom.New()
	app.Group(loom.LiveLayout(hxtest.Chrome, &keptChrome{})).Live("/", &navPage{}, hxtest.Counter)
	p := loomtest.Mount(t, app, "/")
	p.Click("button.bump")
	p.Click("span.toast")
	p = p.Remount()
	p.AssertText("button.bump", "1") // kept
	p.AssertText("span.toast", "hi") // not kept: Mount set it again
	p.AssertEvent("page.mount", "kept", "restored the layout's")

	// Without Keep, a remount starts afresh.
	app = loom.New()
	app.Group(loom.LiveLayout(hxtest.Chrome, &chrome{})).Live("/", &navPage{}, hxtest.Counter)
	p = loomtest.Mount(t, app, "/")
	p.Click("button.bump")
	p.Remount().AssertText("button.bump", "0")
}
