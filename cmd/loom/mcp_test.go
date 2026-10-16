package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom"
)

// callMCP calls one MCP tool and returns its text and whether it failed.
func callMCP(t *testing.T, c *devClient, name string, args map[string]any) (string, bool) {
	t.Helper()
	for _, tool := range mcpTools() {
		if tool.Name == name {
			res := callTool(c, tool, args)
			isErr, _ := res["isError"].(bool)
			return res["content"].([]any)[0].(map[string]any)["text"].(string), isErr
		}
	}
	t.Fatalf("no tool %s", name)
	return "", false
}

// TestMCPLoop covers the tools that close the edit, build and verify
// loop: build, components_add, send_event, recent_errors and migrate.
func TestMCPLoop(t *testing.T) {
	url := devApp(t, true)
	c := &devClient{base: url, http: &http.Client{Timeout: 10 * time.Second}, db: &dbOpts{dir: "migrations"}}

	dir := writeFiles(t, map[string]string{
		"doc.go":    "package views\n",
		"greet.hx":  "component Greet(name string)\n<p>Hi {name}</p>\n",
		"broken.hx": "component Broken(n int)\n<p>{m}</p>\n",
	})
	s, isErr := callMCP(t, c, "build", map[string]any{"path": dir})
	if isErr || !strings.Contains(s, `"code":"TC301"`) || !strings.Contains(s, `"written":[]`) {
		t.Errorf("build with an error: %s", s)
	}
	s, isErr = callMCP(t, c, "build", map[string]any{"path": filepath.Join(dir, "greet.hx")})
	if isErr || !strings.Contains(s, "greet_hx.go") || !strings.Contains(s, `"diagnostics":[]`) {
		t.Errorf("build: %s", s)
	}

	comps := filepath.Join(t.TempDir(), "components")
	s, isErr = callMCP(t, c, "components_add", map[string]any{"names": []any{"badge"}, "dir": comps})
	if isErr || !strings.Contains(s, `"status": "added"`) || !strings.Contains(s, "badge_hx.go") {
		t.Errorf("components_add: %s", s)
	}
	s, isErr = callMCP(t, c, "components_add", map[string]any{"names": []any{"nope"}, "dir": comps})
	if !isErr || !strings.Contains(s, "nope") {
		t.Errorf("components_add unknown: %s", s)
	}

	if _, err := httpGet(url + "/badge"); err != nil {
		t.Fatal(err)
	}
	s, _ = callMCP(t, c, "sessions", nil)
	var sessions []loom.SessionInfo
	if err := json.Unmarshal([]byte(s), &sessions); err != nil || len(sessions) != 1 {
		t.Fatalf("sessions: %s", s)
	}
	sid := sessions[0].SID
	s, isErr = callMCP(t, c, "send_event", map[string]any{"sid": sid, "event": "save", "payload": map[string]any{"n": 2}})
	var ev loom.EventResult
	if err := json.Unmarshal([]byte(s), &ev); isErr || err != nil || !strings.HasPrefix(ev.HTML, `<span class="badge">hi</span>`) || len(ev.Errors) != 0 || ev.Ended {
		t.Errorf("send_event: %s", s)
	}
	s, isErr = callMCP(t, c, "send_event", map[string]any{"sid": "nope", "event": "save"})
	if !isErr || !strings.Contains(s, "RT404") {
		t.Errorf("send_event to an unknown session: %s", s)
	}

	res, err := http.Post(url+"/_loom/dev/errors", "application/json", strings.NewReader(`{"message":"RT418 boom","url":"`+url+`/badge"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	s, isErr = callMCP(t, c, "recent_errors", nil)
	var errs []loom.ErrorInfo
	if err := json.Unmarshal([]byte(s), &errs); isErr || err != nil || len(errs) != 1 || errs[0].Source != "browser" || errs[0].Code != loom.RT420 {
		t.Errorf("recent_errors: %s", s)
	}

	s, isErr = callMCP(t, c, "migrate", nil)
	if !isErr || !strings.Contains(s, "DATABASE_URL") {
		t.Errorf("migrate without a database: %s", s)
	}
}
