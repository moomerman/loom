package check_test

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/moomerman/loom/compiler"
	"github.com/moomerman/loom/compiler/diag"
)

var update = flag.Bool("update", false, "rewrite golden files")

type errorCase struct {
	name  string
	files map[string]string
}

// readCases reads testdata/errors.txt (format described at its top).
func readCases(t *testing.T) []*errorCase {
	data, err := os.ReadFile("testdata/errors.txt")
	if err != nil {
		t.Fatal(err)
	}
	var cases []*errorCase
	var file string
	for _, line := range strings.SplitAfter(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "-- ") && strings.HasSuffix(trimmed, " --"):
			cases = append(cases, &errorCase{name: strings.Trim(trimmed, "- "), files: map[string]string{}})
			file = "a.hx"
		case len(cases) > 0 && strings.HasPrefix(trimmed, "== ") && strings.HasSuffix(trimmed, " =="):
			file = strings.Trim(trimmed, "= ")
			cases[len(cases)-1].files[file] += "" // present, even if empty
		case len(cases) > 0:
			cases[len(cases)-1].files[file] += line
		}
	}
	return cases
}

// TestErrors checks each case in testdata/errors.txt and compares all
// reported errors with testdata/errors.golden.
func TestErrors(t *testing.T) {
	models, err := os.ReadFile("testdata/models.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := os.ReadFile("testdata/tokens.toml.txt")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	seen := map[diag.Code]bool{}
	for _, c := range readCases(t) {
		dir := t.TempDir()
		if _, ok := c.files["models.go"]; !ok {
			c.files["models.go"] = string(models)
		}
		if _, ok := c.files["tokens.toml"]; !ok && strings.HasPrefix(c.name, "ST") {
			c.files["tokens.toml"] = string(tokens)
		}
		var hx []string
		for name, text := range c.files {
			if strings.TrimSpace(text) == "" {
				continue
			}
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(name, ".hx") && !strings.Contains(name, "/") {
				hx = append(hx, path)
			}
		}
		sort.Strings(hx)
		_, errs, err := compiler.CheckPackage(dir, hx)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(errs) == 0 {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		if !strings.HasPrefix(c.name, string(errs[0].Code)) {
			t.Errorf("%s: first error is %s; name each case after the code it tests\n%v", c.name, errs[0].Code, errs[0])
		}
		out.WriteString("== " + c.name + "\n")
		for _, e := range errs {
			if e.Fix == "" {
				t.Errorf("%s: error %s has no fix", c.name, e.Code)
			}
			seen[e.Code] = true
			out.WriteString(strings.ReplaceAll(e.Error(), dir+string(filepath.Separator), ""))
		}
		out.WriteString("\n")
	}

	const golden = "testdata/errors.golden"
	if *update {
		os.WriteFile(golden, []byte(out.String()), 0o644)
	} else if want, err := os.ReadFile(golden); err != nil || string(want) != out.String() {
		t.Errorf("%s differs (run go test -update and review the diff):\n%s", golden, out.String())
	}

	for _, code := range diag.Codes {
		if (strings.HasPrefix(string(code), "TC") || strings.HasPrefix(string(code), "ST")) && !seen[code] {
			t.Errorf("no case produces %s; add one to testdata/errors.txt", code)
		}
	}
}
