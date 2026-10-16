package loomtest_test

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/loomtest"
	"github.com/moomerman/loom/render"
)

// savePage reports a failed save with c.Fail and tells the user.
type savePage struct{ label string }

func (p *savePage) Mount(c *loom.Ctx) { p.label = "ready" }

func (p *savePage) Assigns(c *loom.Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: p.label, Events: []render.Event{{Name: "save"}}}
}

func (p *savePage) Events() loom.Events { return loom.Events{"save": p.save} }

func (p *savePage) save(c *loom.Ctx, payload loom.Payload) {
	c.Fail(errors.New("the store is down"))
	p.label = "couldn't save"
}

func TestEventFailsOnCtxFail(t *testing.T) {
	app := loom.New()
	app.Live("/", &savePage{}, hxtest.Counter)
	ft := &fakeT{}
	p := loomtest.Mount(ft, app, "/")
	p.Event("save", nil)
	if len(ft.errors) != 1 || !strings.Contains(ft.errors[0], `event "save": the page reported an error with c.Fail: error RT419: the store is down`) || !strings.Contains(ft.errors[0], "fail_test.go") {
		t.Fatalf("errors = %q", ft.errors)
	}
	if !strings.Contains(p.HTML(), "couldn&#39;t save") {
		t.Errorf("the page didn't carry on: %s", p.HTML())
	}
}

// hiddenPage handles delete but shows no control for it.
type hiddenPage struct{ savePage }

func (p *hiddenPage) Events() loom.Events {
	return loom.Events{"save": p.save, "delete": func(c *loom.Ctx, _ loom.Payload) { p.label = "deleted" }}
}

func TestAssertRefused(t *testing.T) {
	app := loom.New()
	app.Live("/", &hiddenPage{}, hxtest.Counter)
	p := loomtest.Mount(t, app, "/")
	p.AssertRefused("delete", nil)
	if strings.Contains(p.HTML(), "deleted") {
		t.Errorf("the refused handler ran: %s", p.HTML())
	}
}

// bound returns a page's events for hxtest.Counter's buttons, so every
// event the tests send is on the page (RT438).
func bound(events loom.Events) []render.Event {
	var names []render.Event
	for name := range events {
		names = append(names, render.Event{Name: name})
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name < names[j].Name })
	return names
}
