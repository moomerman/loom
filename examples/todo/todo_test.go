package todo

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/loomtest"
)

// The tests run on a Postgres database built from migrations/, so the
// Guest user starts with two todos, first and second. Without
// LOOM_TEST_DATABASE_URL they skip.

// The seeded todos' ids (migrations/0003_todo_uuids.sql).
const (
	first  = "00000000-0000-0000-0000-000000000001"
	second = "00000000-0000-0000-0000-000000000002"
)

var ctx = context.Background()

// testApp returns the app on a test database rolled back at the end.
func testApp(t *testing.T, clock jobs.Clock) (*loom.App, *Todos) {
	todos := &Todos{DB: loomtest.DB(t), Clock: clock}
	app := loom.New()
	app.UseDB(todos.DB)
	routes(app, todos)
	return app, todos
}

// idOf returns the id of the todo with the title.
func idOf(t *testing.T, todos *Todos, title string) string {
	t.Helper()
	id, err := loom.Get[string](ctx, todos.DB, `SELECT id::text FROM todos WHERE title = $1`, title)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// toggle flips a todo, as the page's toggle_todo event does.
func toggle(t *testing.T, todos *Todos, id string) {
	t.Helper()
	if err := todos.Toggle(ctx, "Guest", id); err != nil {
		t.Fatal(err)
	}
}

func TestTodoPage(t *testing.T) {
	app, todos := testApp(t, jobs.RealClock{})
	p := loomtest.Mount(t, app, "/", loomtest.NoLayout())
	p.AssertHTML(".todo-list li", 2)
	p.AssertHTML(".count", 1)
	p.AssertHTML("footer button[on:click=clear_completed]", 0) // nothing done yet
	p.AssertHTML(`a[href="/todos/`+first+`"]`, 1)
	p.AssertAccessible()
	if p.Title() != "Todos (2)" {
		t.Errorf("title %q", p.Title())
	}

	p.Event("add_todo", loom.Payload{"title": "Buy milk"})
	p.Event("add_todo", loom.Payload{"title": "   "}) // blank titles are refused
	p.AssertDiff("")
	p.Event("toggle_todo", loom.Payload{"id": first, "checked": "true"})
	p.Event("delete_todo", loom.Payload{"id": second})
	milk := idOf(t, todos, "Buy milk")
	p.AssertHTML(".todo-list li", 2)
	p.AssertHTML(`li[data-loom-key="`+second+`"]`, 0)
	p.AssertHTML("li.done input[checked]", 1)
	p.AssertHTML("footer button[on:click=clear_completed]", 1)
	p.AssertText(".count", "1 item left")
	if p.Title() != "Todos (1)" {
		t.Errorf("title %q", p.Title())
	}

	// The filter comes from the URL.
	active := loomtest.Mount(t, app, "/?filter=active", loomtest.NoLayout())
	active.AssertHTML(".todo-list li", 1)
	active.AssertHTML(`li[data-loom-key="`+milk+`"]`, 1)
	active.AssertHTML(`.filters a.current[href="/?filter=active"]`, 1)
	// The filter links patch the page: no page load, the same list.
	active.Follow(`.filters a[href="/?filter=done"]`)
	active.AssertHTML(".todo-list li", 1)
	active.AssertHTML(`li[data-loom-key="`+first+`"]`, 1)
	active.Follow(`.filters a[href="/?filter=active"]`)
	active.AssertHTML(`li[data-loom-key="`+milk+`"]`, 1)

	// Clear completed removes done todos.
	p.Event("clear_completed", loom.Payload{})
	p.AssertHTML(".todo-list li", 1)
	p.AssertHTML(`li[data-loom-key="`+first+`"]`, 0)
	p.AssertHTML("footer button[on:click=clear_completed]", 0)

	// The other page watches the todos table, so it shows the change.
	active.TableChanged("todos")
	active.AssertHTML(".todo-list li", 1)
	p.Event("add_todo", loom.Payload{"title": "Call Ada"})
	active.TableChanged("todos")
	active.AssertHTML(".todo-list li", 2)
}

func TestTodoDetail(t *testing.T) {
	app, _ := testApp(t, jobs.RealClock{})
	p := loomtest.Mount(t, app, "/todos/"+first, loomtest.NoLayout())
	p.AssertText("h1", "Try Loom")
	p.AssertText(".state", "To do")
	p.AssertAccessible()
	p.Event("toggle_todo", loom.Payload{})
	p.AssertText(".state", "Done")
	// Another user's todo, or none, is not found.
	for _, path := range []string{"/todos/" + first + "?as=Ada", "/todos/99", "/todos/00000000-0000-0000-0000-000000000099"} {
		opts := []loomtest.Option{loomtest.NoLayout()}
		if strings.HasSuffix(path, "Ada") {
			opts = append(opts, loomtest.Cookie("user", "Ada"))
		}
		loomtest.Mount(t, app, path, opts...).AssertText(".empty", "No such todo")
	}
}

func TestPruneCompleted(t *testing.T) {
	db := loomtest.DB(t)
	q := loomtest.Jobs(t, loomtest.UseDB(db))
	todos := &Todos{DB: db, Clock: q.Clock()}
	if err := todos.Add(ctx, "Guest", "Write docs/jobs.md"); err != nil {
		t.Fatal(err)
	}
	q.Jobs(PruneCompleted{Todos: todos})
	q.Cron("0 3 * * *", PruneCompleted{})
	titles := func() string {
		list, err := todos.List(ctx, "Guest")
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, todo := range list {
			out = append(out, todo.Title)
		}
		return strings.Join(out, ",")
	}

	toggle(t, todos, first) // done now
	q.Advance(6 * 24 * time.Hour)
	toggle(t, todos, second) // done six days later
	// The daily job ran six times; nothing was done a week ago yet.
	if n := q.Drain(t); n != 6 {
		t.Fatalf("ran %d jobs, want 6", n)
	}
	q.AssertCompleted(t, PruneCompleted{})
	if got := titles(); got != "Try Loom,Read docs/syntax.md,Write docs/jobs.md" {
		t.Fatalf("after six days: %s", got)
	}

	// Two days later, the first todo has been done for over a week.
	q.Advance(2 * 24 * time.Hour)
	q.Drain(t)
	if got := titles(); got != "Read docs/syntax.md,Write docs/jobs.md" {
		t.Fatalf("after eight days: %s", got)
	}
}

func TestPruneCompletedNow(t *testing.T) {
	_, todos := testApp(t, jobs.RealClock{})
	toggle(t, todos, first)
	if err := jobs.PerformNow(t, PruneCompleted{Todos: todos}); err != nil {
		t.Fatal(err)
	}
	if list, _ := todos.List(ctx, "Guest"); len(list) != 2 {
		t.Fatalf("%d todos, want 2: a todo done just now isn't pruned", len(list))
	}
}

// TestCommitted runs the app on a database of its own, where changes
// really commit, so a new app (as after a restart) sees them.
func TestCommitted(t *testing.T) {
	db := loomtest.DB(t, loomtest.Committed())
	app := loom.New()
	app.UseDB(db)
	Setup(app)
	loomtest.Mount(t, app, "/").Event("add_todo", loom.Payload{"title": "Kept"})

	restarted := loom.New()
	restarted.UseDB(db)
	Setup(restarted)
	c := loomtest.Mount(t, restarted, "/")
	if !strings.Contains(c.HTML(), "Kept") {
		t.Fatalf("after a restart:\n%s", c.HTML())
	}
}

func TestLayouts(t *testing.T) {
	app := loom.New()
	app.UseDB(loomtest.DB(t))
	Setup(app)
	p := loomtest.Mount(t, app, "/")
	p.AssertHTML("nav.topnav .user", 1) // the app layout, in the live tree
	loomtest.AssertAccessible(t, p.Document())
	p.AssertHTML("main .todo-list li", 2)
	doc := p.Document()
	for sel, n := range map[string]int{
		"html head title":            1,
		"title[data-default=Todos]":  1,
		"body #loom-root nav.topnav": 1,
		"body script#loom-tree":      1,
		"body script[defer]":         2, // loom.js and navigate.js
	} {
		if got, _ := loomtest.Count(doc, sel); got != n {
			t.Errorf("%q matches %d elements, want %d, in:\n%s", sel, got, n, doc)
		}
	}

	// The about page is outside the group: root layout only.
	about := loomtest.Mount(t, app, "/about")
	about.AssertHTML("nav.topnav", 0)
	about.AssertHTML(".about h1", 1)
	if n, _ := loomtest.Count(about.Document(), "head title"); n != 1 || about.Title() != "About" {
		t.Errorf("about: title %q in:\n%s", about.Title(), about.Document())
	}
}

// TestJobsDashboard checks that only the Admin user gets the jobs
// dashboard, and that it shows the prune job's cron entry and runs it.
func TestJobsDashboard(t *testing.T) {
	app := loom.New()
	app.UseDB(loomtest.DB(t))
	Setup(app)
	if _, err := app.NewView(httptest.NewRequest("GET", "/admin/jobs", nil), "test", true); !errors.Is(err, loom.ErrForbidden) {
		t.Fatalf("as Guest: %v, want ErrForbidden", err)
	}
	p := loomtest.Mount(t, app, "/admin/jobs/cron", loomtest.Cookie("user", "Admin"))
	p.AssertHTML("tbody tr", 2) // PruneCompleted and the built-in prune
	if !strings.Contains(p.HTML(), "todo.PruneCompleted") {
		t.Fatalf("no PruneCompleted entry:\n%s", p.HTML())
	}
	p.Event("run_now", loom.Payload{"index": "1"}) // 0 is the built-in prune
	if !strings.Contains(p.HTML(), "Enqueued job") {
		t.Fatalf("after run now:\n%s", p.HTML())
	}
	recs, err := app.JobRunner().Jobs(ctx, jobs.Filter{})
	if err != nil || len(recs) != 1 || recs[0].Type != "todo.PruneCompleted" {
		t.Fatalf("jobs %v, %v", recs, err)
	}
}
