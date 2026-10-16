// Package loomtest tests Loom components and pages without a browser.
//
//	func TestTodoList(t *testing.T) {
//		html := loomtest.Render(t, templates.TodoList, templates.TodoListAssigns{Todos: todos})
//		loomtest.Snapshot(t, html)
//	}
//
// Snapshots live in testdata/snapshots, named after the test. Run
// `go test -update` to create or update them, and review the diff.
//
// loomtest defines the -update flag; test packages that use loomtest
// should not define their own.
package loomtest

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/moomerman/loom/render"
)

var update = flag.Bool("update", false, "update loomtest snapshots")

// Render renders a component with the given assigns and returns its HTML.
// It fails the test if rendering panics or produces a malformed tree.
func Render[A any](t testing.TB, c render.Component[A], assigns A) string {
	t.Helper()
	r := RenderTree(t, c, assigns)
	return r.HTML()
}

// RenderTree renders a component and returns its render tree, checked for
// consistency: every block has one more static part than dynamics.
func RenderTree[A any](t testing.TB, c render.Component[A], assigns A) (r *render.Rendered) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("rendering %s panicked: %v", c.Name, p)
		}
	}()
	if err := checkGenerated(); err != nil {
		t.Fatal(err)
	}
	r = c.Render(nil, assigns)
	if err := ValidateTree(r); err != nil {
		t.Fatalf("rendering %s produced a malformed tree: %v", c.Name, err)
	}
	return r
}

// ValidateTree checks that a render tree is well formed.
func ValidateTree(r *render.Rendered) error {
	if r == nil || r.Statics == nil {
		return fmt.Errorf("missing statics")
	}
	return validateParts(r.Statics, r.Dynamics)
}

func validateParts(s *render.Statics, dyns []render.Dynamic) error {
	if len(s.Parts) != len(dyns)+1 {
		return fmt.Errorf("statics %s have %d parts for %d dynamics", s.ID, len(s.Parts), len(dyns))
	}
	for _, d := range dyns {
		switch d := d.(type) {
		case *render.Rendered:
			if err := ValidateTree(d); err != nil {
				return err
			}
		case *render.List:
			seen := map[string]bool{}
			for _, it := range d.Items {
				if seen[it.Key] {
					return fmt.Errorf("list %s has duplicate key %q; :key values must be unique", d.Statics.ID, it.Key)
				}
				seen[it.Key] = true
				if err := validateParts(d.Statics, it.Dynamics); err != nil {
					return err
				}
			}
		case nil:
			return fmt.Errorf("nil dynamic in %s", s.ID)
		}
	}
	return nil
}

var (
	mu        sync.Mutex
	snapCount = map[string]int{}
)

// Snapshot compares got with the test's snapshot file, or writes it when
// run with -update. A test may take several snapshots; they are numbered.
func Snapshot(t testing.TB, got string) {
	t.Helper()
	path := snapshotPath(t, "snapshots", ".html")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Fatalf("no snapshot %s; run go test -update to create it", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("snapshot %s differs (run go test -update to accept):\n%s", path, lineDiff(string(want), got))
	}
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// snapshotPath returns the path of the test's next snapshot with the
// given extension: testdata/<dir>/<test name>[.n]<ext>.
func snapshotPath(t testing.TB, dir, ext string) string {
	mu.Lock()
	defer mu.Unlock()
	name := unsafeChars.ReplaceAllString(t.Name(), "_")
	key := t.Name() + "/" + dir + ext
	snapCount[key]++
	n := snapCount[key]
	if n == 1 {
		t.Cleanup(func() { mu.Lock(); delete(snapCount, key); mu.Unlock() })
	}
	if n > 1 {
		name = fmt.Sprintf("%s.%d", name, n)
	}
	return filepath.Join("testdata", dir, name+ext)
}

// diff shows the first differing line of want and got, with context.
func lineDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(wl), len(gl)); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("first difference at line %d:\n  want: %q\n  got:  %q", i+1, w, g)
		}
	}
	return "(no line differs)"
}
