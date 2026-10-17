package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"strings"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/render"
	"github.com/moomerman/loom/storage"
)

type badgePage struct{}

func (p *badgePage) Mount(*loom.Ctx) {}
func (p *badgePage) Assigns(*loom.Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "hi", Events: []render.Event{{Name: "save"}}}
}

// Events has "save" for `loom event`, bound to a button on the page.
func (p *badgePage) Events() loom.Events {
	return loom.Events{"save": func(*loom.Ctx, loom.Payload) {}}
}

func devApp(t *testing.T, dev bool) string {
	mode := loom.DevOff
	if dev {
		mode = loom.DevOn
	}
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Dev: mode})
	app.Live("/badge", &badgePage{}, hxtest.Counter)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv.URL
}

func runCmd(f func([]string, io.Writer, io.Writer) int, args ...string) (int, string, string) {
	var out, errOut strings.Builder
	code := f(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestIntrospect(t *testing.T) {
	url := devApp(t, true)
	if _, err := httpGet(url + "/badge"); err != nil {
		t.Fatal(err)
	}

	code, out, _ := runCmd(runRoutes, "--url", url)
	if code != 0 || !strings.Contains(out, "/badge  *main.badgePage  hxtest.Counter") {
		t.Errorf("routes: %d %q", code, out)
	}

	code, out, _ = runCmd(runManifest, "--url", url)
	if code != 0 || !strings.Contains(out, "  /badge  live page hxtest.Counter\n    auth: none\n    events: none\n") {
		t.Errorf("manifest: %d %q", code, out)
	}
	code, out, _ = runCmd(runManifest, "--url", url, "--json")
	var m loom.Manifest
	if code != 0 || json.Unmarshal([]byte(out), &m) != nil || len(m.Routes) != 1 || m.Routes[0].Page != "*main.badgePage" {
		t.Errorf("manifest --json: %d %q", code, out)
	}

	code, out, _ = runCmd(runSessions, "--url", url, "--json")
	var sessions []loom.SessionInfo
	if code != 0 || json.Unmarshal([]byte(out), &sessions) != nil || len(sessions) != 1 {
		t.Fatalf("sessions: %d %q", code, out)
	}

	code, out, _ = runCmd(runInspect, sessions[0].SID, "--url", url)
	if code != 0 || !strings.Contains(out, `"label": "hi"`) || !strings.Contains(out, `<span class="badge">hi</span>`) {
		t.Errorf("inspect: %d %q", code, out)
	}
	code, out, _ = runCmd(runEvent, sessions[0].SID, "save", "title=milk", "--url", url)
	if code != 0 || !strings.Contains(out, `<span class="badge">hi</span>`) || !strings.Contains(out, "version 1") {
		t.Errorf("event: %d %q", code, out)
	}
	code, out, _ = runCmd(runEvent, "nope", "save", "--url", url, "--json")
	if code != 1 || !strings.Contains(out, `"code":"RT404"`) {
		t.Errorf("event to an unknown session: %d %q", code, out)
	}
	if code, _, errOut := runCmd(runEvent, sessions[0].SID, "save", "title", "--url", url); code != 2 || !strings.Contains(errOut, `payload "title" isn't KEY=VALUE`) {
		t.Errorf("event with a bad payload: %d %q", code, errOut)
	}

	code, out, _ = runCmd(runInspect, "--url", url, "--json", "nope")
	if code != 1 || !strings.Contains(out, `"code":"RT404"`) {
		t.Errorf("inspect unknown: %d %q", code, out)
	}

	f := writeFiles(t, map[string]string{"a.json": `{"label": "x"}`})
	code, out, _ = runCmd(runRender, "--url", url, "--assigns", f+"/a.json", "hxtest.Badge")
	if code != 0 || out != "<span class=\"badge\">x</span>\n" {
		t.Errorf("render: %d %q", code, out)
	}
	code, out, _ = runCmd(runErrors, "--url", url)
	if code != 0 || out != "no runtime errors\n" {
		t.Errorf("errors: %d %q", code, out)
	}
}

func TestIntrospectNoApp(t *testing.T) {
	code, _, errOut := runCmd(runRoutes, "--url", devApp(t, false))
	if code != 2 || !strings.Contains(errOut, "isn't in dev mode") {
		t.Errorf("not dev: %d %q", code, errOut)
	}
	code, _, errOut = runCmd(runRoutes, "--url", "http://127.0.0.1:1")
	if code != 2 || !strings.Contains(errOut, "nothing answered") {
		t.Errorf("no app: %d %q", code, errOut)
	}
	if code, _, _ := runCmd(runInspect); code != 2 {
		t.Errorf("inspect without a sid: %d", code)
	}
}

func httpGet(url string) (string, error) {
	res, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	return string(b), err
}

func TestStorageCommand(t *testing.T) {
	b := storage.Memory()
	for _, k := range []string{"tunes/a.mod", "tunes/b.sid", "x"} {
		b.Put(context.Background(), k, strings.NewReader("1234567890"), 10, storage.PutOptions{})
	}
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Dev: loom.DevOn, Storage: b})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	code, out, _ := runCmd(runStorage, "--url", srv.URL, "tunes/")
	if code != 0 || !strings.HasPrefix(out, "bucket: memory\n") || !strings.Contains(out, "10 B") ||
		!strings.Contains(out, "tunes/a.mod\n") || !strings.Contains(out, "tunes/b.sid\n") || strings.Contains(out, " x\n") {
		t.Errorf("storage tunes/: %d %q", code, out)
	}
	code, out, _ = runCmd(runStorage, "--url", srv.URL, "--limit", "1", "--json")
	var list loom.StorageList
	if code != 0 || json.Unmarshal([]byte(out), &list) != nil || len(list.Objects) != 1 || list.Objects[0].Key != "tunes/a.mod" {
		t.Errorf("storage --json --limit 1: %d %q", code, out)
	}
	code, out, _ = runCmd(runStorage, "--url", srv.URL, "--limit", "1")
	if code != 0 || !strings.Contains(out, `more: loom storage --after "tunes/a.mod"`) {
		t.Errorf("storage --limit 1: %d %q", code, out)
	}
	code, out, _ = runCmd(runStorage, "--url", srv.URL, "/bad")
	if code != 1 || !strings.Contains(out, "SG006") {
		t.Errorf("storage /bad: %d %q", code, out)
	}
}

// TestPortEnv: $PORT moves loom dev and the commands that ask it, unless
// $LOOM_DEV_ADDR or $LOOM_URL say otherwise.
func TestPortEnv(t *testing.T) {
	for _, tc := range []struct {
		port, devAddr, url string
		wantAddr, wantURL  string
	}{
		{"", "", "", "localhost:4000", "http://localhost:4000"},
		{"5000", "", "", "localhost:5000", "http://localhost:5000"},
		{"5000", "localhost:6000", "http://localhost:7000", "localhost:6000", "http://localhost:7000"},
	} {
		t.Setenv("PORT", tc.port)
		t.Setenv("LOOM_DEV_ADDR", tc.devAddr)
		t.Setenv("LOOM_URL", tc.url)
		if got := devAddr(); got != tc.wantAddr {
			t.Errorf("PORT=%q LOOM_DEV_ADDR=%q: devAddr() = %q, want %q", tc.port, tc.devAddr, got, tc.wantAddr)
		}
		if got := appURL(); got != tc.wantURL {
			t.Errorf("PORT=%q LOOM_URL=%q: appURL() = %q, want %q", tc.port, tc.url, got, tc.wantURL)
		}
	}
}
