package loom_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/internal/subapptest"
	"github.com/moomerman/loom/jobs"
)

type listPage struct{}

func (*listPage) Mount(c *loom.Ctx)                          {}
func (*listPage) Assigns(c *loom.Ctx) hxtest.TodoListAssigns { return hxtest.TodoListAssigns{} }
func (*listPage) Events() loom.Events {
	return loom.Events{"toggle": func(*loom.Ctx, loom.Payload) {}}
}

type remind struct {
	UserID string   `json:"user_id"`
	DB     *loom.DB `json:"-"`
}

func (remind) Options() jobs.Options { return jobs.Options{Queue: "mail"} }

func (remind) Perform(ctx context.Context, c *jobs.Ctx) error { return nil }

// The manifest lists routes with the events their templates send, the
// tables they watch, handlers, mounts and jobs, and flags what a reviewer should
// look at, without a database.
func TestManifest(t *testing.T) {
	app := loom.New()
	app.Live("/list", &listPage{}, hxtest.TodoList)
	signedIn := app.Group(loom.Auth(allowAll), loom.Layout(hxtest.Shell, func(c *loom.Ctx) hxtest.ShellAssigns { return hxtest.ShellAssigns{} }))
	signedIn.Live("/items/{page}", &listPage{}, hxtest.TodoList, loom.Watch("items", "notes"))
	signedIn.Handle("POST /logout", http.NotFoundHandler())
	app.Group(loom.Auth(allowAll)).Mount("/admin", handlerApp{})
	app.Queues(jobs.Queue{Name: "mail", Concurrency: 2})
	app.Jobs(remind{})
	app.Cron("0 8 * * *", remind{UserID: "ana"})

	m := app.Manifest()
	if len(m.Routes) != 2 || m.Routes[0].Path != "/list" || len(m.Routes[1].Watch) != 2 {
		t.Fatalf("routes: %+v", m.Routes)
	}
	if ev := m.Routes[0].Events; len(ev) != 1 || ev[0].Name != "toggle" || ev[0].Vals[0] != "id" || ev[0].At[0] != "todo_list.hx:5:45" {
		t.Errorf("live route events: %+v", ev)
	}
	if got := m.Jobs.Types[0]; got.Type != "loom_test.remind" || got.Queue != "mail" || strings.Join(got.Args, ",") != "user_id" {
		t.Errorf("job type: %+v", got)
	}
	text := m.Text()
	for _, want := range []string{
		"  /list  live page hxtest.TodoList\n    auth: none\n    events, handled by (*loom_test.listPage).Events:\n      toggle (on change, val-id)\n",
		"  /items/{page}  live page hxtest.TodoList in hxtest.Shell\n    auth: loom_test.allowAll\n",
		"    re-renders when these tables change: items, notes\n",
		"  POST /logout  auth: loom_test.allowAll\n",
		"  /admin  loom_test.handlerApp\n    auth: loom_test.allowAll\n    routes: GET /ping\n",
		"  loom_test.remind  queue mail, 20 attempts, timeout 1m0s, args: user_id\n",
		"  cron \"0 8 * * *\"  loom_test.remind {\"user_id\":\"ana\"}\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("manifest lacks\n%s\nin:\n%s", want, text)
		}
	}
}

// Migrations are summarised by their statements: schema changes line by
// line, row changes folded into one line, and semicolons in strings and
// function bodies don't split statements.
func TestManifestMigrations(t *testing.T) {
	m := loom.New().Manifest()
	err := m.AddMigrations(fstest.MapFS{
		"0001_items.sql": {Data: []byte("-- items\nCREATE TABLE items (\n  id bigint PRIMARY KEY,\n  title text\n);\n-- loom:track items\n" +
			"CREATE FUNCTION f() RETURNS trigger AS $$ BEGIN RETURN NEW; END; $$ LANGUAGE plpgsql;\n")},
		"0002_seed.sql": {Data: []byte("INSERT INTO items VALUES (1, 'a;b');\nUPDATE items SET title = 'c';\nALTER TABLE items ADD COLUMN due_on date;\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := m.Text()
	want := "database (migrations)\n\n" +
		"  0001_items.sql  tracks items\n    CREATE TABLE items\n    CREATE FUNCTION f() RETURNS trigger AS $$ BEGIN RETURN NEW; END; $$ LANGUAGE ...\n" +
		"  0002_seed.sql\n    ALTER TABLE items ADD COLUMN due_on date\n    2 statements changing rows of items\n"
	if !strings.Contains(text, want) {
		t.Errorf("manifest lacks\n%s\nin:\n%s", want, text)
	}
}

// A sub-app mounted with no auth check is flagged for review, unless it
// says it is public by design (RequiresAuth false), as a sign-in page is.
func TestManifestPublicMount(t *testing.T) {
	app := quietApp()
	app.Mount("/open", handlerApp{})
	app.Mount("/public", &subapptest.App{NeedsAuth: false})
	var subjects []string
	for _, f := range app.Manifest().Flags {
		subjects = append(subjects, f.Subject)
	}
	if strings.Join(subjects, ",") != "mount /open" {
		t.Errorf("flags %v, want only mount /open", subjects)
	}
}
