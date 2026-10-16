package main

import (
	"strings"
	"testing"
)

// With no package, loom dev finds the app's main package: . if it is
// one, else the one in cmd/ that is marked, alone, or serves the app.
func TestFindDevMain(t *testing.T) {
	const (
		lib    = "package app\n"
		server = "package main\n\nimport \"github.com/moomerman/loom\"\n\nfunc main() { loom.New(loom.Config{}).Run(\":4000\") }\n"
		named  = "package main\n\nimport l \"github.com/moomerman/loom\"\n\nfunc main() { app := l.New(l.Config{}); app.Run(\":4000\") }\n"
		tool   = "package main\n\nfunc main() {}\n"
		marked = "package main\n\n//loom:dev\nfunc main() {}\n"
	)
	cases := []struct {
		name  string
		files map[string]string
		want  string // the package, or the error's text
	}{
		{"root main", map[string]string{"main.go": tool, "cmd/other/main.go": server}, "."},
		{"one in cmd", map[string]string{"app.go": lib, "cmd/retro/main.go": tool}, "./cmd/retro"},
		{"the one that serves", map[string]string{"app.go": lib, "cmd/import/main.go": tool, "cmd/web/main.go": server}, "./cmd/web"},
		{"loom imported by another name", map[string]string{"app.go": lib, "cmd/import/main.go": tool, "cmd/web/main.go": named}, "./cmd/web"},
		{"marked wins", map[string]string{"app.go": lib, "cmd/a/main.go": server, "cmd/b/main.go": server, "cmd/c/main.go": marked}, "./cmd/c"},
		{"tests and libraries skipped", map[string]string{"app.go": lib, "cmd/lib/lib.go": lib, "cmd/web/main.go": tool, "cmd/web/main_test.go": marked}, "./cmd/web"},
		{"none", map[string]string{"app.go": lib}, "RT451: loom dev found no main package"},
		{"ambiguous", map[string]string{"app.go": lib, "cmd/a/main.go": server, "cmd/b/main.go": server}, "RT451: loom dev can't tell which main package runs the app: ./cmd/a, ./cmd/b"},
		{"marked twice", map[string]string{"app.go": lib, "cmd/a/main.go": marked, "cmd/b/main.go": marked}, "RT451: more than one main package is marked //loom:dev: ./cmd/a, ./cmd/b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := findDevMain(writeFiles(t, c.files))
			if err != nil {
				got = err.Error()
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
