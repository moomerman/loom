package todo

import (
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/loomtest"
)

// TestFlowPlanTheDay is a flow test: its steps, in
// testdata/flows/TestFlowPlanTheDay.md, are what a person reviews.
func TestFlowPlanTheDay(t *testing.T) {
	app, todos := testApp(t, jobs.RealClock{})
	var p *loomtest.Page
	open := func(path string) { p = loomtest.Mount(t, app, path, loomtest.NoLayout()) }

	f := loomtest.Flow(t, "A guest plans the day")
	f.Step("The guest opens the todo list and sees the two starter todos", func() {
		open("/")
		p.AssertHTML(".todo-list li", 2)
		p.AssertText(".count", "2 items left")
	})
	f.Step(`They add "Buy milk"`, func() {
		p.Submit("form.new-todo", loom.Payload{"title": "Buy milk"})
		p.AssertText(".todo-list li", "Buy milk")
		p.AssertText(".count", "3 items left")
	})
	f.Step("A blank todo is not added", func() {
		p.Submit("form.new-todo", loom.Payload{"title": "   "})
		p.AssertHTML(".todo-list li", 3)
	})
	f.Step(`They tick "Buy milk" off, and it shows as done`, func() {
		p.Change(`li[data-loom-key="`+idOf(t, todos, "Buy milk")+`"] input[type=checkbox]`, "true")
		p.AssertText("li.done", "Buy milk")
		p.AssertText(".count", "2 items left")
	})
	f.Step(`"Active" shows only what is left to do`, func() {
		open("/?filter=active")
		p.AssertHTML(".todo-list li", 2)
		p.AssertHTML("li.done", 0)
	})
	f.Step(`"Clear completed" removes "Buy milk"`, func() {
		open("/")
		p.Click("footer button")
		p.AssertHTML("li.done", 0)
		p.AssertHTML(".todo-list li", 2)
	})
}
