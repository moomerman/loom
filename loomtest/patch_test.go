package loomtest_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/loomtest"
)

// queryPage shows its URL's q in hxtest.Numbers, whose link goes to
// /items?q=1.
type queryPage struct{ q string }

func (p *queryPage) Mount(c *loom.Ctx) {}

func (p *queryPage) HandleParams(c *loom.Ctx, query url.Values) { p.q = query.Get("q") }

func (p *queryPage) Events() loom.Events {
	return loom.Events{
		"search": func(c *loom.Ctx, payload loom.Payload) {
			c.Patch("?q="+url.QueryEscape(payload["value"]), loom.Replace())
		},
		"later": func(c *loom.Ctx, payload loom.Payload) { c.SendAfter(time.Second, "search", payload) },
	}
}

func (p *queryPage) Assigns(c *loom.Ctx) hxtest.NumbersAssigns {
	return hxtest.NumbersAssigns{Text: p.q, Events: bound(p.Events())}
}

func newQueryApp() *loom.App {
	app := loom.New()
	app.Live("/items", &queryPage{}, hxtest.Numbers)
	app.Live("/list", &queryPage{}, hxtest.Numbers)
	return app
}

func TestPatchAndURL(t *testing.T) {
	p := loomtest.Mount(t, newQueryApp(), "/items?q=abc")
	hasText(t, p, "<p>3 true false</p>")
	p.Event("search", loom.Payload{"value": "zz top"})
	if got := p.URL(); got != "/items?q=zz+top" {
		t.Errorf("URL() = %q; want /items?q=zz+top", got)
	}
	hasText(t, p, "<p>6 false false</p>")
	p.Patch("/items")
	hasText(t, p, "<p>0 true true</p>")
}

func TestPatchFromATimer(t *testing.T) {
	p := loomtest.Mount(t, newQueryApp(), "/items")
	p.Event("later", loom.Payload{"value": "x"})
	p.Advance(time.Second)
	if got := p.URL(); got != "/items?q=x" {
		t.Errorf("URL() = %q; want /items?q=x", got)
	}
}

func TestFollowPatchesWithinTheRoute(t *testing.T) {
	p := loomtest.Mount(t, newQueryApp(), "/items")
	p.Follow("a")
	if got := p.URL(); got != "/items?q=1" {
		t.Errorf("URL() = %q; want /items?q=1", got)
	}
	if p.Navigated() != "" {
		t.Errorf("Navigated() = %q; want none", p.Navigated())
	}
	hasText(t, p, "<p>1 true false</p>")
}

func TestFollowLeavesForAnotherRoute(t *testing.T) {
	p := loomtest.Mount(t, newQueryApp(), "/list")
	p.Follow("a")
	if p.Navigated() != "/items?q=1" || p.URL() != "/list" {
		t.Errorf("Navigated() = %q, URL() = %q; want /items?q=1 and /list", p.Navigated(), p.URL())
	}
}

func hasText(t *testing.T, p *loomtest.Page, want string) {
	t.Helper()
	if !strings.Contains(p.HTML(), want) {
		t.Errorf("the page lacks %s:\n%s", want, p.HTML())
	}
}
