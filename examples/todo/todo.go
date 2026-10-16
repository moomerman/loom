// Package todo is the Loom example app: a todo list where adding,
// toggling, deleting and filtering all happen on the server, and the
// browser receives small diffs. Todos are stored in Postgres, one list
// per user (from the "user" cookie). The todos table is tracked
// (-- loom:track), and the pages watch it (loom.Watch), so a change
// committed anywhere (in another tab, by the prune job, in psql, on
// another server) re-renders every open page.
// A daily background job prunes old completed todos; the user named
// Admin sees it in the jobs dashboard at /admin/jobs. Pages render in a
// root layout and an app layout with a nav bar, under a live
// "Todos (N)" title.
package todo

//go:generate go run github.com/moomerman/loom/cmd/loom build ./components ./templates

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/examples/todo/templates"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/jobsweb"
)

// TodoPage is the list: one open page's state.
type TodoPage struct {
	Todos  *Todos // set on the prototype given to app.Live
	user   string
	filter string // from the URL: /?filter=active
}

// Mount reads the user. The list itself is read by Assigns, on every
// render, so it is always the committed list.
func (p *TodoPage) Mount(c *loom.Ctx) {
	p.user = currentUser(c.Request).Name
}

// HandleParams reads the filter from the URL: after Mount, and when a
// filter link, a link to this same page, patches the URL without a page
// load.
func (p *TodoPage) HandleParams(c *loom.Ctx, query url.Values) {
	p.filter = query.Get("filter")
}

// Events are the list's events, named as the template sends them.
func (p *TodoPage) Events() loom.Events {
	return loom.Events{
		"add_todo":        p.add,
		"toggle_todo":     p.toggle,
		"delete_todo":     p.delete,
		"clear_completed": p.clearCompleted,
	}
}

func (p *TodoPage) add(c *loom.Ctx, payload loom.Payload) {
	if title := strings.TrimSpace(payload["title"]); title != "" {
		c.Fail(p.Todos.Add(c.Context(), p.user, title))
	}
}

func (p *TodoPage) toggle(c *loom.Ctx, payload loom.Payload) {
	c.Fail(p.Todos.Toggle(c.Context(), p.user, payload["id"]))
}

func (p *TodoPage) delete(c *loom.Ctx, payload loom.Payload) {
	c.Fail(p.Todos.Delete(c.Context(), p.user, payload["id"]))
}

func (p *TodoPage) clearCompleted(c *loom.Ctx, payload loom.Payload) {
	c.Fail(p.Todos.ClearCompleted(c.Context(), p.user))
}

// Assigns reads the list and sets the page's title, which the root
// layout's <.live_title> keeps up to date. A database error panics, which
// the runtime reports (RT402) and recovers from by remounting.
func (p *TodoPage) Assigns(c *loom.Ctx) templates.TodoAppAssigns {
	todos, err := p.Todos.List(c.Context(), p.user)
	if err != nil {
		panic(err)
	}
	remaining, completed := 0, 0
	for _, t := range todos {
		if t.Done {
			completed++
		} else {
			remaining++
		}
	}
	c.Title(fmt.Sprintf("Todos (%d)", remaining))
	return templates.TodoAppAssigns{Todos: todos, Filter: p.filter, Remaining: remaining, Completed: completed}
}

// TodoDetailPage is a todo's own page, at /todos/{id}.
type TodoDetailPage struct {
	Todos *Todos // set on the prototype given to app.Live
	user  string
	id    string
}

// Mount reads the user and the todo's id from the path.
func (p *TodoDetailPage) Mount(c *loom.Ctx) {
	p.user = currentUser(c.Request).Name
	p.id = c.Request.PathValue("id")
}

// Events has the page's one event.
func (p *TodoDetailPage) Events() loom.Events {
	return loom.Events{"toggle_todo": func(c *loom.Ctx, payload loom.Payload) {
		c.Fail(p.Todos.Toggle(c.Context(), p.user, p.id))
	}}
}

// Assigns reads the todo. Another user's todo, or one deleted meanwhile,
// is the zero Todo, which the page shows as not found.
func (p *TodoDetailPage) Assigns(c *loom.Ctx) templates.TodoDetailAssigns {
	todo, err := p.Todos.Get(c.Context(), p.user, p.id)
	if err != nil {
		panic(err)
	}
	c.Title(todo.Title)
	return templates.TodoDetailAssigns{Todo: todo, ID: p.id}
}

// AboutPage is a static page, outside the app layout's group.
type AboutPage struct{}

// Mount titles the page.
func (p *AboutPage) Mount(c *loom.Ctx) { c.Title("About") }

// Assigns are empty: the page is static, with no events.
func (p *AboutPage) Assigns(c *loom.Ctx) templates.AboutAssigns { return templates.AboutAssigns{} }

// currentUser returns the user named by the "user" cookie. A real app
// would look up a session here.
func currentUser(r *http.Request) templates.User {
	if c, err := r.Cookie("user"); err == nil && c.Value != "" {
		return templates.User{Name: c.Value}
	}
	return templates.User{Name: "Guest"}
}

// Setup adds the todo app's routes and jobs to app, storing todos in
// app.DB(), which must be a Postgres database migrated with migrations/:
//
//	app := loom.New(loom.Config{DB: os.Getenv("DATABASE_URL")})
//	todo.Setup(app)
//
// The todo pages are in the app layout; the about page has the root
// layout only.
func Setup(app *loom.App) {
	routes(app, &Todos{DB: app.DB(), Clock: jobs.RealClock{}})
}

// routes adds the routes and jobs on todos. Tests call it with their own
// clock.
func routes(app *loom.App, todos *Todos) {
	app.RootLayout(templates.Root, func(c *loom.Ctx) templates.RootAssigns {
		return templates.RootAssigns{Title: c.TitleOr("Todos")}
	})
	g := app.Group(loom.Layout(templates.App, func(c *loom.Ctx) templates.AppAssigns {
		return templates.AppAssigns{User: currentUser(c.Request)}
	}))
	// Both pages read the todos table, so both watch it: a change
	// committed anywhere re-renders them.
	g.Live("/", &TodoPage{Todos: todos}, templates.TodoApp, loom.Watch("todos"))
	g.Live("/todos/{id}", &TodoDetailPage{Todos: todos}, templates.TodoDetail, loom.Watch("todos"))
	app.Live("/about", &AboutPage{}, templates.About)
	app.Jobs(PruneCompleted{Todos: todos})
	app.Cron("0 3 * * *", PruneCompleted{})

	// The jobs dashboard, for admins only: jobsweb requires an auth
	// check, so mounting it without one fails at startup.
	admin := app.Group(loom.Auth(requireAdmin))
	admin.Mount("/admin/jobs", jobsweb.New(jobsweb.Config{}))
}

// requireAdmin lets in the user named Admin. It runs on page load and on
// every event, so an admin who signs out mid-session loses the dashboard
// at their next click.
func requireAdmin(c *loom.Ctx) error {
	if currentUser(c.Request).Name != "Admin" {
		return loom.ErrForbidden
	}
	return nil
}
