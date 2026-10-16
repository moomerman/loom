package jobsweb_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/jobsweb"
	"github.com/moomerman/loom/loomtest"
	"github.com/moomerman/loom/pgjobs"
)

// Email is a fixture job: it fails when Fail is set.
type Email struct {
	To   string `json:"to"`
	Fail bool   `json:"fail"`
}

func (Email) Options() jobs.Options { return jobs.Options{Queue: "mail", MaxAttempts: 2} }

func (e Email) Perform(ctx context.Context, c *jobs.Ctx) error {
	if e.Fail {
		return errors.New("smtp is down")
	}
	return nil
}

func allow(c *loom.Ctx) error { return nil }

// stores runs a test on the in-memory store and, when
// LOOM_TEST_DATABASE_URL names a server, on Postgres.
func stores(t *testing.T, test func(t *testing.T, newQueue func() *loomtest.JobQueue)) {
	t.Run("memory", func(t *testing.T) {
		test(t, func() *loomtest.JobQueue { return loomtest.Jobs(t) })
	})
	t.Run("postgres", func(t *testing.T) {
		test(t, func() *loomtest.JobQueue {
			// A database of its own, so jobs really commit and the
			// test runs alongside other packages' Postgres tests. Its
			// migrations are Loom's own (../migrations), with loom_jobs.
			db := loomtest.DB(t, loomtest.Committed())
			return loomtest.Jobs(t, loomtest.WithStore(func(c jobs.Clock) jobs.Store {
				return pgjobs.New(db, pgjobs.Options{Clock: c})
			}))
		})
	})
}

// availableOne matches the mail queue's row when it has one available
// job: its second cell holds 1.
var availableOne = regexp.MustCompile(`(?s)data-loom-key="mail"[^>]*>\s*<td[^>]*>.*?</td>\s*<td class="num[^"]*">1</td>`)

// dashboard mounts jobsweb at /admin/jobs on q's runner, with a digest
// cron entry.
func dashboard(t *testing.T, q *loomtest.JobQueue, cfg jobsweb.Config) *loom.App {
	q.Jobs(Email{})
	q.Cron("0 3 * * *", Email{To: "digest"})
	cfg.Runner = q.Runner()
	app := loom.New()
	app.Mount("/admin/jobs", jobsweb.New(cfg), loom.Auth(allow))
	return app
}

// fixtures enqueues and runs jobs: 1 completed, 2 discarded, 3 available.
func fixtures(t *testing.T, q *loomtest.JobQueue) {
	q.Enqueue(Email{To: "ann@example.com"})
	q.Enqueue(Email{To: "bob@example.com", Fail: true})
	q.Drain(t)
	q.Advance(time.Hour)
	q.Drain(t)
	q.Enqueue(Email{To: "cy@example.com"})
}

func TestOverview(t *testing.T) {
	stores(t, func(t *testing.T, newQueue func() *loomtest.JobQueue) {
		q := newQueue()
		app := dashboard(t, q, jobsweb.Config{})
		fixtures(t, q)
		p := loomtest.Mount(t, app, "/admin/jobs")
		p.Listen(q.PubSub())
		if p.Title() != "Overview" || !strings.Contains(p.Document(), "/admin/jobs/_assets/icon.") || !strings.Contains(p.Document(), "/_loom/css/") {
			t.Fatalf("title %q, document:\n%s", p.Title(), p.Document())
		}
		p.AssertHTML(`a[href="/admin/jobs/jobs?queue=mail"]`, 1)
		p.AssertHTML(".jobs tbody tr", 1) // the discarded job
		p.AssertHTML(`a[href="/admin/jobs/jobs/2"]`, 1)
		p.AssertHTML(".sidebar nav a.current", 1)
		loomtest.AssertAccessible(t, p.Document())

		// Live: the available job completes; the page hears it and updates.
		if !availableOne.MatchString(p.HTML()) {
			t.Fatalf("no available job shown:\n%s", p.HTML())
		}
		v := p.Version()
		q.Drain(t)
		if p.Version() == v {
			t.Fatal("the page didn't update when the job ran")
		}

		// Retry all discarded jobs, after confirming.
		p.Event("retry_discarded", nil)
		p.AssertHTML(".confirm", 1)
		p.AssertAccessible()
		p.Event("dismiss", nil)
		p.AssertHTML(".confirm", 0)
		p.Event("retry_discarded", nil)
		p.Event("confirm", nil)
		if !strings.Contains(p.HTML(), "Retried 1 discarded jobs.") {
			t.Fatalf("flash:\n%s", p.HTML())
		}
		if rec, _ := q.Store().Get(context.Background(), 2); rec.State != jobs.Available {
			t.Fatalf("job 2 is %s, want available", rec.State)
		}
	})
}

// TestNodes: the overview lists the node registry live: each node up or
// stale, the leader, and workers busy per queue, re-rendering when a
// node reports and when one's lease ends without a report.
func TestNodes(t *testing.T) {
	stores(t, func(t *testing.T, newQueue func() *loomtest.JobQueue) {
		q := newQueue()
		app := dashboard(t, q, jobsweb.Config{})
		ctx := context.Background()
		p := loomtest.Mount(t, app, "/admin/jobs")
		p.Listen(q.PubSub())
		p.AssertText(".empty", "No node is running jobs.")
		// The per-node stats name the node serving the page.
		p.AssertText(".heading .aside", "the last minute on loomtest")

		// This node reports; so does another, which leads and has three
		// jobs running. Each heartbeat is announced on jobs.NodesTopic.
		if err := q.Runner().Heartbeat(ctx); err != nil {
			t.Fatal(err)
		}
		p.AssertHTML("tr.node-row", 1)
		p.AssertText(`tr.node-row.up[data-loom-key="loomtest"]`, "serving this page")
		p.AssertText(`tr.node-row.up[data-loom-key="loomtest"] .worker`, "mail 0/10")
		now := q.Clock().Now()
		web2 := jobs.Node{Name: "web-2", Leader: true, Queues: []jobs.NodeQueue{{Name: "default", Concurrency: 10, Running: 3}},
			StartedAt: now.Add(-time.Hour), SeenAt: now, LeaseUntil: now.Add(30 * time.Second)}
		heartbeat := func(n jobs.Node) {
			t.Helper()
			if err := q.Store().Heartbeat(ctx, n); err != nil {
				t.Fatal(err)
			}
			q.PubSub().Publish(loom.Message{Topic: jobs.NodesTopic, Event: jobs.NodesEvent, Payload: map[string]string{"node": n.Name}})
		}
		heartbeat(web2)
		p.AssertHTML("tr.node-row.up", 2)
		p.AssertText(`tr[data-loom-key="web-2"] .badge`, "leader")
		p.AssertText(`tr[data-loom-key="web-2"] .worker`, "default 3/10")
		p.AssertText(`tr[data-loom-key="web-2"]`, "1h ago")
		p.AssertHTML(`tr[data-loom-key="loomtest"] .badge`, 0)
		p.AssertAccessible()

		// Leadership moves: the badge follows the heartbeats.
		web2.Leader = false
		heartbeat(web2)
		p.AssertHTML(".badge", 0)

		// web-2 stops reporting. When its lease ends the page's timer
		// reloads the nodes, with no heartbeat to wake it: web-2 is stale.
		p.AssertText(`tr[data-loom-key="web-2"] .health`, "up")
		q.Advance(31 * time.Second)
		p.Advance(31 * time.Second)
		p.AssertText(`tr[data-loom-key="web-2"] .health.stale`, "stale")
		if err := q.Runner().Heartbeat(ctx); err != nil {
			t.Fatal(err)
		}
		p.AssertHTML("tr.node-row.up", 1)
		p.AssertHTML("tr.node-row.stale", 1)
	})
}

// TestRescuedJobNamesNode: a job rescued from a node that died holding
// it says which node in its history.
func TestRescuedJobNamesNode(t *testing.T) {
	stores(t, func(t *testing.T, newQueue func() *loomtest.JobQueue) {
		q := newQueue()
		app := dashboard(t, q, jobsweb.Config{})
		ctx := context.Background()
		id := q.Enqueue(Email{To: "ann@example.com"})
		now := jobs.Round(q.Clock().Now())
		if _, err := q.Store().Fetch(ctx, jobs.FetchRequest{Queue: "mail", Limit: 1, Node: "worker-7", Now: now, LeaseUntil: now.Add(30 * time.Second)}); err != nil {
			t.Fatal(err)
		}
		q.Advance(31 * time.Second)
		if _, err := q.Store().Rescue(ctx, jobs.Round(q.Clock().Now())); err != nil {
			t.Fatal(err)
		}
		p := loomtest.Mount(t, app, fmt.Sprintf("/admin/jobs/jobs/%d", id))
		p.AssertText(".history .note", "node worker-7 stopped renewing this job's lease")
	})
}

// TestOverviewHearsEnqueues: on the memory store, enqueues are announced
// at once, so the overview counts a new job without any job running.
func TestOverviewHearsEnqueues(t *testing.T) {
	q := loomtest.Jobs(t)
	app := dashboard(t, q, jobsweb.Config{})
	p := loomtest.Mount(t, app, "/admin/jobs")
	p.Listen(q.PubSub())
	p.AssertHTML(`a[href="/admin/jobs/jobs?queue=mail"]`, 1)
	if availableOne.MatchString(p.HTML()) {
		t.Fatal("a job before any was enqueued")
	}
	q.Enqueue(Email{To: "new@example.com"})
	if !availableOne.MatchString(p.HTML()) {
		t.Fatalf("the enqueued job isn't counted:\n%s", p.HTML())
	}
}

func TestJobList(t *testing.T) {
	stores(t, func(t *testing.T, newQueue func() *loomtest.JobQueue) {
		q := newQueue()
		app := dashboard(t, q, jobsweb.Config{})
		fixtures(t, q)

		p := loomtest.Mount(t, app, "/admin/jobs/jobs?state=discarded")
		p.AssertHTML(".jobs tbody tr", 1)
		p.AssertHTML(`nav.tabs a.current[href="/admin/jobs/jobs?state=discarded"]`, 1)
		p.AssertAccessible()
		if !strings.Contains(p.HTML(), "smtp is down") {
			t.Fatalf("no error shown:\n%s", p.HTML())
		}
		// The state tabs are links that patch the URL; the selects and
		// search patch it too, keeping the other filters.
		p.Follow(`nav.tabs a[href="/admin/jobs/jobs"]`)
		p.AssertHTML(".jobs tbody tr", 3)
		p.Event("filter", loom.Payload{"field": "search", "value": "CY@"})
		p.AssertHTML(".jobs tbody tr", 1)
		if got := p.URL(); got != "/admin/jobs/jobs?search=CY%40" {
			t.Fatalf("URL after search: %q", got)
		}
		p.Event("filter", loom.Payload{"field": "search", "value": ""})
		p.Event("filter", loom.Payload{"field": "queue", "value": "default"})
		p.AssertHTML(".jobs tbody tr", 0)
		p.AssertHTML(".empty", 1)
		p.AssertHTML(`nav.tabs a[href="/admin/jobs/jobs?queue=default&state=retryable"]`, 1)
		if got := p.URL(); got != "/admin/jobs/jobs?queue=default" {
			t.Fatalf("URL after filtering by queue: %q", got)
		}
		p.Patch("/admin/jobs/jobs?state=discarded") // the back button
		p.AssertHTML(".jobs tbody tr", 1)

		// Keyset pages of 50, newest first.
		for i := 0; i < 57; i++ {
			q.Enqueue(Email{To: fmt.Sprintf("user%d@example.com", i)})
		}
		p = loomtest.Mount(t, app, "/admin/jobs/jobs")
		p.AssertHTML(".jobs tbody tr", 50)
		p.AssertHTML(`a[href="/admin/jobs/jobs/60"]`, 1)
		p.Event("older", nil)
		p.AssertHTML(".jobs tbody tr", 10)
		p.AssertHTML(`a[href="/admin/jobs/jobs/1"]`, 1)
		p.AssertHTML(".pager button", 2) // newest, newer
		p.Event("newer", nil)
		p.AssertHTML(`a[href="/admin/jobs/jobs/60"]`, 1)
		p.AssertHTML(".pager button", 1) // older
	})
}

func TestJobDetail(t *testing.T) {
	stores(t, func(t *testing.T, newQueue func() *loomtest.JobQueue) {
		q := newQueue()
		app := dashboard(t, q, jobsweb.Config{})
		fixtures(t, q)

		p := loomtest.Mount(t, app, "/admin/jobs/jobs/2")
		p.Listen(q.PubSub())
		if p.Title() != "Job 2" {
			t.Errorf("title %q", p.Title())
		}
		p.AssertHTML(".attempt", 2)
		p.AssertHTML(".state.discarded", 2) // the heading and the history
		p.AssertAccessible()
		if !strings.Contains(p.HTML(), "bob@example.com") || !strings.Contains(p.HTML(), "attempt 1 failed: smtp is down") {
			t.Fatalf("detail:\n%s", p.HTML())
		}
		p.AssertHTML(".actions button", 2) // retry and delete; a finished job can't be cancelled

		p.Event("retry", nil)
		p.AssertHTML(".heading .state.available", 1)
		p.AssertHTML(".actions button", 2) // retry is gone; cancel and delete

		// The job runs elsewhere (here, Drain); the page updates live.
		q.Drain(t)
		p.AssertHTML(".heading .state.discarded", 1)

		p.Event("delete", nil)
		p.AssertHTML(".confirm", 1)
		p.Event("confirm", nil)
		if !strings.Contains(p.HTML(), "There is no job 2") || !strings.Contains(p.HTML(), "Deleted the job.") {
			t.Fatalf("after delete:\n%s", p.HTML())
		}

		// Cancel an available job, after confirming.
		id := q.Enqueue(Email{To: "dee@example.com"})
		c := loomtest.Mount(t, app, fmt.Sprintf("/admin/jobs/jobs/%d", id))
		c.Event("cancel", nil)
		c.Event("confirm", nil)
		c.AssertHTML(".heading .state.cancelled", 1)

		missing := loomtest.Mount(t, app, "/admin/jobs/jobs/999")
		missing.AssertHTML(".empty", 1)
	})
}

func TestCron(t *testing.T) {
	stores(t, func(t *testing.T, newQueue func() *loomtest.JobQueue) {
		q := newQueue()
		app := dashboard(t, q, jobsweb.Config{})
		p := loomtest.Mount(t, app, "/admin/jobs/cron")
		p.AssertHTML(".cron tbody tr", 2) // the built-in prune, and the digest
		p.AssertAccessible()
		if !strings.Contains(p.HTML(), "0 3 * * *") || !strings.Contains(p.HTML(), "not yet") {
			t.Fatalf("cron:\n%s", p.HTML())
		}
		p.Event("run_now", loom.Payload{"index": "1"}) // 0 is the built-in prune
		if !strings.Contains(p.HTML(), "Enqueued job 1.") {
			t.Fatalf("run now:\n%s", p.HTML())
		}
		q.AssertEnqueued(t, Email{To: "digest"})
	})
}

// TestReadOnly: read-only operators see no action buttons, and actions
// sent as events anyway are refused (RT438, before the handlers' own
// read-only check).
func TestReadOnly(t *testing.T) {
	stores(t, func(t *testing.T, newQueue func() *loomtest.JobQueue) {
		q := newQueue()
		app := dashboard(t, q, jobsweb.Config{ReadOnly: func(*loom.Ctx) bool { return true }})
		fixtures(t, q)
		ctx := context.Background()

		o := loomtest.Mount(t, app, "/admin/jobs")
		o.AssertHTML("button", 0)
		o.AssertHTML(".mode .badge", 1)
		o.AssertRefused("retry_discarded", nil)
		o.AssertRefused("confirm", nil)
		if rec, _ := q.Store().Get(ctx, 2); rec.State != jobs.Discarded {
			t.Errorf("read-only retry ran: job 2 is %s", rec.State)
		}

		d := loomtest.Mount(t, app, "/admin/jobs/jobs/2")
		d.AssertHTML("button", 0)
		d.AssertRefused("retry", nil)
		d.AssertRefused("delete", nil)
		d.AssertRefused("confirm", nil)
		if rec, err := q.Store().Get(ctx, 2); err != nil || rec.State != jobs.Discarded {
			t.Errorf("read-only actions ran: %v %v", rec, err)
		}

		c := loomtest.Mount(t, app, "/admin/jobs/cron")
		c.AssertHTML("button", 0)
		c.AssertRefused("run_now", loom.Payload{"index": "1"})
		if len(q.Matching(Email{To: "digest"})) != 0 {
			t.Error("read-only run now enqueued a job")
		}
	})
}

func TestRedactArgs(t *testing.T) {
	q := loomtest.Jobs(t)
	app := dashboard(t, q, jobsweb.Config{RedactArgs: func(typ string, args json.RawMessage) json.RawMessage {
		return json.RawMessage(`{"to":"[redacted]"}`)
	}})
	fixtures(t, q)
	for _, path := range []string{"/admin/jobs", "/admin/jobs/jobs", "/admin/jobs/jobs/1", "/admin/jobs/cron"} {
		p := loomtest.Mount(t, app, path)
		if strings.Contains(p.HTML(), "@example.com") || strings.Contains(p.HTML(), `"digest"`) {
			t.Errorf("%s shows args:\n%s", path, p.HTML())
		}
	}
	loomtest.Mount(t, app, "/admin/jobs/jobs").AssertHTML(`input[type=search]`, 0)
}

func TestRequiresAuth(t *testing.T) {
	defer func() {
		var e *loom.Error
		if err, _ := recover().(error); !errors.As(err, &e) || e.Code != loom.RT415 {
			t.Fatalf("mounting without auth: %v, want RT415", err)
		}
	}()
	loom.New().Mount("/admin/jobs", jobsweb.New(jobsweb.Config{}))
}
