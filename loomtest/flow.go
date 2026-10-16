package loomtest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FlowTest is a test written as a user's flow through the app: steps in
// plain words, each with the code that does or checks it. Flow keeps the
// steps in testdata/flows/<test>.md, so they read as acceptance criteria
// a person can agree before the code exists and review in a pull request
// without reading Go, and prints them back as a checklist.
type FlowTest struct {
	t     testing.TB
	title string
	path  string
	steps []flowStep
}

type flowStep struct {
	text  string
	state string // running, passed or failed
}

// Flow starts a flow test titled title:
//
//	f := loomtest.Flow(t, "Ann plans her day")
//	var p *loomtest.Page
//	f.Step("Ann opens her todo list", func() {
//		p = loomtest.Mount(t, app, "/")
//	})
//	f.Step(`She adds "Buy milk"`, func() {
//		p.Submit("form.new-todo", loom.Payload{"title": "Buy milk"})
//	})
//
// When the test passes, its title and steps must match
// testdata/flows/<test>.md (go test -update writes it). When it fails,
// the checklist shows which steps passed, the one that failed, and the
// steps of the file it didn't reach, so a flow file written first, and a
// test growing step by step to match it, shows the progress.
func Flow(t testing.TB, title string) *FlowTest {
	t.Helper()
	f := &FlowTest{t: t, title: title, path: snapshotPath(t, "flows", ".md")}
	t.Cleanup(f.finish)
	return f
}

// Step runs do as the next step of the flow. The step fails if the test
// fails during do; a t.Fatal in it ends the flow there.
func (f *FlowTest) Step(text string, do func()) {
	f.t.Helper()
	if strings.TrimSpace(text) == "" || strings.Contains(text, "\n") {
		f.t.Fatalf("flow step %q: a step is one line of plain words", text)
	}
	f.steps = append(f.steps, flowStep{text: text, state: "running"})
	s := &f.steps[len(f.steps)-1]
	failed := f.t.Failed()
	do()
	s.state = "passed"
	if !failed && f.t.Failed() {
		s.state = "failed"
	}
}

// File returns the flow as testdata/flows holds it: a heading and the
// numbered steps.
func (f *FlowTest) File() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", f.title)
	for i, s := range f.steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s.text)
	}
	return b.String()
}

// Checklist returns the flow as a checklist: ✓ passed, ✗ failed, and ○
// for the steps of the committed file the test didn't reach.
func (f *FlowTest) Checklist() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", f.title)
	for _, s := range f.steps {
		mark := "✓"
		if s.state != "passed" {
			mark = "✗"
		}
		fmt.Fprintf(&b, "  %s %s\n", mark, s.text)
	}
	if f.t.Failed() {
		if want, err := readFlow(f.path); err == nil && len(want) > len(f.steps) {
			for _, s := range want[len(f.steps):] {
				fmt.Fprintf(&b, "  ○ %s\n", s)
			}
		}
	}
	return b.String()
}

func (f *FlowTest) finish() {
	f.t.Helper()
	for i := range f.steps {
		if f.steps[i].state == "running" {
			f.steps[i].state = "failed" // its do called t.Fatal or panicked
		}
	}
	f.t.Log("flow:\n" + strings.TrimSuffix(f.Checklist(), "\n"))
	if f.t.Failed() {
		return
	}
	got := f.File()
	if *update {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(f.path, []byte(got), 0o644); err != nil {
			f.t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		f.t.Errorf("no flow file %s; run go test -update to write it, and commit it", f.path)
		return
	}
	if err != nil {
		f.t.Fatal(err)
	}
	if string(want) != got {
		f.t.Errorf("the flow's steps differ from %s (run go test -update to accept them):\n%s", f.path, lineDiff(string(want), got))
	}
}

// readFlow returns the steps of a flow file.
func readFlow(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var steps []string
	for _, line := range strings.Split(string(data), "\n") {
		if n, text, ok := strings.Cut(line, ". "); ok && n != "" && strings.Trim(n, "0123456789") == "" {
			steps = append(steps, text)
		}
	}
	return steps, nil
}
