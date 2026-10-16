package parser

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moomerman/loom/compiler/ast"
	"github.com/moomerman/loom/compiler/diag"
)

var update = flag.Bool("update", false, "rewrite golden files")

// golden compares got with the contents of path, or rewrites path with -update.
func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch (run go test -update to accept)\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// TestParseGolden parses each testdata/ok/*.hx file and compares the AST
// dump with the matching .ast file.
func TestParseGolden(t *testing.T) {
	files, _ := filepath.Glob("testdata/ok/*.hx")
	if len(files) == 0 {
		t.Fatal("no test files")
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := ParseString(filepath.Base(path), string(src))
			if err != nil {
				t.Fatalf("unexpected error:\n%v", err)
			}
			golden(t, strings.TrimSuffix(path, ".hx")+".ast", ast.Dump(f))
		})
	}
}

// errorCase is one case from testdata/errors.txt.
type errorCase struct {
	name, src string
}

// readErrorCases reads testdata/errors.txt, where each case starts with a
// line "-- name --" followed by the template source.
func readErrorCases(t *testing.T) []errorCase {
	data, err := os.ReadFile("testdata/errors.txt")
	if err != nil {
		t.Fatal(err)
	}
	var cases []errorCase
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if strings.HasPrefix(line, "-- ") && strings.HasSuffix(strings.TrimSpace(line), " --") {
			name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "-- "), " --")
			cases = append(cases, errorCase{name: name})
			continue
		}
		if len(cases) > 0 {
			cases[len(cases)-1].src += line
		}
	}
	return cases
}

// TestErrors checks that each malformed template in testdata/errors.txt
// fails with a well-formed error, and compares the messages with
// testdata/errors.golden.
func TestErrors(t *testing.T) {
	var out strings.Builder
	seen := map[diag.Code]bool{}
	for _, c := range readErrorCases(t) {
		_, err := ParseString(c.name+".hx", c.src)
		if err == nil {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		e, ok := err.(*diag.Error)
		if !ok {
			t.Errorf("%s: error is %T, not *diag.Error", c.name, err)
			continue
		}
		if e.Fix == "" {
			t.Errorf("%s: error %s has no fix", c.name, e.Code)
		}
		if !strings.HasPrefix(c.name, string(e.Code)) {
			t.Errorf("%s: got code %s; name each case after the code it tests", c.name, e.Code)
		}
		seen[e.Code] = true
		out.WriteString(err.Error())
		out.WriteString("\n")
	}
	golden(t, "testdata/errors.golden", out.String())

	for _, code := range diag.Codes {
		if (strings.HasPrefix(string(code), "LX") || strings.HasPrefix(string(code), "PA")) && !seen[code] {
			t.Errorf("no error case produces %s; add one to testdata/errors.txt", code)
		}
	}
}
