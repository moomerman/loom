package loom

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/jobs"
)

// newsPage shows the latest headline published on "news".
type newsPage struct{ headline string }

func (p *newsPage) Mount(c *Ctx) {
	c.Subscribe("news")
	c.Subscribe("news") // twice is fine
	p.headline = "none"
}

func (p *newsPage) Assigns(c *Ctx) hxtest.BadgeAssigns { return hxtest.BadgeAssigns{Label: p.headline} }

func (p *newsPage) HandleInfo(c *Ctx, msg Message) {
	if msg.Event == "boom" {
		panic("boom")
	}
	p.headline = msg.Payload["headline"]
}

// deafPage subscribes but can't receive.
type deafPage struct{}

func (p *deafPage) Mount(c *Ctx)                       { c.Subscribe("news") }
func (p *deafPage) Assigns(c *Ctx) hxtest.BadgeAssigns { return hxtest.BadgeAssigns{} }

func TestPubSubToPage(t *testing.T) {
	app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Live("/", &newsPage{}, hxtest.Badge)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	sid, _ := load(t, srv)
	events := stream(t, srv, sid, "1")
	app.PubSub().Publish(Message{Topic: "news", Event: "headline", Payload: map[string]string{"headline": "Loom ships jobs"}})
	ev := next(t, events)
	if ev.event != "diff" || !strings.Contains(ev.data, "Loom ships jobs") {
		t.Fatalf("got %s %s", ev.event, ev.data)
	}
	// Other topics don't reach the page.
	app.PubSub().Publish(Message{Topic: "sport", Event: "headline", Payload: map[string]string{"headline": "nope"}})
	// A panic in HandleInfo restarts the page, which subscribes again.
	app.PubSub().Publish(Message{Topic: "news", Event: "boom"})
	if ev := next(t, events); ev.event != "full" {
		t.Fatalf("after a panic: %s %s", ev.event, ev.data)
	}
	app.PubSub().Publish(Message{Topic: "news", Event: "headline", Payload: map[string]string{"headline": "again"}})
	if ev := next(t, events); ev.event != "diff" || !strings.Contains(ev.data, "again") {
		t.Fatalf("after restart: %s %s", ev.event, ev.data)
	}
}

func TestSubscribeWithoutHandleInfo(t *testing.T) {
	app := New()
	app.Live("/", &deafPage{}, hxtest.Badge)
	_, err := app.NewView(httptest.NewRequest("GET", "/", nil), "sid", true)
	var e *Error
	if !errors.As(err, &e) || e.Code != RT410 || !strings.Contains(e.Fix, "HandleInfo") {
		t.Fatalf("got %v, want RT410", err)
	}
}

// exportJob tells live pages an export is ready.
type exportJob struct {
	User string `json:"user"`
}

func (j exportJob) Perform(ctx context.Context, c *jobs.Ctx) error {
	return c.Broadcast("news", "headline", map[string]string{"headline": "export ready for " + j.User})
}

func TestJobBroadcastReachesPage(t *testing.T) {
	app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Live("/", &newsPage{}, hxtest.Badge)
	app.Jobs(exportJob{})
	app.JobRunner().PollInterval = 10 * time.Millisecond
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	t.Cleanup(app.shutdownJobs)

	sid, _ := load(t, srv) // the first request starts the runner
	events := stream(t, srv, sid, "1")
	var committed bool
	err := app.DB().Tx(context.Background(), func(tx Tx) error {
		tx.AfterCommit(func() { committed = true })
		_, err := jobs.Enqueue(tx, exportJob{User: "ann"})
		return err
	})
	if err != nil || !committed {
		t.Fatalf("enqueue: %v, committed %v", err, committed)
	}
	if ev := next(t, events); ev.event != "diff" || !strings.Contains(ev.data, "export ready for ann") {
		t.Fatalf("got %s %s", ev.event, ev.data)
	}
}

func TestJobsStartupError(t *testing.T) {
	app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Cron("* * * * *", exportJob{}) // not registered
	err := app.startJobs()
	var e *jobs.Error
	if !errors.As(err, &e) || e.Code != jobs.JB501 {
		t.Fatalf("got %v, want JB501", err)
	}
}

// With a database, a broadcast on one node reaches the pages of every
// node, by NOTIFY, and its own node's once.
func TestPubSubAcrossNodes(t *testing.T) {
	db := testDB(t)
	node := func() (*App, *httptest.Server) {
		app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		app.UseDB(db)
		app.Live("/", &newsPage{}, hxtest.Badge)
		srv := httptest.NewServer(app)
		t.Cleanup(srv.Close)
		return app, srv
	}
	app1, srv1 := node()
	_, srv2 := node()
	sid1, _ := load(t, srv1)
	sid2, _ := load(t, srv2)
	events1, events2 := stream(t, srv1, sid1, "1"), stream(t, srv2, sid2, "1")

	if err := app1.Broadcast("news", "headline", map[string]string{"headline": "across"}); err != nil {
		t.Fatal(err)
	}
	for i, events := range []<-chan sse{events1, events2} {
		if ev := next(t, events); ev.event != "diff" || !strings.Contains(ev.data, "across") {
			t.Fatalf("node %d: %s %s", i+1, ev.event, ev.data)
		}
	}
	select {
	case ev := <-events1:
		t.Fatalf("node 1 had its own broadcast twice: %s %s", ev.event, ev.data)
	case <-time.After(300 * time.Millisecond):
	}

	big := map[string]string{"headline": strings.Repeat("x", 8000)}
	var le *Error
	if err := app1.Broadcast("news", "headline", big); !errors.As(err, &le) || le.Code != RT447 {
		t.Fatalf("a broadcast over 8000 bytes: %v, want RT447", err)
	}
	if ev := next(t, events1); !strings.Contains(ev.data, "xxxx") {
		t.Fatalf("node 1 should still get it: %s", ev.data)
	}
}
