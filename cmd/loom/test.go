package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/moomerman/loom/compiler"
	"github.com/moomerman/loom/compiler/diag"
)

// `loom test` checks and builds the templates, then runs go test. Stale
// generated code still compiles, so `go test` alone can pass against
// templates as they were before an edit; `loom test` can't.

// testOutput is the JSON of `loom test --json`. See docs/cli.md.
type testOutput struct {
	Passed      bool              `json:"passed"`
	Written     []string          `json:"written"`     // generated files rebuilt before testing
	Diagnostics []diag.Diagnostic `json:"diagnostics"` // template errors and warnings; with errors, no tests run
	Packages    []testPackage     `json:"packages"`
	Output      string            `json:"output,omitempty"` // go's own output that isn't a test's, such as a failed command
}

// testPackage is one package's result.
type testPackage struct {
	Package  string        `json:"package"`
	Result   string        `json:"result"` // "pass", "fail", "skip" (no tests) or "build-fail"
	Elapsed  float64       `json:"elapsed"`
	Failures []testFailure `json:"failures"`
}

// testFailure is a failed test, or a package that didn't build (Test "").
type testFailure struct {
	Test   string `json:"test"`
	Output string `json:"output"`
}

// maxFailureOutput caps each failure's output, keeping its end.
const maxFailureOutput = 8 << 10

func runTest(args []string, stdout, stderr io.Writer) int {
	fs := newCommand("test", "test [--json] [--race] [--run REGEXP] [PACKAGE ...]", "print the result as JSON", stderr)
	asJSON := fs.JSON
	run := fs.String("run", "", "run only the tests matching this regular expression, as go test -run")
	race := fs.Bool("race", false, "run with the race detector, as go test -race")
	if !fs.parse(args, 0, -1) {
		return 2
	}
	if *asJSON {
		out, err := loomTest(context.Background(), *run, *race, fs.Args())
		if err != nil {
			fmt.Fprintf(stderr, "loom test: %v\n", err)
			return 2
		}
		writeJSON(stdout, out)
		if !out.Passed {
			return 1
		}
		return 0
	}

	res, written, err := compiler.Build(".")
	if err != nil {
		fmt.Fprintf(stderr, "loom test: %v\n", err)
		return 2
	}
	printDiagnostics(stdout, res)
	if res.Diagnostics.HasErrors() {
		return 1
	}
	for _, w := range written {
		fmt.Fprintf(stdout, "wrote %s\n", w)
	}
	cmd := exec.Command("go", goTestArgs(false, *run, *race, fs.Args())...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return 1
		}
		fmt.Fprintf(stderr, "loom test: %v\n", err)
		return 2
	}
	return 0
}

func goTestArgs(asJSON bool, run string, race bool, pkgs []string) []string {
	args := []string{"test"}
	if asJSON {
		args = append(args, "-json")
	}
	if race {
		args = append(args, "-race")
	}
	if run != "" {
		args = append(args, "-run", run)
	}
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}
	return append(args, pkgs...)
}

// loomTest builds the templates under the current directory and, if
// they have no errors, runs go test -json on pkgs and summarizes it.
func loomTest(ctx context.Context, run string, race bool, pkgs []string) (*testOutput, error) {
	res, written, err := compiler.Build(".")
	if err != nil {
		return nil, err
	}
	out := &testOutput{Written: append([]string{}, written...), Diagnostics: diagnostics(res.Diagnostics), Packages: []testPackage{}}
	if res.Diagnostics.HasErrors() {
		return out, nil
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", goTestArgs(true, run, race, pkgs)...)
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out.Packages = summarize(pipe)
	waitErr := cmd.Wait()
	out.Output = tail(stderr.String(), maxFailureOutput)
	out.Passed = waitErr == nil
	for _, p := range out.Packages {
		out.Passed = out.Passed && p.Result != "fail" && p.Result != "build-fail"
	}
	if _, exit := waitErr.(*exec.ExitError); waitErr != nil && !exit {
		return nil, waitErr
	}
	return out, nil
}

// testEvent is a line of go test -json (go doc test2json).
type testEvent struct {
	Action     string
	Package    string
	ImportPath string // for build-output and build-fail
	Test       string
	Elapsed    float64
	Output     string
}

// summarize reads go test -json and returns each package's result, with
// the output of failed tests and of packages that didn't build.
func summarize(r io.Reader) []testPackage {
	byPkg := map[string]*testPackage{}
	pkg := func(name string) *testPackage {
		if byPkg[name] == nil {
			byPkg[name] = &testPackage{Package: name, Failures: []testFailure{}}
		}
		return byPkg[name]
	}
	output := map[[2]string]*strings.Builder{} // package, test -> output
	builds := map[string]*strings.Builder{}    // import path -> build output
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e testEvent
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Action {
		case "build-output":
			if builds[e.ImportPath] == nil {
				builds[e.ImportPath] = &strings.Builder{}
			}
			builds[e.ImportPath].WriteString(e.Output)
		case "output":
			k := [2]string{e.Package, e.Test}
			if output[k] == nil {
				output[k] = &strings.Builder{}
			}
			output[k].WriteString(e.Output)
		case "pass", "fail", "skip":
			if e.Test != "" {
				if e.Action == "fail" {
					p := pkg(e.Package)
					p.Failures = append(p.Failures, testFailure{Test: e.Test, Output: tail(text(output[[2]string{e.Package, e.Test}]), maxFailureOutput)})
				}
				continue
			}
			p := pkg(e.Package)
			p.Result, p.Elapsed = e.Action, e.Elapsed
			if e.Action == "fail" && len(p.Failures) == 0 {
				// It failed without a failed test: it didn't build, or
				// TestMain or init failed.
				p.Result = "build-fail"
				msg := text(output[[2]string{e.Package, ""}])
				for path, b := range builds {
					if path == e.Package || strings.HasPrefix(path, e.Package+" ") {
						msg = b.String() + msg
					}
				}
				p.Failures = append(p.Failures, testFailure{Output: tail(msg, maxFailureOutput)})
			}
		}
	}
	list := make([]testPackage, 0, len(byPkg))
	for _, p := range byPkg {
		if p.Result != "" {
			list = append(list, *p)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Package < list[j].Package })
	return list
}

func text(b *strings.Builder) string {
	if b == nil {
		return ""
	}
	return b.String()
}
