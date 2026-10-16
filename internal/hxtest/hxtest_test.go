package hxtest

import (
	"strings"
	"testing"

	"github.com/moomerman/loom/form"
	"github.com/moomerman/loom/internal/hxtest/parts"
	"github.com/moomerman/loom/loomtest"
	"github.com/moomerman/loom/render"
)

var todos = []Todo{
	{ID: "a", Title: "Buy milk", Done: true},
	{ID: "b", Title: "Walk <dog>", Done: false},
	{ID: "c", Title: "Write \"docs\"", Done: false},
}

func TestTodoListAll(t *testing.T) {
	loomtest.Snapshot(t, loomtest.Render(t, TodoList, TodoListAssigns{Todos: todos, Filter: "all"}))
}

func TestTodoListActive(t *testing.T) {
	html := loomtest.Render(t, TodoList, TodoListAssigns{Todos: todos, Filter: "active"})
	if strings.Contains(html, "Buy milk") {
		t.Errorf("done todo shown with filter active:\n%s", html)
	}
	loomtest.Snapshot(t, html)
}

func TestTodoListEmpty(t *testing.T) {
	loomtest.Snapshot(t, loomtest.Render(t, TodoList, TodoListAssigns{}))
}

func TestBoard(t *testing.T) {
	cols := [][]Card{
		{{ID: 1, Title: "One", Priority: 0.5, Tags: []string{"x", "y"}}, {ID: 2, Title: "Two", Priority: 2}},
		{},
	}
	loomtest.Snapshot(t, loomtest.Render(t, Board, BoardAssigns{Columns: cols, Title: "plan", Show: true}))
	loomtest.Snapshot(t, loomtest.Render(t, Board, BoardAssigns{Columns: cols, Title: "hidden", Show: false}))
}

func TestProfile(t *testing.T) {
	u := User{Name: "Ada & co", UserID: 7, HTMLBio: "<b>bold</b>", CreatedAt: "2024", Address: Address{City: "London"}}
	loomtest.Snapshot(t, loomtest.Render(t, Profile, ProfileAssigns{User: u, Admin: true, Scores: []int{4, 5}, Ratio: 1.5}))
	loomtest.Snapshot(t, loomtest.Render(t, Profile, ProfileAssigns{User: u, Admin: false, Scores: []int{-4, 5}, Ratio: 0.25}))
}

func TestFooterWithoutSlots(t *testing.T) {
	loomtest.Snapshot(t, loomtest.Render(t, Footer, FooterAssigns{Count: 3}))
}

func TestRenderTreeShape(t *testing.T) {
	r := loomtest.RenderTree(t, TodoList, TodoListAssigns{Todos: todos, Filter: "all"})
	if len(r.Statics.Parts) != 3 || r.Statics != todoList0 {
		t.Fatalf("unexpected top-level statics: %+v", r.Statics)
	}
	again := loomtest.RenderTree(t, TodoList, TodoListAssigns{Todos: todos[:1], Filter: "all"})
	if again.Statics != r.Statics {
		t.Error("statics are not shared between renders")
	}
}

func TestLayouts(t *testing.T) {
	page := func() *render.Rendered {
		return &render.Rendered{Statics: &render.Statics{Parts: []string{"<p>page</p>"}}}
	}
	user := User{Name: "Ada <3"}
	loomtest.Snapshot(t, loomtest.Render(t, Root, RootAssigns{Title: "Home & away", AppName: "App", InnerBlock: page}))
	loomtest.Snapshot(t, loomtest.Render(t, Root, RootAssigns{AppName: "App"})) // no title: the default
	loomtest.Snapshot(t, loomtest.Render(t, AdminShell, AdminShellAssigns{User: user, InnerBlock: page}))
}

func TestVariants(t *testing.T) {
	loomtest.Snapshot(t, loomtest.Render(t, Toolbar, ToolbarAssigns{ID: "7", Danger: true, OnClick: render.Event{Name: "remove"}}))
	loomtest.Snapshot(t, loomtest.Render(t, Toolbar, ToolbarAssigns{ID: "8"}))
	// Go code that leaves an enum empty gets its default.
	html := loomtest.Render(t, Button, ButtonAssigns{Label: "Go"})
	if want := `<button class="primary md">Go</button>`; html != want {
		t.Errorf("got %s, want %s", html, want)
	}
}

func TestSortable(t *testing.T) {
	cards := []Card{{ID: 1, Title: "One"}, {ID: 2, Title: "Two"}}
	html := loomtest.Render(t, Sortable, SortableAssigns{Cards: cards, Label: "Plan <A>"})
	for _, want := range []string{
		`data-loom-use="` + SortableBehavior.ID + `"`,
		`data-loom-props="{&#34;axis&#34;:&#34;y&#34;,&#34;ids&#34;:[&#34;1&#34;,&#34;2&#34;]}"`,
		`data-loom-requires="[aria-label]"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in:\n%s", want, html)
		}
	}
	loomtest.Snapshot(t, html)
	got := render.Behaviors(Sortable)
	if len(got) != 4 || got[0] != SortableBehavior || got[1] != ItemBehavior || got[3] != parts.HintBehavior {
		t.Errorf("Behaviors = %v", got)
	}
}

func TestSignupForm(t *testing.T) {
	f := form.New(SignupInput{Age: 30, Shares: map[string]string{"sam": "5"}})
	f.Submit(map[string]string{"name": "", "age": "x", "agree": "on"})
	html := loomtest.Render(t, Signup, SignupAssigns{Signup: f, People: []string{"sam", "ali"}})
	for _, want := range []string{
		`<input name="name" value="" aria-invalid="true">`,
		`<span class="error">Enter a name.</span>`,
		`name="agree" checked`,
		`<input data-loom-key="sam" name="shares[sam]" value="5">`,
		`<input data-loom-key="ali" name="shares[ali]" value="">`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in:\n%s", want, html)
		}
	}
	loomtest.Snapshot(t, html)
}
