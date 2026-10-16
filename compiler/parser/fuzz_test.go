package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moomerman/loom/compiler/diag"
)

// FuzzParse checks that the parser never panics and that every failure is
// a *diag.Error with a code and a fix. Run with: go test -fuzz=FuzzParse
func FuzzParse(f *testing.F) {
	files, _ := filepath.Glob("testdata/ok/*.hx")
	for _, path := range files {
		src, _ := os.ReadFile(path)
		f.Add(string(src))
	}
	for _, c := range readErrorCases(&testing.T{}) {
		f.Add(c.src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		_, err := ParseString("fuzz.hx", src)
		if err == nil {
			return
		}
		e, ok := err.(*diag.Error)
		if !ok {
			t.Fatalf("error is %T, not *diag.Error: %v", err, err)
		}
		if e.Code == "" || e.Fix == "" {
			t.Fatalf("error without code or fix: %v", e)
		}
		_ = e.Error() // formatting must not panic either
	})
}
