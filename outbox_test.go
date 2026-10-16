package loom

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/internal/hxtest"
)

// logPage appends each "add" event's x to its label, so the label shows
// which events ran and in what order. "save" inserts x into a table.
type logPage struct {
	db    *DB
	table string
	log   string
}

func (p *logPage) Mount(c *Ctx) {}

func (p *logPage) Events() Events {
	return Events{
		"add": func(c *Ctx, pl Payload) { p.log += pl["x"] },
		"save": func(c *Ctx, pl Payload) {
			time.Sleep(200 * time.Millisecond) // still running when shutdown begins
			if _, err := p.db.Exec(c.Context(), `INSERT INTO `+p.table+` VALUES ($1)`, pl["x"]); err != nil {
				c.Fail(err)
			}
		},
	}
}

func (p *logPage) Assigns(c *Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "log:" + p.log, Events: bound(p.Events())}
}

func newClientID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type ack struct {
	status  int
	V       int    `json:"v"`
	Handled bool   `json:"handled"`
	Code    string `json:"code"`
}

// send posts an event as loom.js does, from client c with sequence seq.
func send(t *testing.T, url, sid, c string, seq int, event, x string) ack {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"sid": sid, "client": c, "seq": seq, "event": event, "payload": map[string]string{"x": x}})
	res, err := http.Post(url+"/_loom/event", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	a := ack{status: res.StatusCode}
	json.NewDecoder(res.Body).Decode(&a)
	return a
}

func logApp(t *testing.T, db *DB) (*App, *httptest.Server) {
	app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if db != nil {
		app.UseDB(db)
	}
	app.Live("/", &logPage{db: db}, hxtest.Counter)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return app, srv
}

func label(t *testing.T, app *App, sid string) string {
	t.Helper()
	var html string
	if !app.session(sid).call(func() { html = app.session(sid).view.HTML() }) {
		t.Fatal("no session")
	}
	return regexp.MustCompile(`log:[a-z0-9]*`).FindString(html)
}

// An event is acked after its handler ran, with the version of its diff;
// a resend of an event already handled is answered as handled and
// doesn't run again, so retries keep the order they were sent in.
func TestEventsRunOnce(t *testing.T) {
	app, srv := logApp(t, nil)
	sid, _ := load(t, srv)
	c := newClientID()
	if a := send(t, srv.URL, sid, c, 1, "add", "a"); a.status != 200 || a.V != 2 || a.Handled {
		t.Fatalf("first: %+v", a)
	}
	if got := label(t, app, sid); got != "log:a" {
		t.Fatalf("acked before running: %s", got)
	}
	if a := send(t, srv.URL, sid, c, 1, "add", "a"); a.status != 200 || !a.Handled || a.V != 2 {
		t.Fatalf("resend: %+v", a)
	}
	send(t, srv.URL, sid, c, 2, "add", "b")
	send(t, srv.URL, sid, c, 1, "add", "a") // a late retry of an older one
	send(t, srv.URL, sid, c, 3, "add", "c")
	send(t, srv.URL, sid, c, 2, "add", "b")
	if got := label(t, app, sid); got != "log:abc" {
		t.Fatalf("after retries: %s", got)
	}
	// Another tab numbers its own events.
	send(t, srv.URL, sid, newClientID(), 1, "add", "d")
	if got := label(t, app, sid); got != "log:abcd" {
		t.Fatalf("another client: %s", got)
	}
	// Refused events aren't recorded, so their resend runs.
	app.draining.Store(true)
	if a := send(t, srv.URL, sid, c, 4, "add", "e"); a.status != 503 || a.Code != RT424 {
		t.Fatalf("while draining: %+v", a)
	}
	app.draining.Store(false)
	if a := send(t, srv.URL, sid, c, 4, "add", "e"); a.status != 200 || a.Handled {
		t.Fatalf("resent: %+v", a)
	}
	if got := label(t, app, sid); got != "log:abcde" {
		t.Fatalf("after the resend: %s", got)
	}
	// A new session (a remount) on the same node knows the client too.
	sid2, _ := load(t, srv)
	if a := send(t, srv.URL, sid2, c, 4, "add", "e"); !a.Handled {
		t.Fatalf("resend to a new session: %+v", a)
	}
	// An event the page has no handler for is refused with its code.
	if a := send(t, srv.URL, sid, c, 5, "nope", ""); a.status != 400 || a.Code != RT423 {
		t.Fatalf("unknown event: %+v", a)
	}
	if a := send(t, srv.URL, "gone", c, 6, "add", "f"); a.status != 404 || a.Code != RT404 {
		t.Fatalf("unknown session: %+v", a)
	}
}

// testDB opens the test database, with Loom's own migrations applied,
// or skips.
func testDB(t *testing.T) *DB {
	url := os.Getenv("LOOM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LOOM_TEST_DATABASE_URL to run the Postgres tests")
	}
	db, err := data.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.SQL().Close() })
	if _, err := data.Migrate(context.Background(), db, os.DirFS("migrations")); err != nil {
		t.Fatal(err)
	}
	return db
}

// With a database, an event handled on one node is answered as handled
// by another: a resend after a restart doesn't run it again.
func TestEventsRunOnceAcrossNodes(t *testing.T) {
	db := testDB(t)
	app1, srv1 := logApp(t, db)
	app2, srv2 := logApp(t, db)
	sid1, _ := load(t, srv1)
	sid2, _ := load(t, srv2)
	c := newClientID()
	if a := send(t, srv1.URL, sid1, c, 1, "add", "a"); a.status != 200 || a.Handled {
		t.Fatalf("on node 1: %+v", a)
	}
	if a := send(t, srv2.URL, sid2, c, 1, "add", "a"); a.status != 200 || !a.Handled {
		t.Fatalf("resent to node 2: %+v", a)
	}
	if got := label(t, app2, sid2); got != "log:" {
		t.Fatalf("node 2 ran it again: %s", got)
	}
	send(t, srv2.URL, sid2, c, 2, "add", "b")
	if got := label(t, app1, sid1) + label(t, app2, sid2); got != "log:alog:b" {
		t.Fatalf("labels: %s", got)
	}
}

// Shutdown refuses new events but finishes those already queued: the
// row an event's handler writes is committed before Run returns.
func TestDrainOnShutdown(t *testing.T) {
	db := testDB(t)
	table := "drain_" + newClientID()
	if _, err := db.Exec(context.Background(), `CREATE TABLE `+table+` (x text)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(context.Background(), `DROP TABLE `+table) })
	app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.UseDB(db)
	app.Live("/", &logPage{db: db, table: table}, hxtest.Counter)
	ctx, stop := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	ran := make(chan error, 1)
	go func() { ran <- app.run(ctx, "127.0.0.1:0", ready) }()
	var url string
	select {
	case addr := <-ready:
		url = "http://" + addr
	case err := <-ran:
		t.Fatalf("run: %v", err)
	}
	res, err := http.Get(url + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	sid := sidRE.FindStringSubmatch(string(body))[1]

	c := newClientID()
	acked := make(chan ack, 1)
	go func() { acked <- send(t, url, sid, c, 1, "save", "kept") }()
	time.Sleep(50 * time.Millisecond) // the handler is running
	stop()
	time.Sleep(20 * time.Millisecond)
	if a := send(t, url, sid, c, 2, "add", "x"); a.status != 503 || a.Code != RT424 {
		t.Errorf("an event during shutdown: %+v", a)
	}
	if err := <-ran; err != nil {
		t.Fatal(err)
	}
	if a := <-acked; a.status != 200 {
		t.Errorf("the queued event's ack: %+v", a)
	}
	n, err := data.Get[int](context.Background(), db, `SELECT count(*) FROM `+table+` WHERE x = 'kept'`)
	if err != nil || n != 1 {
		t.Fatalf("rows: %d, %v", n, err)
	}
}
