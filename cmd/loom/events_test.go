package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/moomerman/loom"
)

// loom events lists what the app did, with a cursor; loom trace shows one
// action; loom stats counts them.
func TestEventsCommands(t *testing.T) {
	url := devApp(t, true)
	if _, err := httpGet(url + "/badge"); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCmd(runEvents, "--url", url, "--json")
	var r loom.EventsResult
	if code != 0 || json.Unmarshal([]byte(out), &r) != nil || len(r.Events) == 0 || r.Events[0].Name != "page.mount" {
		t.Fatalf("events --json: %d %q", code, out)
	}
	_, out, _ = runCmd(runSessions, "--url", url, "--json")
	var sessions []loom.SessionInfo
	json.Unmarshal([]byte(out), &sessions)
	code, out, _ = runCmd(runEvent, sessions[0].SID, "save", "--url", url, "--json")
	var ev loom.EventResult
	if code != 0 || json.Unmarshal([]byte(out), &ev) != nil || ev.Trace == "" {
		t.Fatalf("event --json: %d %q", code, out)
	}

	code, out, _ = runCmd(runEvents, "--url", url, "--since", itoa(r.Next), "--name", "page.*")
	if code != 0 || !strings.Contains(out, " page.event ") || !strings.Contains(out, "event=save") || strings.Contains(out, "page.mount") {
		t.Errorf("events --since: %d %q", code, out)
	}
	code, out, _ = runCmd(runTrace, ev.Trace, "--url", url)
	if code != 0 || !strings.Contains(out, "trace "+ev.Trace) || !strings.Contains(out, "ms page.event") {
		t.Errorf("trace: %d %q", code, out)
	}
	code, out, _ = runCmd(runTrace, "nope", "--url", url)
	if code != 0 || !strings.Contains(out, "no events with trace nope") {
		t.Errorf("trace unknown: %d %q", code, out)
	}
	code, out, _ = runCmd(runStats, "--url", url)
	if code != 0 || !strings.Contains(out, "1 live pages open") || !strings.Contains(out, "page.event") {
		t.Errorf("stats: %d %q", code, out)
	}
	code, out, _ = runCmd(runQueries, "--url", url)
	if code != 0 || !strings.Contains(out, "the busiest first") {
		t.Errorf("queries: %d %q", code, out)
	}
	code, out, _ = runCmd(runQueries, "--url", url, "--json")
	var q loom.QueriesResult
	if code != 0 || json.Unmarshal([]byte(out), &q) != nil || q.Queries == nil {
		t.Errorf("queries --json: %d %q", code, out)
	}
}

func itoa(n uint64) string { b, _ := json.Marshal(n); return string(b) }

// loom doctor runs every check and exits 1 if one fails, saying how to
// fix it.
func TestDoctor(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"app/go.mod":        "module example.com/app\n",
		"app/page.hx":       "component Page(name string)\n<p>{name}</p>\n",
		"app/migrations/01": "",
	}) + "/app"
	t.Setenv("DATABASE_URL", "")
	url := devApp(t, true)
	code, out, _ := runCmd(runDoctor, "--url", url, "--dir", dir+"/migrations", dir)
	if code != 1 {
		t.Errorf("doctor = %d, want 1:\n%s", code, out)
	}
	for _, want := range []string{
		"FAIL templates  generated code is out of date",
		"fix: run `loom build " + dir + "`",
		"FAIL database   " + dir + "/migrations/ has migrations, but DATABASE_URL isn't set",
		"ok   mcp        no .mcp.json",
		"ok   app        ok: database none, jobs none, 0 live pages",
		"ok   startup    app.Validate passes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
	code, out, _ = runCmd(runDoctor, "--url", "http://127.0.0.1:1", "--json", "--dir", dir+"/none", dir)
	var checks []Check
	if code != 1 || json.Unmarshal([]byte(out), &checks) != nil || len(checks) != 5 || checks[4].Check != "app" || !checks[4].OK {
		t.Errorf("doctor --json with no app: %d %s", code, out)
	}
}
