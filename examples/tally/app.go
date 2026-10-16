// Package tally is Tally, a shared-expenses app: groups of people who
// split bills, see who owes whom, settle up, and get reminded. Each page
// is a Go file (state, Mount, Assigns, Events) next to the .hx file it
// renders; `loom build` compiles the .hx files to *_hx.go.
//
// It was built by an agent using Loom as a newcomer would, from llms.txt,
// `loom new` and the CLI; docs/example-tally.md says what it shows.
//
// Run it:
//
//	DATABASE_URL=postgres://postgres:postgres@localhost:5432/tally go run ./cmd/loom migrate --dir examples/tally/migrations
//	DATABASE_URL=postgres://postgres:postgres@localhost:5432/tally go run ./examples/tally/cmd/tally
package tally

//go:generate go run github.com/moomerman/loom/cmd/loom build . ./components

import (
	"net/http"
	"strconv"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/jobsweb"
)

// App is Tally: its store, shared by every page, and the Loom app.
type App struct {
	store *Store
	app   *loom.App
}

// The tables a signed-in page reads, through its own content or the
// sidebar's balances and unread count. A change to any of them, from
// any page, job or node, re-renders the page.
var watched = loom.Watch("groups", "members", "expenses", "shares", "settlements", "recurring", "notifications")

// Setup adds Tally's routes and jobs to app. It needs a Postgres
// database migrated with migrations/.
func Setup(app *loom.App) {
	routes(app, &Store{DB: app.DB()})
}

// routes adds the routes and jobs on a store. Tests call it with their own.
func routes(app *loom.App, store *Store) {
	a := &App{store: store, app: app}
	// Who is signed in is Loom's (loom_user_sessions); who they are is
	// Tally's: pages read the signed-in Person with loom.User[Person].
	loom.Users(app, store.Person)
	site := app.Group(loom.RootLayout(Root, func(c *loom.Ctx) RootAssigns {
		return RootAssigns{Title: c.TitleOr("")}
	}))
	site.Live("/login", &LoginPage{app: a}, Login)
	site.Handle("POST /login", http.HandlerFunc(a.login))
	site.Handle("POST /logout", http.HandlerFunc(a.logout))

	// Signed-in pages: everyone else goes to /login, on the page load and
	// before every event, so signing out ends every open page.
	signedIn := site.Group(
		loom.SignedIn("/login"),
		loom.Layout(Shell, a.shell),
	)
	signedIn.Live("/", &DashboardPage{app: a}, Dashboard, watched)
	signedIn.Live("/activity", &ActivityPage{app: a}, Activity, watched)
	signedIn.Live("/groups/{id}", &GroupPage{app: a}, GroupView, watched)
	signedIn.Live("/groups/{id}/expenses/{eid}", &ExpensePage{app: a}, ExpenseView, watched)

	app.Jobs(Notify{Store: store}, Remind{Store: store}, PostRecurring{Store: store})
	app.Queues(jobs.Queue{Name: "notifications", Concurrency: 5})
	app.Cron("0 6 * * *", PostRecurring{Store: store})

	// The jobs dashboard, for Sam, who runs this Tally.
	admin := app.Group(loom.SignedIn("/login"), loom.Auth(requireAdmin))
	admin.Mount("/admin/jobs", jobsweb.New(jobsweb.Config{}))
}

// admin is the one person who sees the jobs dashboard.
const admin = "sam"

// requireAdmin lets in the admin. It runs after loom.SignedIn.
func requireAdmin(c *loom.Ctx) error {
	if c.UserID() != admin {
		return loom.ErrForbidden
	}
	return nil
}

// me returns the person signed in on the page's request.
func (a *App) me(c *loom.Ctx) (Person, error) {
	return loom.User[Person](c), nil
}

// login signs in as the person the form names (there are no passwords
// in this app) and goes to the page they asked for, or their dashboard.
// It is a plain handler, a form post; a live page would call c.SignIn.
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("person")
	if _, err := a.store.Person(r.Context(), id); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := a.app.SignIn(w, r, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// logout signs out: the session ends, so every open page of it ends at
// its next event.
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.app.SignOut(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// shell returns the app layout's assigns: the sidebar's groups with
// the person's balance in each, and the unread count. It runs on every
// render, so a watched table's change updates the sidebar too. The page
// says which sidebar item is current with c.Set("section", ...).
func (a *App) shell(c *loom.Ctx) ShellAssigns {
	me, err := a.me(c)
	must(err)
	groups, err := a.store.Groups(c.Context(), me.ID)
	must(err)
	unread, err := a.store.Unread(c.Context(), me.ID)
	must(err)
	section, _ := c.Get("section").(string)
	for i := range groups {
		groups[i].Current = section == "group:"+strconv.Itoa(groups[i].ID)
	}
	return ShellAssigns{Me: me, Groups: groups, Section: section, Unread: unread, Admin: me.ID == admin}
}

// id parses an id from a path or payload, 0 if it isn't one.
func id(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// must panics on an error. A database error in Mount, Assigns or a
// handler panics, which the runtime reports (RT402) and recovers from
// by remounting the page.
func must(err error) {
	if err != nil {
		panic(err)
	}
}
