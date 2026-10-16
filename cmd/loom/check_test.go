package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moomerman/loom/compiler/diag"
)

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		path := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCheckOK(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a.hx":               "component A()\n<p>a</p>\n",
		"sub/b.hx":           "component B(x int)\n<p>{x}</p>\n",
		"testdata/broken.hx": "<p>",
	})
	var out, errOut bytes.Buffer
	if code := runCheck([]string{dir}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errOut.String())
	}
	if got := out.String(); got != "ok: 2 templates checked\n" {
		t.Errorf("got %q", got)
	}
}

func TestCheckErrorsJSON(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a.hx": "component A()\n<p>{}</p>\n",
		"b.hx": "component B()\n<ul><li></ul>\n",
		"c.hx": "component C()\n<p>fine</p>\n",
	})
	var out, errOut bytes.Buffer
	if code := runCheck([]string{"--json", dir}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	var got []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if len(got) != 2 || got[0].Code != "PA219" || got[1].Code != "PA217" {
		t.Fatalf("unexpected diagnostics: %+v", got)
	}
	if got[0].Line != 2 || got[0].Col != 4 || got[0].Fix == "" || !strings.HasSuffix(got[0].File, "a.hx") {
		t.Errorf("unexpected diagnostic: %+v", got[0])
	}
}

func TestCheckJSONEmptyIsArray(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.hx": "component A()\n"})
	var out, errOut bytes.Buffer
	runCheck([]string{"--json", dir}, &out, &errOut)
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("got %q, want []", out.String())
	}
}

func TestCheckHuman(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.hx": "component A()\n<p>{}</p>\n"})
	var out, errOut bytes.Buffer
	runCheck([]string{dir}, &out, &errOut)
	for _, want := range []string{"error PA219: empty expression", "= fix:", "1 error in 1 template checked"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestBuild(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"models.go":   "package app\n\ntype Todo struct{ Title string }\n",
		"todo_one.hx": "component TodoOne(t Todo)\n<p>{t.title}</p>\n",
	})
	var out, errOut bytes.Buffer
	if code := runBuild([]string{"--json", dir}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errOut.String())
	}
	var res struct {
		Written     []string          `json:"written"`
		Diagnostics []diag.Diagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	gen := filepath.Join(dir, "todo_one_hx.go")
	if len(res.Written) != 1 || res.Written[0] != gen || res.Diagnostics == nil {
		t.Fatalf("unexpected result: %s", out.String())
	}
	src, _ := os.ReadFile(gen)
	if !strings.Contains(string(src), "type TodoOneAssigns struct") {
		t.Errorf("unexpected generated code:\n%s", src)
	}

	out.Reset()
	runBuild([]string{dir}, &out, &errOut)
	if !strings.Contains(out.String(), "0 files written") {
		t.Errorf("second build rewrote files: %s", out.String())
	}
}

func TestBuildWritesNothingOnErrors(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a.hx": "component A(n int)\n<p>{m}</p>\n",
		"b.hx": "component B()\n<p>ok</p>\n",
	})
	var out, errOut bytes.Buffer
	if code := runBuild([]string{dir}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "b_hx.go")); err == nil {
		t.Error("build wrote files despite errors")
	}
	if !strings.Contains(out.String(), "TC301") {
		t.Errorf("missing diagnostic: %s", out.String())
	}
}
