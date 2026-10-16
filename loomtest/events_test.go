package loomtest_test

import (
	"strings"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/loomtest"
)

// formPage records the last event and its payload in recorded: each
// page load has its own copy of the page, out of the test's reach.
type formPage struct{}

var recorded string

func (p *formPage) Mount(c *loom.Ctx) {}

func (p *formPage) Assigns(c *loom.Ctx) hxtest.NewTodoAssigns {
	return hxtest.NewTodoAssigns{Title: "draft", Count: 2}
}

func (p *formPage) Events() loom.Events {
	record := func(name string) func(*loom.Ctx, loom.Payload) {
		return func(c *loom.Ctx, payload loom.Payload) {
			var kv []string
			for _, k := range []string{"list", "title", "priority", "urgent", "notes", "value", "checked", "count"} {
				if v, ok := payload[k]; ok {
					kv = append(kv, k+"="+v)
				}
			}
			recorded = name + " " + strings.Join(kv, " ")
		}
	}
	e := loom.Events{}
	for _, name := range []string{"add", "typing", "prioritise", "urgent", "clear"} {
		e[name] = record(name)
	}
	return e
}

func TestDrivingThePageThroughItsMarkup(t *testing.T) {
	app := loom.New()
	app.Live("/", &formPage{}, hxtest.NewTodo)
	p := loomtest.Mount(t, app, "/")

	for _, tc := range []struct {
		do   func()
		want string
	}{
		{func() { p.Click("button.clear span") }, "clear count=2"},
		{func() { p.Input("input[name=title]", "Milk") }, "typing value=Milk"},
		{func() { p.Change("select[name=priority]", "1") }, "prioritise value=1"},
		{func() { p.Change("input[name=urgent]", "true") }, "urgent list=home value=on checked=true"},
		{func() { p.Submit("form.new-todo", loom.Payload{"title": "Milk"}) }, "add list=home title=Milk priority=2 notes=none"},
		{func() { p.Submit("button[type=submit]", nil) }, "add list=home title=draft priority=2 notes=none"},
	} {
		tc.do()
		if got := recorded; got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestDriversFailOnMarkupMistakes(t *testing.T) {
	app := loom.New()
	app.Live("/", &formPage{}, hxtest.NewTodo)
	for _, tc := range []struct {
		do   func(p *loomtest.Page)
		want string
	}{
		{func(p *loomtest.Page) { p.Submit("form", loom.Payload{"titel": "Milk"}) }, `the form has no field named "titel"`},
		{func(p *loomtest.Page) { p.Click("textarea") }, "neither it nor an element around it has on:click"},
		{func(p *loomtest.Page) { p.Click("button") }, "matches 2 elements"},
		{func(p *loomtest.Page) { p.Change("button.clear", "x") }, "not an input, select or textarea"},
		{func(p *loomtest.Page) { p.Event("delte", nil) }, "RT423"},
	} {
		msg := fatal(func(ft testing.TB) { tc.do(loomtest.Mount(ft, app, "/")) })
		if !strings.Contains(msg, tc.want) {
			t.Errorf("got %q, want %q", msg, tc.want)
		}
	}
}
