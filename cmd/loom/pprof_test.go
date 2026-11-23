package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moomerman/loom"
)

// loom pprof saves a profile from a deployed app, carrying the inspect
// token that go tool pprof can't send.
func TestPprof(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), InspectToken: token})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	dir := t.TempDir()

	for _, name := range []string{"heap", "allocs", "goroutine"} {
		file := filepath.Join(dir, name+".pb.gz")
		code, out, errOut := runCmd(runPprof, name, "--url", srv.URL, "--token", token, "--out", file)
		if code != 0 || !strings.Contains(out, "wrote the "+name+" profile to "+file) || !strings.Contains(out, "go tool pprof") {
			t.Errorf("pprof %s: %d %q %q", name, code, out, errOut)
		}
		b, err := os.ReadFile(file)
		if err != nil || !bytes.HasPrefix(b, []byte{0x1f, 0x8b}) { // a gzipped profile
			t.Errorf("pprof %s wrote %d bytes, %v", name, len(b), err)
		}
	}

	file := filepath.Join(dir, "j.pb.gz")
	code, out, _ := runCmd(runPprof, "heap", "--url", srv.URL, "--token", token, "--out", file, "--json")
	var r PprofResult
	if code != 0 || json.Unmarshal([]byte(out), &r) != nil || r.Profile != "heap" || r.File != file || r.Bytes == 0 {
		t.Errorf("pprof --json: %d %q", code, out)
	}

	if code, _, errOut := runCmd(runPprof, "heap", "--url", srv.URL, "--out", file); code != 2 || !strings.Contains(errOut, "inspect token") {
		t.Errorf("pprof without the token: %d %q", code, errOut)
	}
	if code, _, errOut := runCmd(runPprof, "profile", "--url", srv.URL, "--token", token); code != 2 || !strings.Contains(errOut, "heap, allocs or goroutine") {
		t.Errorf("pprof profile: %d %q", code, errOut)
	}
}
