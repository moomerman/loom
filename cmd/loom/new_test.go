package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moomerman/loom/compiler"
)

// TestNew scaffolds an app against this checkout of Loom, then checks
// that its templates are valid and that it builds, vets and passes its
// own test. It needs no network: Loom's dependencies come from the
// module cache, which building this package has filled.
func TestNew(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOWORK", "off")
	dir := filepath.Join(t.TempDir(), "hello")
	var stdout, stderr bytes.Buffer
	if code := runNew([]string{"--json", "--module", "example.com/hello-world", "--loom", root, dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout.String(), stderr.String())
	}
	var out newOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("%v: %s", err, stdout.String())
	}
	if out.Module != "example.com/hello-world" || out.Loom != "replace" || len(out.Diagnostics) != 0 {
		t.Errorf("output %+v", out)
	}
	_, goOnPath := exec.LookPath("go")
	if goOnPath == nil && len(out.Warnings) != 0 {
		t.Errorf("warnings: %q", out.Warnings)
	}
	if last := out.Next[len(out.Next)-1]; last != "loom dev" {
		t.Errorf("next ends %q", last)
	}
	for _, f := range []string{
		"go.mod", "go.sum", ".gitignore", "app.go", "cmd/hello-world/main.go",
		"counter.go", "counter.hx", "counter_hx.go", "counter_test.go", "layouts.hx", "layouts_hx.go", "tokens.toml",
		"AGENTS.md", "CLAUDE.md", ".mcp.json", ".claude/skills/loom/SKILL.md",
		"Dockerfile", ".dockerignore", "migrations/0001_loom_clients.sql", "migrations/0004_loom_jobs.sql", "migrations/0005_loom_nodes.sql",
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
		if !contains(out.Files, f) {
			t.Errorf("files doesn't list %s: %q", f, out.Files)
		}
	}

	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}
	if mod := read("go.mod"); !strings.Contains(mod, "module example.com/hello-world") || !strings.Contains(mod, "replace github.com/moomerman/loom => "+root) {
		t.Errorf("go.mod:\n%s", mod)
	}
	if main := read("cmd/hello-world/main.go"); !strings.Contains(main, `helloworld "example.com/hello-world"`) {
		t.Errorf("main.go doesn't import the app as helloworld:\n%s", main)
	}
	if main := read("cmd/hello-world/main.go"); !strings.Contains(main, "loom.MigrateCommand(helloworld.Migrations(), os.Args[2:]") {
		t.Errorf("main.go doesn't run migrate:\n%s", main)
	}
	if df := read("Dockerfile"); !strings.Contains(df, "go build -trimpath -o /app ./cmd/hello-world") || !strings.Contains(df, "local checkout") {
		t.Errorf("Dockerfile:\n%s", df)
	}
	if !strings.HasPrefix(read("CLAUDE.md"), "@AGENTS.md\n") {
		t.Errorf("CLAUDE.md doesn't import AGENTS.md:\n%s", read("CLAUDE.md"))
	}
	for _, want := range []string{"loom check", "loom build", "loom dev", "loom test", "loom migrate", "loom db new", "loom components add", "loom mcp", "loom docs", "before `go test`"} {
		if !strings.Contains(read("AGENTS.md"), want) {
			t.Errorf("AGENTS.md doesn't mention %q", want)
		}
	}
	var mcp struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(read(".mcp.json")), &mcp); err != nil || mcp.MCPServers["loom"].Command != "loom" || strings.Join(mcp.MCPServers["loom"].Args, " ") != "mcp" {
		t.Errorf(".mcp.json (%v):\n%s", err, read(".mcp.json"))
	}
	if skill := read(".claude/skills/loom/SKILL.md"); !strings.HasPrefix(skill, "---\nname: loom\ndescription: Use when") {
		t.Errorf("SKILL.md frontmatter:\n%s", skill)
	}

	res, err := compiler.Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diagnostics) != 0 || res.Checked != 2 {
		t.Fatalf("loom check: %d templates, diagnostics %v", res.Checked, res.Diagnostics)
	}

	if goOnPath != nil {
		t.Skip("go isn't on the PATH; not building the app")
	}
	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}, {"test", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, b)
		}
	}
}

func TestNewRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644)
	var stdout, stderr bytes.Buffer
	if code := runNew([]string{dir}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "is not empty (it has notes.txt)") {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("wrote into a non-empty directory: %v", entries)
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	for _, args := range [][]string{
		{"--module", "my app", filepath.Join(t.TempDir(), "a")},
		{"--loom", t.TempDir(), filepath.Join(t.TempDir(), "b")},
	} {
		var stdout, stderr bytes.Buffer
		if code := runNew(args, &stdout, &stderr); code != 2 || stderr.Len() == 0 {
			t.Errorf("%q: exit %d: %s", args, code, stderr.String())
		}
	}
}

func TestPackageName(t *testing.T) {
	for in, want := range map[string]string{"myapp": "myapp", "hello-world": "helloworld", "My_App": "myapp", "2048": "app2048", "type": "typeapp", "loom": "loomapp", "app": "appweb", "---": "web"} {
		if got := packageName(in); got != want {
			t.Errorf("packageName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDocs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runDocs([]string{"--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var list docsList
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil || len(list.Topics) < 10 || list.Topics[0].Name != "getting-started" || list.Topics[1].Title != "Template syntax" {
		t.Fatalf("%v: %s", err, stdout.String())
	}

	stdout.Reset()
	if code := runDocs([]string{"syntax", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var page docsPage
	if err := json.Unmarshal(stdout.Bytes(), &page); err != nil || page.Name != "syntax" || !strings.HasPrefix(page.Content, "# Template syntax\n") {
		t.Fatalf("%v: %.200s", err, stdout.String())
	}

	stdout.Reset()
	if code := runDocs([]string{"syntax"}, &stdout, &stderr); code != 0 || !strings.HasPrefix(stdout.String(), "# Template syntax\n") {
		t.Fatalf("exit %d: %.200s", code, stdout.String())
	}
	stdout.Reset()
	if code := runDocs(nil, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "getting-started") {
		t.Fatalf("exit %d: %s", code, stdout.String())
	}
	stderr.Reset()
	if code := runDocs([]string{"nope"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), `unknown docs topic "nope"`) {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
}

func TestMCPDocs(t *testing.T) {
	in := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"docs","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"docs","arguments":{"topic":"live"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"docs","arguments":{"topic":"nope"}}}`,
	}, "\n") + "\n")
	var out, errOut bytes.Buffer
	if code := runMCP(nil, in, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var res []struct {
		Content []struct{ Text string }
		IsError bool
	}
	dec := json.NewDecoder(&out)
	for dec.More() {
		var m struct {
			Result struct {
				Content []struct{ Text string }
				IsError bool
			}
		}
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		res = append(res, m.Result)
	}
	if len(res) != 3 {
		t.Fatalf("%d results: %s", len(res), out.String())
	}
	// The same JSON as `loom docs --json`, without indentation.
	var stdout, stderr bytes.Buffer
	runDocs([]string{"--json"}, &stdout, &stderr)
	var want, got any
	json.Unmarshal(stdout.Bytes(), &want)
	json.Unmarshal([]byte(res[0].Content[0].Text), &got)
	w, _ := json.Marshal(want)
	g, _ := json.Marshal(got)
	if string(w) != string(g) {
		t.Errorf("docs tool list differs from loom docs --json:\n%s", res[0].Content[0].Text)
	}
	if !strings.Contains(res[1].Content[0].Text, `"name":"live"`) || !strings.Contains(res[1].Content[0].Text, `"content":"# Live pages`) {
		t.Errorf("docs live: %.200s", res[1].Content[0].Text)
	}
	if !res[2].IsError || !strings.Contains(res[2].Content[0].Text, "unknown docs topic") {
		t.Errorf("docs nope: %+v", res[2])
	}
}
