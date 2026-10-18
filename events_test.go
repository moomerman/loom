package loom

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/trace"
)

// notifyJob broadcasts on "notes" when it runs.
type notifyJob struct {
	N int `json:"n"`
}

func (notifyJob) Perform(ctx context.Context, c *jobs.Ctx) error {
	return c.Broadcast("notes", "noted", nil)
}

// notePage commits a job in its "add" handler, and re-renders when the
// job's broadcast arrives.
type notePage struct {
	app   *App
	heard int
}

func (p *notePage) Mount(c *Ctx) { c.Subscribe("notes") }

func (p *notePage) Events() Events {
	return Events{
		"add": func(c *Ctx, _ Payload) {
			err := p.app.DB().Tx(c.Context(), func(tx Tx) error {
				_, err := jobs.Enqueue(tx, notifyJob{N: 1})
				return err
			})
			if err != nil {
				c.Fail(err)
			}
		},
		"fail": func(c *Ctx, _ Payload) { c.Fail(errors.New("the store is down")) },
	}
}

func (p *notePage) HandleInfo(c *Ctx, msg Message) { p.heard++ }

func (p *notePage) Assigns(c *Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: strings.Repeat("|", p.heard), Events: bound(p.Events())}
}

type eventLog struct {
	mu  sync.Mutex
	all []trace.Event
}

func (l *eventLog) add(e trace.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all = append(l.all, e)
}

func noteApp(t *testing.T, logs *bytes.Buffer, onEvent func(trace.Event)) *App {
	app := New(Config{Logger: slog.New(slog.NewTextHandler(logs, nil)), OnEvent: onEvent})
	app.Jobs(notifyJob{})
	app.JobRunner().Manual = true // the test runs the jobs
	app.Live("/", &notePage{app: app}, hxtest.Counter)
	if err := app.startJobs(); err != nil {
		t.Fatal(err)
	}
	return app
}

// waitEvent waits for an event matching f, which a session goroutine may
// emit after the request that caused it has returned.
func waitEvent(t *testing.T, app *App, f trace.Filter) trace.Event {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if got := app.Stream().Since(0, f); len(got) > 0 {
			return got[0]
		}
	}
	t.Fatalf("no event matching %+v in:\n%s", f, eventNames(app.Stream().Since(0, trace.Filter{})))
	return trace.Event{}
}

func eventNames(es []trace.Event) string {
	var b strings.Builder
	for _, e := range es {
		b.WriteString(e.Name + " " + e.Trace + "\n")
	}
	return b.String()
}

// A click, its commit, its job, the job's broadcast and the re-render it
// causes share one trace: the tab's hashed client id and the event's
// sequence, which a resend keeps.
func TestEventTrace(t *testing.T) {
	var logs bytes.Buffer
	var seen eventLog
	app := noteApp(t, &logs, seen.add)
	srv := newServer(t, app)
	sid, _ := load(t, srv)
	waitEvent(t, app, trace.Filter{Name: trace.PageMount})

	c := newClientID()
	if a := send(t, srv.URL, sid, c, 1, "add", ""); a.status != 200 {
		t.Fatalf("add: %+v", a)
	}
	id := eventTrace(c, 1)
	if strings.Contains(id, c) {
		t.Errorf("trace %s shows the client id", id)
	}
	if _, err := app.JobRunner().RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, app, trace.Filter{Name: trace.PageInfo, Trace: id})
	var names []string
	for _, e := range app.Stream().Since(0, trace.Filter{Trace: id}) {
		names = append(names, e.Name)
		if e.Name == trace.PageEvent && (e.String("event") != "add" || e.String("outcome") != "ok" || e.String("sid") != sid || e.String("route") != "/") {
			t.Errorf("page.event = %+v", e)
		}
	}
	// The page handles the broadcast on its own goroutine, so page.info
	// may come before or after job.finish.
	want := "job.enqueue tx.commit page.event job.start pubsub.publish job.finish page.info"
	slices.Sort(names[5:])
	if got := strings.Join(names, " "); got != want {
		t.Errorf("trace %s:\n got %s\nwant %s", id, got, want)
	}

	// A resend keeps the trace, and is recorded as a duplicate.
	send(t, srv.URL, sid, c, 1, "add", "")
	waitEvent(t, app, trace.Filter{Name: trace.EventDuplicate, Trace: id})
	// An event for no session never reaches a page, but leaves evidence.
	if a := send(t, srv.URL, "nope", c, 2, "add", ""); a.status != 404 {
		t.Fatalf("no session: %+v", a)
	}
	if e := waitEvent(t, app, trace.Filter{Name: trace.EventRefused}); e.String("code") != RT404 || e.String("sid") != "nope" {
		t.Errorf("event.refused = %+v", e)
	}
	// Info events reach the log; per-click Debug ones don't.
	if !strings.Contains(logs.String(), "msg=event.refused code=RT404") || strings.Contains(logs.String(), "page.event") {
		t.Errorf("log:\n%s", logs.String())
	}
	// OnEvent sees every event the stream keeps.
	seen.mu.Lock()
	n := len(seen.all)
	seen.mu.Unlock()
	if kept := len(app.Stream().Since(0, trace.Filter{})); n != kept {
		t.Errorf("OnEvent got %d events, the stream kept %d", n, kept)
	}
}

// An error is one event: its message, with code, location and fix as
// attributes, logged and listed by `loom errors` with its trace and
// route.
func TestErrorEvent(t *testing.T) {
	var logs bytes.Buffer
	app := noteApp(t, &logs, nil)
	srv := newServer(t, app)
	sid, _ := load(t, srv)
	c := newClientID()
	send(t, srv.URL, sid, c, 1, "fail", "")
	e := waitEvent(t, app, trace.Filter{Name: trace.Error})
	if e.String("code") != RT419 || e.String("msg") != "the store is down" || !strings.Contains(e.String("where"), "events_test.go") || e.String("route") != "/" || e.Trace != eventTrace(c, 1) {
		t.Errorf("error event = %+v", e)
	}
	line := logs.String()
	if !strings.Contains(line, `level=ERROR msg="the store is down" event=error code=RT419`) || !strings.Contains(line, "where=") || !strings.Contains(line, "sid="+sid[:8]+" ") {
		t.Errorf("log line:\n%s", line)
	}
	errs := app.errors.all()
	if len(errs) != 1 || errs[0].Code != RT419 || errs[0].Trace != e.Trace || errs[0].Route != "/" || errs[0].SID != sid {
		t.Errorf("loom errors = %+v", errs)
	}
}

// /_loom/dev/events pages through the stream with ?since=, filters it,
// and follows it with ?follow=1; send_event returns the event's trace
// and what it did.
func TestDevEvents(t *testing.T) {
	var logs bytes.Buffer
	app := noteApp(t, &logs, nil)
	app.dev = true
	srv := newServer(t, app)
	sid, _ := load(t, srv)

	var page EventsResult
	getJSON(t, srv.URL+"/_loom/dev/events?name=page.*", &page)
	if len(page.Events) != 1 || page.Events[0].Name != trace.PageMount || page.Next == 0 {
		t.Fatalf("events = %+v", page)
	}
	res, err := http.Post(srv.URL+"/_loom/dev/sessions/"+sid+"/event", "application/json", strings.NewReader(`{"event":"add"}`))
	if err != nil {
		t.Fatal(err)
	}
	var r EventResult
	json.NewDecoder(res.Body).Decode(&r)
	res.Body.Close()
	var names []string
	for _, e := range r.Events {
		names = append(names, e.Name)
	}
	if r.Trace == "" || strings.Join(names, " ") != "job.enqueue tx.commit page.event" {
		t.Errorf("send_event: trace %q, events %q", r.Trace, names)
	}
	var since EventsResult
	getJSON(t, srv.URL+"/_loom/dev/events?since="+itoa(page.Next), &since)
	if len(since.Events) == 0 || since.Events[0].Seq <= page.Next {
		t.Errorf("since %d: %+v", page.Next, since.Events)
	}

	// Follow: the kept matching events, then new ones as they happen.
	fres, err := http.Get(srv.URL + "/_loom/dev/events?follow=1&name=billing.*")
	if err != nil {
		t.Fatal(err)
	}
	defer fres.Body.Close()
	go func() {
		time.Sleep(50 * time.Millisecond)
		app.Emit(context.Background(), "billing.charged", "amount", 3)
	}()
	sc := bufio.NewScanner(fres.Body)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var e trace.Event
			json.Unmarshal([]byte(data), &e)
			if e.Name != "billing.charged" || e.String("amount") != "3" {
				t.Errorf("followed %s", data)
			}
			break
		}
	}

	var stats StatsResult
	getJSON(t, srv.URL+"/_loom/dev/stats", &stats)
	if stats.Sessions != 1 || len(stats.Events) == 0 {
		t.Errorf("stats = %+v", stats)
	}
	if stats.Runtime.Goroutines == 0 || stats.Runtime.HeapBytes == 0 {
		t.Errorf("runtime = %+v", stats.Runtime)
	}
	if code := getJSON(t, srv.URL+"/_loom/dev/pprof/goroutine?debug=1", nil); code != 200 {
		t.Errorf("pprof goroutine = %d", code)
	}
	byRoute := false
	for _, s := range stats.Events {
		if s.Name == trace.PageEvent && s.By == "route" && s.Key != "" && s.Count > 0 && s.LastMinute > 0 {
			byRoute = true
		}
	}
	if !byRoute {
		t.Errorf("no page.event stats by route: %+v", stats.Events)
	}
	var queries QueriesResult
	getJSON(t, srv.URL+"/_loom/dev/queries", &queries)
	if queries.Queries == nil || queries.Started.IsZero() {
		t.Errorf("queries = %+v", queries)
	}
	var topics map[string][]string
	getJSON(t, srv.URL+"/_loom/dev/topics", &topics)
	if len(topics["notes"]) != 1 || topics["notes"][0] != sid {
		t.Errorf("topics = %v", topics)
	}
	var d SessionDetail
	getJSON(t, srv.URL+"/_loom/dev/sessions/"+sid, &d)
	if len(d.Topics) != 1 || d.Topics[0] != "notes" || d.Watch == nil {
		t.Errorf("session topics %q, watch %q", d.Topics, d.Watch)
	}
}

func itoa(n uint64) string { b, _ := json.Marshal(n); return string(b) }

func newServer(t *testing.T, app *App) *httptest.Server {
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}
