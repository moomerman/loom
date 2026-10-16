package loomtest_test

import (
	"flag"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/moomerman/loom/loomtest"
)

// TestFlow passes, and its steps match testdata/flows/TestFlow.md.
func TestFlow(t *testing.T) {
	f := loomtest.Flow(t, "Ann adds a todo")
	f.Step("Ann opens her list", func() {})
	f.Step(`She adds "Buy milk"`, func() {})
	if got, want := f.File(), "# Ann adds a todo\n\n1. Ann opens her list\n2. She adds \"Buy milk\"\n"; got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
}

// flowT is a testing.TB that records what a flow logs and reports, and
// stops the goroutine on Fatal, as testing.T does.
type flowT struct {
	testing.TB
	name     string
	failed   bool
	logs     []string
	errors   []string
	cleanups []func()
}

func (f *flowT) Name() string     { return f.name }
func (f *flowT) Helper()          {}
func (f *flowT) Failed() bool     { return f.failed }
func (f *flowT) Cleanup(c func()) { f.cleanups = append(f.cleanups, c) }
func (f *flowT) Log(args ...any)  { f.logs = append(f.logs, fmt.Sprint(args...)) }
func (f *flowT) Errorf(s string, a ...any) {
	f.failed = true
	f.errors = append(f.errors, fmt.Sprintf(s, a...))
}
func (f *flowT) Fatalf(s string, a ...any) { f.Errorf(s, a...); runtime.Goexit() }
func (f *flowT) Fatal(a ...any)            { f.Errorf("%s", fmt.Sprint(a...)); runtime.Goexit() }

// run runs test on a flowT named name, then its cleanups. With -update
// the flow would rewrite testdata/flows/TestFlow.md, so tests using run
// are skipped.
func run(t *testing.T, name string, test func(t testing.TB)) *flowT {
	if flag.Lookup("update").Value.String() == "true" {
		t.Skip("-update: the flows run here would rewrite their files")
	}
	ft := &flowT{name: name}
	done := make(chan struct{})
	go func() {
		defer close(done)
		test(ft)
	}()
	<-done
	for i := len(ft.cleanups) - 1; i >= 0; i-- {
		ft.cleanups[i]()
	}
	return ft
}

// A failing flow shows the steps that passed, the one that failed, and
// the committed file's steps it didn't reach.
func TestFlowFails(t *testing.T) {
	ft := run(t, "TestFlow", func(t testing.TB) {
		f := loomtest.Flow(t, "Ann adds a todo")
		f.Step("Ann opens her list", func() {})
		f.Step(`She adds "Buy milk"`, func() { t.Fatalf("no form") })
		f.Step("never run", func() {})
	})
	want := "flow:\nAnn adds a todo\n  ✓ Ann opens her list\n  ✗ She adds \"Buy milk\""
	if len(ft.logs) != 1 || ft.logs[0] != want {
		t.Errorf("logs %q, want %q", ft.logs, want)
	}

	ft = run(t, "TestFlow", func(t testing.TB) {
		f := loomtest.Flow(t, "Ann adds a todo")
		f.Step("Ann opens her list", func() { t.Errorf("no list") })
	})
	want = "flow:\nAnn adds a todo\n  ✗ Ann opens her list\n  ○ She adds \"Buy milk\""
	if len(ft.logs) != 1 || ft.logs[0] != want {
		t.Errorf("logs %q, want %q", ft.logs, want)
	}
}

// A passing flow whose steps differ from its file fails, with the diff.
func TestFlowDiffers(t *testing.T) {
	ft := run(t, "TestFlow", func(t testing.TB) {
		f := loomtest.Flow(t, "Ann adds a todo")
		f.Step("Ann opens her list", func() {})
		f.Step(`She adds "Buy bread"`, func() {})
	})
	if len(ft.errors) != 1 || !strings.Contains(ft.errors[0], `got:  "2. She adds \"Buy bread\""`) || !strings.Contains(ft.errors[0], "go test -update") {
		t.Errorf("errors %q", ft.errors)
	}
	ft = run(t, "TestNoSuchFlow", func(t testing.TB) {
		loomtest.Flow(t, "Missing").Step("a step", func() {})
	})
	if len(ft.errors) != 1 || !strings.Contains(ft.errors[0], "no flow file testdata/flows/TestNoSuchFlow.md") {
		t.Errorf("errors %q", ft.errors)
	}
}
