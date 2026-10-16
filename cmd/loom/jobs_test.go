package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/jobs"
)

type greet struct {
	Name string `json:"name"`
}

func (g greet) Perform(ctx context.Context, c *jobs.Ctx) error {
	if g.Name == "fail" {
		return errors.New("no such person")
	}
	return nil
}

type nightly struct{}

func (nightly) Perform(context.Context, *jobs.Ctx) error { return nil }

// jobsApp serves a dev-mode app with jobs, running.
func jobsApp(t *testing.T) (*loom.App, string) {
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Dev: loom.DevOn})
	app.Live("/badge", &badgePage{}, hxtest.Counter)
	app.Jobs(greet{}, nightly{})
	app.Queues(jobs.Queue{Name: "default", Concurrency: 5})
	app.Cron("0 3 * * *", nightly{})
	r := app.JobRunner()
	r.PollInterval = 10 * time.Millisecond
	r.Backoff = func(int) time.Duration { return time.Hour }
	r.CronCatchUp = 0 // no catch-up of the hourly prune in the first minutes of an hour: job IDs start at 1
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { r.Shutdown(context.Background()) })
	return app, srv.URL
}

func waitState(t *testing.T, url, id, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, out, _ := runCmd(runJobs, "inspect", id, "--url", url, "--json")
		var d jobs.JobDetail
		json.Unmarshal([]byte(out), &d)
		if string(d.State) == state {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s is %s, want %s", id, d.State, state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestJobsCommands(t *testing.T) {
	_, url := jobsApp(t)
	if _, err := httpGet(url + "/badge"); err != nil { // starts the runner
		t.Fatal(err)
	}
	f := writeFiles(t, map[string]string{"ok.json": `{"name": "ann"}`, "fail.json": `{"name": "fail"}`, "bad.json": `{"nom": "x"}`})

	code, out, _ := runCmd(runJobs, "enqueue", "greet", "--url", url, "--args", f+"/ok.json")
	if code != 0 || out != "enqueued job 1\n" {
		t.Fatalf("enqueue: %d %q", code, out)
	}
	waitState(t, url, "1", "completed")
	code, out, _ = runCmd(runJobs, "enqueue", "--json", "main.greet", "--url", url, "--args", f+"/fail.json")
	if code != 0 || !strings.Contains(out, `"id": 2`) {
		t.Fatalf("enqueue --json: %d %q", code, out)
	}
	waitState(t, url, "2", "retryable")

	code, out, _ = runCmd(runJobs, "--url", url)
	if code != 0 || !strings.Contains(out, "ID  TYPE        QUEUE    STATE      ATTEMPT") || !strings.Contains(out, "retryable  1/20") || !strings.Contains(out, "no such person") {
		t.Errorf("jobs: %d\n%s", code, out)
	}
	code, out, _ = runCmd(runJobs, "--url", url, "--state", "completed", "--json")
	var list []jobs.Record
	if code != 0 || json.Unmarshal([]byte(out), &list) != nil || len(list) != 1 || list[0].ID != 1 {
		t.Errorf("jobs --state completed: %d %q", code, out)
	}
	code, out, _ = runCmd(runJobs, "--url", url, "--state", "nope")
	if code != 1 || !strings.Contains(out, "error JB508: unknown state") {
		t.Errorf("jobs --state nope: %d %q", code, out)
	}

	code, out, _ = runCmd(runJobs, "inspect", "2", "--url", url)
	if code != 0 || !strings.Contains(out, "job 2: main.greet in queue default, retryable, attempt 1 of 20") || !strings.Contains(out, `"name": "fail"`) || !strings.Contains(out, "attempt 1 at") {
		t.Errorf("inspect: %d\n%s", code, out)
	}
	code, out, _ = runCmd(runJobs, "inspect", "99", "--url", url)
	if code != 1 || !strings.Contains(out, "error JB507: no job 99") {
		t.Errorf("inspect missing: %d %q", code, out)
	}

	code, out, _ = runCmd(runJobs, "cancel", "2", "--url", url)
	if code != 0 || out != "job 2 (main.greet) is now cancelled\n" {
		t.Errorf("cancel: %d %q", code, out)
	}
	code, out, _ = runCmd(runJobs, "cancel", "2", "--url", url, "--json")
	if code != 1 || !strings.Contains(out, `"code":"JB508"`) {
		t.Errorf("cancel again: %d %q", code, out)
	}
	code, out, _ = runCmd(runJobs, "retry", "1", "--url", url)
	if code != 0 || !strings.Contains(out, "job 1 (main.greet) is now") {
		t.Errorf("retry: %d %q", code, out)
	}
	waitState(t, url, "1", "completed")

	code, out, _ = runCmd(runJobs, "enqueue", "greet", "--url", url, "--args", f+"/bad.json")
	if code != 1 || !strings.Contains(out, "error JB509") || !strings.Contains(out, `unknown field "nom"`) {
		t.Errorf("enqueue bad args: %d %q", code, out)
	}
	code, out, _ = runCmd(runJobs, "enqueue", "Nope", "--url", url)
	if code != 1 || !strings.Contains(out, "error JB501: job type Nope is not registered") {
		t.Errorf("enqueue unknown: %d %q", code, out)
	}
	code, out, _ = runCmd(runJobs, "enqueue", "nightly", "--in", "1h", "--url", url)
	if code != 0 {
		t.Errorf("enqueue --in: %d %q", code, out)
	}
	waitState(t, url, "3", "scheduled")

	code, out, _ = runCmd(runQueues, "--url", url)
	if code != 0 || !strings.Contains(out, "QUEUE    RUNNING  AVAILABLE  SCHEDULED  RETRYABLE  OLDEST WAIT  COMPLETED/MIN  FAILED/MIN") || !strings.Contains(out, "default  0/5      0          1          0          -            2              1") {
		t.Errorf("queues: %d\n%s", code, out)
	}
	code, out, _ = runCmd(runQueues, "--url", url, "--json")
	var qs []jobs.QueueInfo
	if code != 0 || json.Unmarshal([]byte(out), &qs) != nil || len(qs) != 1 || qs[0].States[jobs.Cancelled] != 1 {
		t.Errorf("queues --json: %d %q", code, out)
	}

	// The one node, which leads, with its queue's workers. How many were
	// busy is as of the node's last heartbeat, which may have been while
	// a job above ran, so only the workers are checked.
	code, out, _ = runCmd(runNodes, "--url", url)
	if code != 0 || !strings.Contains(out, "NODE") || !regexp.MustCompile(` \(this one\)  up     leader  default \d/5`).MatchString(out) {
		t.Errorf("nodes: %d\n%s", code, out)
	}
	code, out, _ = runCmd(runNodes, "--url", url, "--json")
	var ns []jobs.NodeInfo
	if code != 0 || json.Unmarshal([]byte(out), &ns) != nil || len(ns) != 1 || !ns[0].Self || ns[0].State != "up" || !ns[0].Leader ||
		len(ns[0].Queues) != 1 || ns[0].Queues[0].Name != "default" || ns[0].Queues[0].Concurrency != 5 {
		t.Errorf("nodes --json: %d %q", code, out)
	}

	code, out, _ = runCmd(runCron, "--url", url)
	if code != 0 || !strings.Contains(out, "0 * * * *       jobs.Prune (built in)") || !strings.Contains(out, "0 3 * * *       main.nightly") {
		t.Errorf("cron: %d\n%s", code, out)
	}
	code, out, _ = runCmd(runCron, "--url", url, "--json")
	var cs []jobs.CronInfo
	if code != 0 || json.Unmarshal([]byte(out), &cs) != nil || len(cs) != 2 || cs[1].NextRun.Hour() != 3 || cs[1].Index != 1 {
		t.Errorf("cron --json: %d %q", code, out)
	}

	code, out, _ = runCmd(runCron, "run", "1", "--url", url)
	if code != 0 || out != "enqueued job 4\n" {
		t.Errorf("cron run: %d %q", code, out)
	}
	code, out, _ = runCmd(runCron, "run", "9", "--url", url)
	if code != 1 || !strings.Contains(out, "error JB515") {
		t.Errorf("cron run 9: %d %q", code, out)
	}
	code, out, _ = runCmd(runJobs, "--url", url, "--search", "ANN", "--json")
	if code != 0 || json.Unmarshal([]byte(out), &list) != nil || len(list) != 1 || list[0].ID != 1 {
		t.Errorf("jobs --search: %d %q", code, out)
	}
	code, out, _ = runCmd(runJobs, "--url", url, "--before", "2", "--json")
	if code != 0 || json.Unmarshal([]byte(out), &list) != nil || len(list) != 1 || list[0].ID != 1 {
		t.Errorf("jobs --before: %d %q", code, out)
	}
	code, out, _ = runCmd(runJobs, "delete", "3", "--url", url)
	if code != 0 || out != "deleted job 3\n" {
		t.Errorf("delete: %d %q", code, out)
	}
	code, out, _ = runCmd(runJobs, "inspect", "3", "--url", url)
	if code != 1 || !strings.Contains(out, "JB507") {
		t.Errorf("inspect deleted: %d %q", code, out)
	}
	code, out, _ = runCmd(runJobs, "retry-discarded", "--url", url)
	if code != 0 || out != "retried 0 discarded jobs\n" {
		t.Errorf("retry-discarded: %d %q", code, out)
	}
}

func TestMCP(t *testing.T) {
	_, url := jobsApp(t)
	httpGet(url + "/badge")
	var in bytes.Buffer
	for _, line := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"jobs_enqueue","arguments":{"type":"greet","args":{"name":"bo"},"in":"1h"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"jobs_inspect","arguments":{"id":1}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"jobs_inspect","arguments":{"id":42}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"routes","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"check","arguments":{"path":"../../internal/hxtest"}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"nope"}`,
	} {
		in.WriteString(line + "\n")
	}
	var out, errOut bytes.Buffer
	if code := runMCP([]string{"--url", url}, &in, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var res []map[string]any
	dec := json.NewDecoder(&out)
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		res = append(res, m)
	}
	if len(res) != 8 {
		t.Fatalf("%d responses, want 8 (notifications get none):\n%s", len(res), out.String())
	}
	text := func(i int) (string, bool) {
		r := res[i]["result"].(map[string]any)
		isErr, _ := r["isError"].(bool)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), isErr
	}
	if v := res[0]["result"].(map[string]any)["protocolVersion"]; v != "2025-06-18" {
		t.Errorf("initialize: %v", res[0])
	}
	tools := res[1]["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, tl := range tools {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if got := strings.Join(names, " "); got != "check docs build test dev_status components components_add manifest_diff manifest routes sessions inspect_session render_component previews preview_screenshot send_event recent_errors events trace stats topics doctor mail storage_list jobs_list jobs_inspect jobs_retry jobs_cancel jobs_delete jobs_retry_discarded jobs_enqueue queues nodes cron cron_run db_schema migrate migrate_status db_query" {
		t.Errorf("tools: %s", got)
	}
	if s, isErr := text(2); isErr || !strings.Contains(s, `"id": 1`) {
		t.Errorf("jobs_enqueue: %s", s)
	}
	if s, isErr := text(3); isErr || !strings.Contains(s, `"state": "scheduled"`) || !strings.Contains(s, `"name": "bo"`) {
		t.Errorf("jobs_inspect: %s", s)
	}
	if s, isErr := text(4); !isErr || !strings.Contains(s, `"code":"JB507"`) {
		t.Errorf("jobs_inspect missing: %s", s)
	}
	if s, isErr := text(5); isErr || !strings.Contains(s, `"/badge"`) {
		t.Errorf("routes: %s", s)
	}
	if s, isErr := text(6); isErr || s != "[]" {
		t.Errorf("check: %s", s)
	}
	if e := res[7]["error"].(map[string]any); e["code"].(float64) != -32601 {
		t.Errorf("unknown method: %v", res[7])
	}
}
