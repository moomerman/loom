package loom_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/loomtest"
)

// itemsPage shows how many items there are; its route watches items.
type itemsPage struct{ db *loom.DB }

func (p *itemsPage) Mount(c *loom.Ctx) {}

func (p *itemsPage) Assigns(c *loom.Ctx) hxtest.BadgeAssigns {
	n, err := loom.Get[int64](c.Context(), p.db, `SELECT count(*) FROM items`)
	if err != nil {
		panic(err)
	}
	return hxtest.BadgeAssigns{Label: "items:" + strconv.FormatInt(n, 10)}
}

func watchDB(t *testing.T, opts ...loomtest.DBOption) *loom.DB {
	return loomtest.DB(t, append(opts, loomtest.Migrations("testdata/watch/migrations"))...)
}

func watchApp(db *loom.DB, tables ...string) *loom.App {
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.UseDB(db)
	app.Live("/", &itemsPage{db: db}, hxtest.Badge, loom.Watch(tables...))
	return app
}

var watchSID = regexp.MustCompile(`data-sid="([0-9a-f]+)"`)

// openPage loads the page and returns a channel of the data of the diffs
// on its stream.
func openPage(t *testing.T, url string) <-chan string {
	t.Helper()
	res, err := http.Get(url + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	m := watchSID.FindSubmatch(body)
	if m == nil {
		t.Fatalf("GET / = %d:\n%s", res.StatusCode, body)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/_loom/stream?v=1&sid="+string(m[1]), nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan string, 16)
	go func() {
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				ch <- d
			}
		}
	}()
	return ch
}

func nextDiff(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a diff")
	}
	return ""
}

func execSQL(t *testing.T, db *loom.DB, sql string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}

// TestWatchAcrossNodes runs two apps on one database, as two nodes: a
// change committed outside either page (here, straight to the database)
// re-renders the pages on both. A rolled-back change and a change to
// another table re-render nothing.
func TestWatchAcrossNodes(t *testing.T) {
	db := watchDB(t, loomtest.Committed())
	s1, s2 := httptest.NewServer(watchApp(db, "items")), httptest.NewServer(watchApp(db, "items"))
	t.Cleanup(s1.Close)
	t.Cleanup(s2.Close)
	p1, p2 := openPage(t, s1.URL), openPage(t, s2.URL)

	execSQL(t, db, `INSERT INTO items (title) VALUES ('milk')`)
	for i, p := range []<-chan string{p1, p2} {
		if d := nextDiff(t, p); !strings.Contains(d, "items:1") {
			t.Fatalf("node %d after a commit: %s", i+1, d)
		}
	}
	// Changes in quick succession are coalesced: fewer renders than
	// changes, and the last shows them all.
	for range 5 {
		execSQL(t, db, `INSERT INTO items (title) VALUES ('eggs')`)
	}
	var last string
	for n := 0; !strings.Contains(last, "items:6"); n++ {
		if n == 5 {
			t.Fatalf("five changes, five renders: not coalesced (last %s)", last)
		}
		last = nextDiff(t, p1)
	}
	// Node 2 renders the same changes, coalesced its own way: let it
	// catch up too, so none of its renders of them is still on the way.
	for last = ""; !strings.Contains(last, "items:6"); {
		last = nextDiff(t, p2)
	}

	err := db.Tx(context.Background(), func(tx loom.Tx) error {
		tx.Exec(context.Background(), `INSERT INTO items (title) VALUES ('never')`)
		return errors.New("roll back")
	})
	if err == nil {
		t.Fatal("the transaction should fail")
	}
	execSQL(t, db, `INSERT INTO notes (body) VALUES ('hi')`)
	select {
	case d := <-p2:
		if !strings.Contains(d, "items:6") { // a late coalesced render is fine
			t.Fatalf("a diff after a rollback: %s", d)
		}
	case <-time.After(500 * time.Millisecond):
	}
}

// TestWatchChecksTables: Run and Validate refuse a watch on a table
// that isn't tracked, doesn't exist, or with no database.
func TestWatchChecksTables(t *testing.T) {
	db := watchDB(t)
	for _, tc := range []struct{ table, want string }{
		{"notes", "isn't tracked"},
		{"itemz", "doesn't exist"},
	} {
		err := watchApp(db, tc.table).Validate(context.Background())
		if !data.IsCode(err, data.DB622) || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "tracked tables: items") {
			t.Errorf("watching %s: %v", tc.table, err)
		}
	}
	if err := watchApp(db, "items").Validate(context.Background()); err != nil {
		t.Errorf("watching items: %v", err)
	}
	app := loom.New()
	app.Live("/", &itemsPage{}, hxtest.Badge, loom.Watch("items"))
	if err := app.Validate(context.Background()); !data.IsCode(err, data.DB622) || !strings.Contains(err.Error(), "no database") {
		t.Errorf("with no database: %v", err)
	}
	if err := app.Run("127.0.0.1:0"); !data.IsCode(err, data.DB622) {
		t.Errorf("Run with no database: %v", err)
	}
}

// TestWatchInLoomtest: TableChanged re-renders a page as a change to a
// watched table does, and refuses a table the route doesn't watch.
func TestWatchInLoomtest(t *testing.T) {
	db := watchDB(t)
	app := watchApp(db, "items")
	p := loomtest.Mount(t, app, "/")
	p.AssertText("span", "items:0")
	execSQL(t, db, `INSERT INTO items (title) VALUES ('milk')`)
	p.TableChanged("items")
	p.AssertText("span", "items:1")
	if got := app.Manifest().Routes[0].Watch; len(got) != 1 || got[0] != "items" {
		t.Errorf("manifest watch = %q", got)
	}
	if got := app.Routes()[0].Watch; len(got) != 1 || got[0] != "items" {
		t.Errorf("routes watch = %q", got)
	}
}
