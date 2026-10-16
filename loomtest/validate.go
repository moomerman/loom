package loomtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/compiler"
	"github.com/moomerman/loom/render"
)

// Validate fails the test where the app would fail to start or serve
// stale code: it runs app.Validate, the checks app.Run makes before
// serving (jobs queues with no workers, watched tables that aren't tracked), and
// checks the generated *_hx.go files of every compiled template are what
// `loom build` writes now (RT421). Mount runs it, so most tests never
// call it; call it in a test of an app's setup:
//
//	func TestSetup(t *testing.T) {
//		app := loom.New()
//		todo.Setup(app)
//		loomtest.Validate(t, app)
//	}
func Validate(t testing.TB, app *loom.App) {
	t.Helper()
	if err := app.Validate(context.Background()); err != nil {
		t.Fatalf("the app would fail to start: %v", err)
	}
	if err := checkGenerated(); err != nil {
		t.Fatal(err)
	}
}

// The generated code is checked once per test binary: templates don't
// change while tests run.
var (
	generatedOnce sync.Once
	generatedErr  error
)

// checkGenerated rebuilds the templates of every package that registered
// components, in memory, and compares the result with the files on disk.
// `go test` compiles the *_hx.go files as they are, so an .hx edited
// without `loom build` would otherwise pass every test.
func checkGenerated() error {
	generatedOnce.Do(func() {
		var dirs []string
		for _, d := range render.SourceDirs() {
			// Packages in the module cache are other modules' committed
			// output, checked by their own tests.
			if !strings.Contains(filepath.ToSlash(d), "/pkg/mod/") {
				dirs = append(dirs, d)
			}
		}
		if len(dirs) == 0 {
			return
		}
		res, stale, err := compiler.Stale(dirs...)
		switch {
		case err != nil:
			generatedErr = fmt.Errorf("%s: checking the generated code: %v", loom.RT421, err)
		case res.Diagnostics.HasErrors():
			var msgs []string
			for _, d := range res.Diagnostics {
				if d.Severity() == "error" {
					msgs = append(msgs, strings.TrimSpace(d.Error()))
				}
			}
			generatedErr = fmt.Errorf("%s: the templates have errors, so the generated code is out of date; fix them and run `loom build`:\n%s", loom.RT421, strings.Join(msgs, "\n"))
		case len(stale) > 0:
			wd, _ := os.Getwd()
			var dirs []string
			for i, f := range stale {
				if rel, err := filepath.Rel(wd, f); err == nil {
					stale[i] = rel
				}
				if d := filepath.Dir(stale[i]); !slices.Contains(dirs, d) {
					dirs = append(dirs, d)
				}
			}
			generatedErr = fmt.Errorf("%s: generated code is out of date with its templates: %s; run `loom build %s` and commit the result", loom.RT421, strings.Join(stale, ", "), strings.Join(dirs, " "))
		}
	})
	return generatedErr
}
