package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const manifestV1 = `# header

routes

  /  live page t.List
    auth: none
    events:
      add (on submit)
  /about  live page t.About
    auth: none
    events: none

for review

  route /: filter is read from the query string, so anyone can set it; check the page treats it as untrusted
`

const manifestV2 = `# header

routes

  /  live page t.List in t.App
    auth: none
    events:
      add (on submit)
      done (on click, val-id)
  /due  live page t.Due
    auth: none

background jobs

  t.Remind  queue default, 20 attempts, timeout 1m0s, args: none

for review

  route /: filter is read from the query string, so anyone can set it; check the page treats it as untrusted
  route /due: today is read from the query string, so anyone can set it; check the page treats it as untrusted
`

func TestDiffManifests(t *testing.T) {
	got, _ := json.Marshal(diffManifests(manifestV1, manifestV2))
	want := `[` +
		`{"section":"routes","added":[{"item":"/due  live page t.Due","lines":["auth: none"]}],` +
		`"removed":[{"item":"/about  live page t.About","lines":["auth: none","events: none"]}],` +
		`"changed":[{"item":"/  live page t.List in t.App","was":"/  live page t.List","added":["  done (on click, val-id)"]}]},` +
		`{"section":"background jobs","added":[{"item":"t.Remind  queue default, 20 attempts, timeout 1m0s, args: none"}],"removed":[],"changed":[]},` +
		`{"section":"for review","added":[{"item":"route /due: today is read from the query string, so anyone can set it; check the page treats it as untrusted"}],"removed":[],"changed":[]}]`
	if string(got) != want {
		t.Errorf("diff:\n%s\nwant:\n%s", got, want)
	}
}

// loom diff compares the manifests of two commits, or a commit and the
// working tree, in a real repository.
func TestDiffCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(text string) {
		if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "app", "loom.manifest"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	write(manifestV1)
	run("add", ".")
	run("commit", "-q", "-m", "v1")
	run("checkout", "-q", "-b", "feature")
	write(manifestV2)
	run("commit", "-q", "-am", "v2")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	code, out, _ := runCmd(runDiff, "main..HEAD")
	if code != 0 || !strings.Contains(out, "app/loom.manifest (modified)\n\n  routes\n    + /due  live page t.Due\n        auth: none\n    - /about  live page t.About\n    ~ /  live page t.List in t.App\n        was: /  live page t.List\n        + done (on click, val-id)\n") {
		t.Errorf("diff main..HEAD: %d\n%s", code, out)
	}
	code, out, _ = runCmd(runDiff, "--json", "HEAD")
	var d ManifestDiff
	if code != 0 || json.Unmarshal([]byte(out), &d) != nil || d.Head != "" || len(d.Files) != 0 {
		t.Errorf("diff HEAD against a clean tree: %d %s", code, out)
	}
	write(manifestV1)
	code, out, _ = runCmd(runDiff, "HEAD")
	if code != 0 || !strings.Contains(out, "to the working tree") || !strings.Contains(out, "    + /about  live page t.About\n") {
		t.Errorf("diff HEAD against the working tree: %d\n%s", code, out)
	}
	if code, _, errOut := runCmd(runDiff, "nope"); code != 2 || !strings.Contains(errOut, `unknown revision "nope"`) {
		t.Errorf("unknown revision: %d %s", code, errOut)
	}
}
