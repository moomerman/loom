package tally

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/loomtest"
)

// The tests run on a Postgres database built from migrations/, with the
// example data: Sam, Priya and Marcus in Flat 4B (group 1), five people
// on the Lisbon trip (2), and Sam, Diego and Marcus in the climbing club
// (3). Without LOOM_TEST_DATABASE_URL they skip.

var ctx = context.Background()

// setup returns an app on a test database rolled back at the end, with a
// job queue that the store's enqueues land in.
func setup(t *testing.T) (*loom.App, *Store, *loomtest.JobQueue) {
	db := loomtest.DB(t)
	q := loomtest.Jobs(t, loomtest.UseDB(db))
	store := &Store{DB: db, Now: func() time.Time { return time.Date(2026, 10, 11, 18, 0, 0, 0, time.UTC) }}
	app := loom.New()
	app.UseDB(db)
	q.Attach(app) // the jobs routes registers run in the test's queue
	routes(app, store)
	return app, store, q
}

// as mounts the page at path for a signed-in person.
func as(t *testing.T, app *loom.App, person, path string) *loomtest.Page {
	return loomtest.Mount(t, app, path, loomtest.SignIn(t, app, person))
}

func TestSignIn(t *testing.T) {
	app, _, _ := setup(t)
	srv := httptest.NewServer(app)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	res, err := client.Get(srv.URL + "/groups/1")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login?next=%2Fgroups%2F1" {
		t.Errorf("signed out: %d %s, want a redirect to /login", res.StatusCode, res.Header.Get("Location"))
	}
	res, err = client.PostForm(srv.URL+"/login", url.Values{"person": {"priya"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusSeeOther || len(res.Cookies()) != 1 || res.Cookies()[0].Name != loom.SessionCookie {
		t.Fatalf("sign in: %d %v, want a redirect with a session cookie", res.StatusCode, res.Cookies())
	}
	ck := res.Cookies()[0]
	r, _ := http.NewRequest("GET", srv.URL+"/groups/1", nil)
	r.AddCookie(ck)
	if res, err := client.Do(r); err != nil || res.StatusCode != 200 {
		t.Fatalf("signed in: %v %v", res, err)
	}
	r, _ = http.NewRequest("POST", srv.URL+"/logout", nil)
	r.AddCookie(ck)
	if res, err := client.Do(r); err != nil || res.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign out: %v %v", res, err)
	}
	r, _ = http.NewRequest("GET", srv.URL+"/groups/1", nil)
	r.AddCookie(ck)
	if res, err := client.Do(r); err != nil || res.StatusCode != http.StatusSeeOther {
		t.Errorf("signed out, the old cookie: %v %v, want a redirect", res, err)
	}
}

func TestDashboard(t *testing.T) {
	app, _, _ := setup(t)
	p := as(t, app, "sam", "/")
	p.AssertHTML("a.group", 3)
	p.AssertText("h1", "Good evening, Sam")
	p.AssertText(".sidebar", "Flat 4B")
	p.AssertAccessible()
}

func TestNotAMember(t *testing.T) {
	app, _, _ := setup(t)
	p := as(t, app, "lena", "/groups/1") // Lena isn't in Flat 4B
	p.AssertText(".missing", "This group isn't here")
	p.AssertHTML("main button", 0)
}

func TestFlowAddAnExpense(t *testing.T) {
	app, store, q := setup(t)
	var sam, priya *loomtest.Page
	f := loomtest.Flow(t, "Sam adds an expense and Priya hears about it")
	f.Step("Priya has Flat 4B open", func() {
		priya = as(t, app, "priya", "/groups/1")
		priya.AssertHTML(".unread", 0)
	})
	f.Step(`Sam opens "Add expense" and types "Takeaway curry", $57.40, split equally`, func() {
		sam = as(t, app, "sam", "/groups/1")
		sam.Click(`button[on:click=add_expense]`)
		sam.AssertHTML("[role=dialog]", 1)
		sam.Event("expense_form", loom.Payload{
			"description": "Takeaway curry", "amount": "57.40", "date": "2026-10-11",
			"category": "dining", "paid_by": "sam", "split": "equal",
			"among[sam]": "true", "among[priya]": "true", "among[marcus]": "true",
		})
		sam.AssertText(".split", "$19.14")
	})
	f.Step("He leaves Marcus out, and the shares update to $28.70 each", func() {
		sam.Event("expense_form", loom.Payload{
			"description": "Takeaway curry", "amount": "57.40", "date": "2026-10-11",
			"category": "dining", "paid_by": "sam", "split": "equal", "among[sam]": "true", "among[priya]": "true",
		})
		sam.AssertText(".split", "$28.70")
	})
	f.Step("He saves it, and it is at the top of the list with a toast", func() {
		sam.Submit("form.form", loom.Payload{
			"description": "Takeaway curry", "amount": "57.40", "date": "2026-10-11",
			"category": "dining", "paid_by": "sam", "split": "equal", "among[sam]": "true", "among[priya]": "true",
		})
		sam.AssertHTML("[role=dialog]", 0)
		sam.AssertText("a.row", "Takeaway curry")
		sam.AssertText("[role=status]", "Added “Takeaway curry”")
	})
	f.Step("Priya's page shows it, and she owes $28.70 more", func() {
		priya.TableChanged("expenses")
		priya.AssertText("a.row", "Takeaway curry")
		priya.AssertText("a.row", "you borrowed")
		nets, err := store.Nets(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if nets["priya"] != -78337-2870 {
			t.Errorf("Priya's balance = %d, want %d", nets["priya"], -78337-2870)
		}
	})
	f.Step("The Notify job tells Priya and Marcus, not Sam", func() {
		q.AssertEnqueued(t, Notify{GroupID: 1, Actor: "sam"})
		q.Drain(t)
		priya.TableChanged("notifications")
		priya.AssertText(".unread", "1")
		for who, want := range map[string]int{"priya": 1, "marcus": 1, "sam": 0} {
			n, err := loom.Get[int](ctx, store.DB, `select count(*)::int from notifications where person_id = $1 and body like '%Takeaway curry%'`, who)
			if err != nil {
				t.Fatal(err)
			}
			if n != want {
				t.Errorf("%s has %d notifications about it, want %d", who, n, want)
			}
		}
	})
}

func TestExactSplitMustAddUp(t *testing.T) {
	app, _, q := setup(t)
	p := as(t, app, "sam", "/groups/1")
	p.Click(`button[on:click=add_expense]`)
	fields := loom.Payload{
		"description": "Taxi", "amount": "30", "date": "2026-10-11", "category": "transport",
		"paid_by": "sam", "split": "exact", "exact[sam]": "10", "exact[priya]": "10", "exact[marcus]": "5",
	}
	p.Event("expense_form", fields)
	p.AssertText(".remaining", "$5.00 left to assign")
	p.Submit("form.form", fields)
	p.AssertText("[role=alert]", "The amounts add up to $25.00, not $30.00.")
	q.RefuteEnqueued(t, Notify{})

	fields["exact[marcus]"] = "10"
	p.Submit("form.form", fields)
	p.AssertHTML("[role=dialog]", 0)
	p.AssertText("a.row", "Taxi")
}

// TestExpenseErrors checks the form shows each error by its field: none
// while the dialog is untouched, a field's once it is changed, and every
// one after a submit.
func TestExpenseErrors(t *testing.T) {
	app, _, q := setup(t)
	p := as(t, app, "sam", "/groups/1")
	p.Click(`button[on:click=add_expense]`)
	p.AssertHTML("[aria-invalid=true]", 0)
	fields := loom.Payload{"description": "", "amount": "abc", "date": "2026-10-11", "category": "general", "paid_by": "sam", "split": "equal"}
	p.Event("expense_form", fields)
	p.AssertText("label.field", "Enter an amount, like 12.50.")
	p.AssertHTML("input[name=amount][aria-invalid=true]", 1)
	p.AssertHTML("input[name=description][aria-invalid=true]", 0)
	p.Submit("form.form", fields)
	p.AssertHTML("input[name=description][aria-invalid=true]", 1)
	p.AssertText(".split-error", "Split it with at least one person.")
	p.AssertHTML("[role=dialog]", 1)
	q.RefuteEnqueued(t, Notify{})
}

func TestSettleUpErrors(t *testing.T) {
	app, _, _ := setup(t)
	p := as(t, app, "marcus", "/groups/1")
	p.Click(`.actions button[on:click=settle_up]`)
	p.Submit("form.form", loom.Payload{"from": "marcus", "to": "marcus", "amount": ""})
	p.AssertText("label.field", "Pick two different people.")
	p.AssertText("label.field", "Enter an amount, like 40.00.")
	p.AssertHTML("[role=dialog]", 1)
}

func TestSettleUp(t *testing.T) {
	app, store, _ := setup(t)
	p := as(t, app, "marcus", "/groups/1")
	p.AssertText(".pays", "You pay Sam")
	p.AssertAccessible()
	p.Click(`.pay button[on:click=settle_up]`) // Record, filled in from the suggestion
	p.AssertHTML("[role=dialog]", 1)
	p.Submit("form.form", loom.Payload{"from": "marcus", "to": "sam", "amount": "803.23"})
	p.AssertText("[role=status]", "Payment of $803.23 recorded")
	nets, err := store.Nets(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if nets["marcus"] != 0 {
		t.Errorf("Marcus's balance after settling = %d, want 0", nets["marcus"])
	}
	p.AssertText(".pays", "Priya pays Sam")
}

func TestRemindOncePerHour(t *testing.T) {
	app, store, q := setup(t)
	p := as(t, app, "sam", "/groups/1")
	p.Click(`.pay button[val-debtor=marcus]`)
	p.AssertText("[role=status]", "Reminder sent to Marcus")
	p.Click(`.pay button[val-debtor=marcus]`)
	p.AssertText("[role=status]", "You already reminded Marcus in the last hour")
	if n := len(q.Matching(Remind{Debtor: "marcus"})); n != 1 {
		t.Errorf("%d reminder jobs, want 1", n)
	}
	q.Drain(t)
	n, err := loom.Get[int](ctx, store.DB, `select count(*)::int from notifications where person_id = 'marcus' and body like 'Sam sent a reminder%'`)
	if err != nil || n != 1 {
		t.Errorf("Marcus has %d reminders (%v), want 1", n, err)
	}
}

func TestDeleteExpense(t *testing.T) {
	app, store, q := setup(t)
	id, err := loom.Get[int64](ctx, store.DB, `select id from expenses where description = 'Pizza night'`)
	if err != nil {
		t.Fatal(err)
	}
	path := "/groups/1/expenses/" + strconv.FormatInt(id, 10)
	priya := as(t, app, "priya", path)
	priya.AssertText("h1", "Pizza night")
	priya.AssertText(".split", "$21.30")

	sam := as(t, app, "sam", path)
	sam.AssertAccessible()
	sam.Click(`button[on:click=delete]`)
	sam.AssertText("[role=dialog]", "will be removed for everyone in Flat 4B")
	sam.Click(`button[on:click=confirm_delete]`)
	if sam.Navigated() != "/groups/1" {
		t.Errorf("after deleting, navigated to %q, want /groups/1", sam.Navigated())
	}
	q.AssertEnqueued(t, Notify{GroupID: 1, Actor: "sam"})

	priya.TableChanged("expenses") // Priya had it open: it says it's gone
	priya.AssertText(".gone", "This expense was deleted")
}

func TestPostRecurringRunsOncePerMonth(t *testing.T) {
	app, store, q := setup(t)
	_ = app
	q.Clock().Set(time.Date(2026, 10, 11, 6, 0, 0, 0, time.UTC))
	q.Enqueue(PostRecurring{})
	q.Drain(t)
	q.Enqueue(PostRecurring{})
	q.Drain(t)
	posted, err := loom.Select[string](ctx, store.DB, `select description from expenses where recurring_id is not null`)
	if err != nil {
		t.Fatal(err)
	}
	if len(posted) != 1 || posted[0] != "Cleaner" {
		t.Errorf("posted %v, want the Cleaner once (rent was posted this month; broadband is due on the 15th)", posted)
	}
}

func TestPayments(t *testing.T) {
	got := payments(map[string]int64{"a": 5000, "b": -3000, "c": -2000, "d": 0})
	if len(got) != 2 || got[0].From != "b" || got[0].To != "a" || got[0].Cents != 3000 || got[1].From != "c" || got[1].Cents != 2000 {
		t.Errorf("payments = %+v", got)
	}
	if s := splitEqually(1000, []string{"a", "b", "c"}); s["a"] != 334 || s["b"] != 333 || s["c"] != 333 {
		t.Errorf("splitEqually(1000, 3) = %v", s)
	}
	for in, want := range map[string]int64{"12": 1200, "12.5": 1250, "$1,234.56": 123456, ".5": 50} {
		if got, ok := parseCents(in); !ok || got != want {
			t.Errorf("parseCents(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "abc", "1.234", "-5"} {
		if _, ok := parseCents(bad); ok {
			t.Errorf("parseCents(%q) ok, want not", bad)
		}
	}
}

// TestManifest keeps loom.manifest, the app's behaviour for review, in
// step with the code. It needs no database.
func TestManifest(t *testing.T) {
	app := loom.New()
	Setup(app)
	loomtest.Manifest(t, app)
}
