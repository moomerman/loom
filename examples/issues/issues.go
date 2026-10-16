// Package issues is a Linear-style issue tracker, the larger of Loom's
// example apps: teams with issue lists grouped by status, filters,
// search, an issue page with editable properties, comments and history,
// a new-issue composer, an inbox fed by a background job, and keyboard
// shortcuts. It is written to look and feel like a real product, and to
// use everything Loom has: layouts, the starter kit and design tokens,
// typed behaviors, the data layer (transactions, AfterCommit), jobs, the
// jobs dashboard, and sign-in with the auth sub-app.
//
// Run it, then sign in as ada@example.com with the password loom-issues:
//
//	DATABASE_URL=postgres://postgres:postgres@localhost:5432/issues go run ./cmd/loom migrate --dir examples/issues/migrations
//	DATABASE_URL=postgres://postgres:postgres@localhost:5432/issues go run ./examples/issues/cmd/issues
package issues

//go:generate go run github.com/moomerman/loom/cmd/loom build . ./ui

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/auth"
	"github.com/moomerman/loom/devmail"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/jobsweb"
)

// App is the issue tracker: its store, shared by every page.
type App struct {
	store *Store
}

// files holds the favicon, linked with asset("icon.svg").
//
//go:embed assets
var files embed.FS

// Setup adds the tracker's routes and jobs to app, storing everything
// in app.DB(), a Postgres database migrated with migrations/. Mailed
// sign-in links point to the app's BaseURL, and the app needs a mailer
// (app.UseMailer) unless it runs under `loom dev`:
//
//	app := loom.New(loom.Config{DB: os.Getenv("DATABASE_URL"), BaseURL: "http://localhost:4000"})
//	app.UseMailer(issues.LogMailer{Logger: app.Logger()})
//	issues.Setup(app)
func Setup(app *loom.App) {
	routes(app, &Store{DB: app.DB(), PubSub: app.PubSub(), Clock: jobs.RealClock{}})
}

// routes adds the routes and jobs on a store. Tests call it with their
// own store.
func routes(app *loom.App, store *Store) {
	a := &App{store: store}
	assets, err := fs.Sub(files, "assets")
	if err != nil {
		panic(err)
	}
	site := app.Group(
		loom.Assets(assets),
		loom.RootLayout(Root, func(c *loom.Ctx) RootAssigns {
			return RootAssigns{Title: c.TitleOr("")}
		}),
	)

	// Sign-in is the auth sub-app, at /auth: its pages take this root
	// layout and tokens.toml, so they look like the tracker. Its
	// accounts share their ids with the users table: migration 0004
	// gives the example's people accounts, and someone new who signs up
	// gets a users row in the same transaction (joinWorkspace).
	loom.Users(app, store.User)
	site.Mount("/auth", auth.New(auth.Config{
		AppName:  "Loom Issues",
		OnSignUp: store.joinWorkspace,
		// Google and GitHub, when the app is registered with them (see
		// docs/auth.md); without the variables, passwords and links only.
		Providers: providersFromEnv(),
	}))

	// Signed-in pages: loom.SignedIn sends everyone else to the sign-in
	// page, on the page load and before every event, and brings them
	// back after.
	signedIn := site.Group(
		loom.SignedIn("/auth/sign-in"),
		loom.LiveLayout(Shell, &chrome{app: a}),
	)
	signedIn.Live("/", &MyIssuesPage{app: a}, MyIssues)
	signedIn.Live("/inbox", &InboxPage{app: a}, Inbox)
	signedIn.Live("/team/{key}", &TeamPage{app: a}, TeamIssues)
	signedIn.Live("/issue/{id}", &IssuePage{app: a}, IssueDetail)

	app.Jobs(Notify{Store: store})
	app.Queues(jobs.Queue{Name: "notifications", Concurrency: 5}, jobs.Queue{Name: "default", Concurrency: 10})

	// The jobs dashboard, for Ada, who runs the workspace.
	admin := app.Group(loom.SignedIn("/auth/sign-in"), loom.Auth(requireAdmin))
	admin.Mount("/admin/jobs", jobsweb.New(jobsweb.Config{}))

	// Under loom dev the app keeps the mail it sends, sign-in links
	// included, and the mailbox shows it at /dev/mail. Outside dev the
	// mailbox answers 404, so the line can stay.
	app.Mount("/dev/mail", devmail.New())
}

// requireAdmin lets in Ada, the workspace's admin.
func requireAdmin(c *loom.Ctx) error {
	if c.UserID() != "ada" {
		return loom.ErrForbidden
	}
	return nil
}

// joinWorkspace makes a users row for someone who signs up, named from
// their email, and adds them to every team: a small workspace where
// everyone sees everything. auth runs it in the sign-up transaction.
func (s *Store) joinWorkspace(ctx context.Context, tx loom.Tx, u auth.User) error {
	name, _, _ := strings.Cut(u.Email, "@")
	if _, err := tx.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1, $2, $3)`, u.ID, name, u.Email); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO team_members (team_id, user_id) SELECT id, $1 FROM teams`, u.ID)
	return err
}

// atoi parses an id from a payload, 0 if it isn't one.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// must panics on an error other than ErrNotFound. A database error in
// Mount or an event handler panics, which the runtime reports (RT402) and recovers
// from by remounting the page.
func must(err error) {
	if err != nil && !errors.Is(err, ErrNotFound) {
		panic(err)
	}
}

// providersFromEnv is Google and GitHub sign-in for the providers whose
// GOOGLE_CLIENT_ID or GITHUB_CLIENT_ID (and _CLIENT_SECRET) are set.
func providersFromEnv() []auth.Provider {
	var ps []auth.Provider
	if id := os.Getenv("GOOGLE_CLIENT_ID"); id != "" {
		ps = append(ps, auth.Google(id, os.Getenv("GOOGLE_CLIENT_SECRET")))
	}
	if id := os.Getenv("GITHUB_CLIENT_ID"); id != "" {
		ps = append(ps, auth.GitHub(id, os.Getenv("GITHUB_CLIENT_SECRET")))
	}
	return ps
}
