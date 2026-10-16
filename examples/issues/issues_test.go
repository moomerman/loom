package issues

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/loomtest"
)

// The tests run on a Postgres database built from migrations/, with the
// example data: Ada, Grace, Alan and Margaret in Engineering (22
// issues), Ada, Katherine and Margaret in Design (8). Without
// LOOM_TEST_DATABASE_URL they skip.

var ctx = context.Background()

// testSecret is the app secret in tests: auth encrypts two-factor
// secrets with it.
const testSecret = "a-test-secret-of-at-least-32-chars"

// setup returns an app on a test database rolled back at the end, with
// a job queue that the app's enqueues land in and a mailbox.
func setup(t *testing.T) (*loom.App, *Store, *loomtest.JobQueue) {
	db := loomtest.DB(t)
	q := loomtest.Jobs(t, loomtest.UseDB(db))
	store := &Store{DB: db, PubSub: q.PubSub(), Clock: jobs.RealClock{}}
	app := loom.New(loom.Config{BaseURL: "https://issues.test", Secret: testSecret})
	app.UseDB(db)
	q.Attach(app) // the app's jobs, auth's mail among them, run in the test's queue
	loomtest.Mail(t, app)
	routes(app, store)
	return app, store, q
}

// as mounts the page at path for a signed-in user.
func as(t *testing.T, app *loom.App, user, path string) *loomtest.Page {
	return loomtest.Mount(t, app, path, loomtest.SignIn(t, app, user))
}

// Signing in is the auth sub-app's: sent there with the page asked for,
// the example's people sign in with the demo password, and someone new
// who signs up joins the workspace.
func TestSignIn(t *testing.T) {
	app, store, _ := setup(t)
	srv := httptest.NewServer(app)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	res, err := client.Get(srv.URL + "/team/ENG")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/auth/sign-in?next=%2Fteam%2FENG"; res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != want {
		t.Errorf("signed out: %d %s, want a redirect to %s", res.StatusCode, res.Header.Get("Location"), want)
	}

	p := loomtest.Mount(t, app, "/auth/sign-in?next=%2Fteam%2FENG")
	p.AssertText(".app", "Loom Issues")
	p.AssertAccessible()
	p.Submit("form", loom.Payload{"email": "grace@example.com", "password": "loom-issues"})
	if nav := p.Navigated(); !strings.HasPrefix(nav, "/_loom/signin/") || !strings.HasSuffix(nav, "?next=%2Fteam%2FENG") {
		t.Errorf("signing in went to %q", nav)
	}

	p = loomtest.Mount(t, app, "/auth/sign-up")
	p.Submit("form", loom.Payload{"email": "hedy@example.com", "password": "frequency hopping"})
	if !strings.HasPrefix(p.Navigated(), "/_loom/signin/") {
		t.Fatalf("signing up went to %q", p.Navigated())
	}
	ids, err := loom.Select[string](ctx, store.DB, `SELECT id FROM users WHERE email = 'hedy@example.com'`)
	if err != nil || len(ids) != 1 {
		t.Fatalf("no users row for the new account: %v %v", ids, err)
	}
	as(t, app, ids[0], "/team/DES").AssertText(".list", "")
}

func TestTeamPage(t *testing.T) {
	app, _, _ := setup(t)
	p := as(t, app, "ada", "/team/ENG")
	if p.Title() != "Engineering" {
		t.Errorf("title %q", p.Title())
	}
	p.AssertHTML(".list a[data-row]", 22)
	p.AssertHTML(".list section", 6) // every status has issues
	p.AssertAccessible()

	// The view tabs are links to the page's own route, so following one
	// patches the URL and the page, without a page load.
	p.Follow(`[role=tab][href="/team/ENG?view=backlog"]`)
	p.AssertHTML(".list a[data-row]", 5)
	p.AssertHTML(".list section", 1)
	p.AssertHTML("[role=tab][aria-selected=true]", 1)

	p.Follow(`[role=tab][href="/team/ENG?view=active"]`)
	p.AssertHTML(".list a[data-row]", 11) // 6 todo, 3 in progress, 2 in review

	p.Event("menu", loom.Payload{"menu": "priority"})
	p.AssertHTML("[role=listbox] [role=option]", 6)
	p.AssertAccessible()
	// Clicking another menu's button opens it, and then the first one
	// hears the click outside it and closes: the second stays open.
	p.Event("menu", loom.Payload{"menu": "label"})
	p.Event("menu_close", loom.Payload{"menu": "priority"})
	p.AssertHTML("[role=listbox][aria-label=Label]", 1)
	p.Event("menu", loom.Payload{"menu": "priority"})
	p.Event("pick", loom.Payload{"menu": "priority", "value": "1"})
	p.AssertHTML("[role=listbox]", 0)
	p.AssertHTML(".list a[data-row]", 3) // the urgent active issues
	p.AssertHTML(".clear", 1)
	if got := p.URL(); got != "/team/ENG?priority=1&view=active" {
		t.Errorf("after picking a priority the URL is %q", got)
	}
	// The tabs keep the filters.
	p.AssertHTML(`[role=tab][href="/team/ENG?priority=1&view=backlog"]`, 1)

	p.Event("menu", loom.Payload{"menu": "assignee"}) // open it, as a person would
	p.Event("pick", loom.Payload{"menu": "assignee", "value": "ada"})
	p.AssertHTML(".list a[data-row]", 1)
	p.AssertHTML(`a[href="/issue/ENG-21"]`, 1)

	// The URL holds the filters, so loading it shows the same list.
	shared := as(t, app, "ada", p.URL())
	shared.AssertHTML(".list a[data-row]", 1)
	shared.AssertHTML(`a[href="/issue/ENG-21"]`, 1)
	// And going back undoes the last one.
	p.Patch("/team/ENG?priority=1&view=active")
	p.AssertHTML(".list a[data-row]", 3)

	p.Event("clear_filters", nil)
	p.AssertHTML(".list a[data-row]", 11)
	if got := p.URL(); got != "/team/ENG?view=active" {
		t.Errorf("after clearing the filters the URL is %q", got)
	}
	p.Event("search", loom.Payload{"value": "notify"})
	p.AssertHTML(".list a[data-row]", 1)
	if got := p.URL(); got != "/team/ENG?q=notify&view=active" {
		t.Errorf("after a search the URL is %q", got)
	}
	p.Event("search", loom.Payload{"value": "eng-21"})
	p.AssertHTML(`a[href="/issue/ENG-21"]`, 1)
	p.Event("search", loom.Payload{"value": "nothing like this"})
	p.AssertHTML(".list a[data-row]", 0)
	if !strings.Contains(p.HTML(), "No issues match") {
		t.Error("no empty state for a search that finds nothing")
	}
}

// A team's link in the sidebar goes to another path value of the same
// route, so it mounts the team's page afresh rather than patching the
// one showing: Design's page once stayed on Design's issues.
func TestSwitchingTeams(t *testing.T) {
	app, _, _ := setup(t)
	p := as(t, app, "ada", "/team/DES")
	p.AssertHTML(".list a[data-row]", 8)
	p.Follow(`nav[aria-label=Teams] a[href="/team/ENG"]`)
	if got := p.Navigated(); got != "/team/ENG" {
		t.Fatalf("Navigated() = %q; want /team/ENG", got)
	}
	if got := p.URL(); got != "/team/DES" {
		t.Errorf("URL() = %q; the Design page shouldn't patch to /team/ENG", got)
	}
}

func TestTeamsAreScoped(t *testing.T) {
	app, _, _ := setup(t)
	// Grace isn't in Design: its page, its issues and its labels are
	// hidden from her, even with a hand-written event.
	p := as(t, app, "grace", "/team/DES")
	if !strings.Contains(p.HTML(), "Team not found") {
		t.Errorf("Grace sees Design:\n%s", p.HTML())
	}
	p.Patch("/team/DES?view=backlog")
	p.AssertDiff("")

	p = as(t, app, "grace", "/issue/DES-1")
	if !strings.Contains(p.HTML(), "Issue not found") {
		t.Errorf("Grace sees DES-1")
	}
	p.AssertRefused("pick", loom.Payload{"menu": "status", "value": "done"})

	p = as(t, app, "grace", "/")
	p.AssertHTML(`nav[aria-label=Teams] a`, 1)
}

func TestCompose(t *testing.T) {
	app, store, q := setup(t)
	p := as(t, app, "ada", "/team/ENG")
	p.Event("compose", loom.Payload{"team": "1", "status": "in_progress"})
	p.AssertHTML("form[role=dialog]", 1)
	p.AssertAccessible()

	p.Event("compose_submit", loom.Payload{"title": "  ", "description": ""})
	p.AssertHTML("form[role=dialog] .error", 1)

	p.Event("compose_menu", loom.Payload{"menu": "assignee"})
	p.AssertHTML("form[role=dialog] [role=option]", 5) // nobody, and the four members
	p.Event("compose_pick", loom.Payload{"menu": "assignee", "value": "grace"})
	p.Event("compose_menu", loom.Payload{"menu": "priority"}) // open it, as a person would
	p.Event("compose_pick", loom.Payload{"menu": "priority", "value": "2"})
	p.Event("compose_menu", loom.Payload{"menu": "labels"}) // open it, as a person would
	p.Event("compose_pick", loom.Payload{"menu": "labels", "value": idOfLabel(t, store, "ENG", "Bug")})
	p.Event("compose_submit", loom.Payload{"title": "Toasts overlap the composer", "description": "On small screens."})
	p.AssertHTML("form[role=dialog]", 0)
	if !strings.Contains(p.HTML(), "Created ENG-23") {
		t.Errorf("no toast:\n%s", p.HTML())
	}
	p.AssertHTML(".list a[data-row]", 23)
	p.AssertHTML(`a[href="/issue/ENG-23"]`, 2) // the row and the toast's link

	issue, err := store.Issue(ctx, "ada", "ENG-23")
	if err != nil {
		t.Fatal(err)
	}
	if issue.Status != "in_progress" || issue.Priority != 2 || issue.Assignee.ID != "grace" || len(issue.Labels) != 1 || issue.Description != "On small screens." {
		t.Errorf("created %+v", issue)
	}

	// Grace hears of it in her inbox, from the Notify job enqueued in
	// the issue's transaction.
	q.AssertEnqueued(t, Notify{UserID: "grace", IssueID: issue.ID, ActorID: "ada", Kind: "assigned"})
	inbox := as(t, app, "grace", "/inbox")
	inbox.Listen(q.PubSub())
	inbox.AssertHTML(".unread", 0)
	q.Drain(t)
	inbox.AssertHTML("a.unread", 1)
	inbox.AssertHTML("nav .unread", 1) // the sidebar's count
	inbox.AssertAccessible()

	// Opening the issue marks it read.
	as(t, app, "grace", "/issue/ENG-23")
	inbox.AssertHTML("a.unread", 0)
}

func TestNotifyRunsOnce(t *testing.T) {
	_, store, _ := setup(t)
	id := issueID(t, store, "ENG-1")
	job := Notify{UserID: "ada", IssueID: id, ActorID: "grace", Kind: "commented", Store: store}
	// PerformNow gives every run the same job id, as a retry would have.
	for range 2 {
		if err := jobs.PerformNow(t, job); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := store.Unread(ctx, "ada"); err != nil || n != 1 {
		t.Errorf("%d notifications (%v), want 1", n, err)
	}
}

func TestIssuePage(t *testing.T) {
	app, store, q := setup(t)
	p := as(t, app, "ada", "/issue/ENG-19")
	if p.Title() != "ENG-19 Pull protocol for shapes" {
		t.Errorf("title %q", p.Title())
	}
	p.AssertAccessible()

	// Another of Ada's pages, the team's list, follows the changes.
	list := as(t, app, "ada", "/team/ENG")
	list.Listen(q.PubSub())
	list.Follow(`[role=tab][href="/team/ENG?view=active"]`)
	list.AssertHTML(".list a[data-row]", 11)

	p.Event("menu", loom.Payload{"menu": "status"})
	p.AssertHTML("[role=listbox][aria-label=Status] [role=option]", 6)
	p.Event("pick", loom.Payload{"menu": "status", "value": "done"})
	p.AssertHTML("[role=listbox]", 0)
	if !strings.Contains(p.HTML(), "changed status from Todo to Done") {
		t.Errorf("no history entry:\n%s", p.HTML())
	}
	list.AssertHTML(".list a[data-row]", 10)

	p.Event("menu", loom.Payload{"menu": "assignee"}) // open it, as a person would
	p.Event("pick", loom.Payload{"menu": "assignee", "value": "margaret"})
	if !strings.Contains(p.HTML(), "reassigned the issue from Ada Lovelace to Margaret Hamilton") {
		t.Error("no history entry for the assignee")
	}
	q.AssertEnqueued(t, Notify{UserID: "margaret", Kind: "assigned"})

	// Labels toggle, and the menu stays open.
	docs := idOfLabel(t, store, "ENG", "Docs")
	p.Event("menu", loom.Payload{"menu": "labels"})
	p.Event("pick", loom.Payload{"menu": "labels", "value": docs})
	p.AssertHTML("[role=listbox][aria-label=Labels]", 1)
	p.AssertHTML("aside .chips .chip", 2)
	p.Event("pick", loom.Payload{"menu": "labels", "value": docs})
	p.AssertHTML("aside .chips .chip", 1)

	p.Event("set", loom.Payload{"field": "title", "value": "Pull protocol for shapes, paginated"})
	if p.Title() != "ENG-19 Pull protocol for shapes, paginated" {
		t.Errorf("title %q", p.Title())
	}
	p.Event("set", loom.Payload{"field": "title", "value": "   "})
	p.AssertDiff("") // an empty title is ignored

	p.Event("comment", loom.Payload{"body": "Starting with the pagination tests."})
	p.AssertHTML("ol .comment", 1)
	q.AssertEnqueued(t, Notify{UserID: "margaret", Kind: "commented"})

	p.Event("set", loom.Payload{"field": "description", "value": "Upserts and removes, paged."})
	if !strings.Contains(p.HTML(), "Upserts and removes, paged.</textarea>") {
		t.Error("description not saved")
	}
}

func TestShortcutsAndLayout(t *testing.T) {
	app, _, _ := setup(t)
	p := as(t, app, "ada", "/")
	p.AssertHTML("[data-hotkey=c]", 1)
	p.AssertHTML(`[data-hotkey="g 1"]`, 1)
	p.AssertHTML(`[data-hotkey="g 2"]`, 1)
	p.Event("help", nil)
	p.AssertHTML("[role=dialog][aria-label='Keyboard shortcuts']", 1)
	p.AssertAccessible()
	p.Event("help_close", nil)
	p.AssertHTML("[role=dialog]", 0)

	p.Follow(`[role=tab][href="/?view=created"]`)
	p.AssertHTML(".list a[data-row]", 10)
}

func issueID(t *testing.T, s *Store, identifier string) int {
	t.Helper()
	issue, err := s.Issue(ctx, "ada", identifier)
	if err != nil {
		t.Fatal(err)
	}
	return issue.ID
}

func idOfLabel(t *testing.T, s *Store, team, name string) string {
	t.Helper()
	id, err := loom.Get[string](ctx, s.DB, `SELECT l.id::text FROM labels l JOIN teams t ON t.id = l.team_id WHERE t.key = $1 AND l.name = $2`, team, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
