package diag

import (
	"os"
	"regexp"
	"testing"
)

// TestCodesDocumented keeps docs/errors.md in sync with Codes. Runtime
// (RT) codes are checked by the loom package.
func TestCodesDocumented(t *testing.T) {
	data, err := os.ReadFile("../../docs/errors.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^### ((?:LX|PA|TC|ST)\d{3})$`).FindAllStringSubmatch(string(data), -1) {
		documented[m[1]] = true
	}
	known := map[string]bool{}
	for _, c := range Codes {
		known[string(c)] = true
		if !documented[string(c)] {
			t.Errorf("%s is not documented in docs/errors.md", c)
		}
	}
	for c := range documented {
		if !known[c] {
			t.Errorf("docs/errors.md documents %s, which is not in diag.Codes", c)
		}
	}
}
