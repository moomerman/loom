package main

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"text/template"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/compiler"
	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/data"
)

// `loom new` scaffolds an app: a module with one live page that builds
// and tests straight away, and the files an agent needs to work on it
// (AGENTS.md, CLAUDE.md, .mcp.json and a Claude Code skill). The files
// come from newapp/, text/templates named after the file they make with
// .tmpl added, so the go tool and loom check ignore them here.

//go:embed all:newapp
var newAppFiles embed.FS

const loomModule = "github.com/moomerman/loom"

// newOutput is the JSON form of `loom new --json`. See docs/cli.md.
type newOutput struct {
	Dir         string            `json:"dir"`
	Module      string            `json:"module"`
	Loom        string            `json:"loom"`  // how go.mod gets Loom: "version", "replace" or "missing"
	Files       []string          `json:"files"` // relative to dir, sorted
	Diagnostics []diag.Diagnostic `json:"diagnostics"`
	Warnings    []string          `json:"warnings"`
	Next        []string          `json:"next"` // commands to run next, from the parent of dir
}

// newApp is the data the templates in newapp/ see.
type newApp struct {
	Module      string   // the module path, e.g. example.com/myapp
	ModuleBase  string   // its last element, e.g. myapp
	Name        string   // the app's name, for titles: ModuleBase
	Package     string   // the Go package of the app's root directory
	Command     string   // the directory under cmd/ holding main
	LoomVersion string   // the version go.mod requires, or "" for none
	LoomDir     string   // the local checkout go.mod replaces Loom with, or ""
	LoomReqs    []string // with LoomDir, Loom's own requirements, e.g. "github.com/jackc/pgx/v5 v5.7.2"
}

func runNew(args []string, stdout, stderr io.Writer) int {
	fs := newCommand("new", "new [--json] [--module PATH] [--loom DIR] DIR", "print the result as JSON", stderr)
	asJSON := fs.JSON
	module := fs.String("module", "", "the Go module path (default: DIR's base name)")
	loomDir := fs.String("loom", "", "a local checkout of Loom for go.mod to use, with a replace directive")
	if !fs.parse(args, 1, 1) {
		return 2
	}
	out, err := scaffold(fs.Arg(0), *module, *loomDir)
	if err != nil {
		fmt.Fprintf(stderr, "loom new: %v\n", err)
		return 2
	}
	if *asJSON {
		writeJSON(stdout, out)
	} else {
		fmt.Fprintf(stdout, "created %s (module %s)\n", out.Dir, out.Module)
		for _, f := range out.Files {
			fmt.Fprintf(stdout, "  %s\n", f)
		}
		for _, d := range out.Diagnostics {
			fmt.Fprintln(stdout, d.Message)
		}
		for _, w := range out.Warnings {
			fmt.Fprintf(stdout, "warning: %s\n", w)
		}
		fmt.Fprintln(stdout, "\nNext:")
		for _, n := range out.Next {
			fmt.Fprintf(stdout, "  %s\n", n)
		}
		fmt.Fprintln(stdout, "\nAgents: start with AGENTS.md. Docs: loom docs getting-started.")
	}
	if len(out.Diagnostics) > 0 {
		return 1
	}
	return 0
}

// scaffold writes a new app into dir, which must not exist or be empty.
func scaffold(dir, module, loomDir string) (*newOutput, error) {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("%s is not empty (it has %s); give a new directory, or empty this one", dir, entries[0].Name())
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if module == "" {
		module = filepath.Base(abs)
	}
	if !validModule(module) {
		return nil, fmt.Errorf("%q isn't a usable module path; give one with --module, such as example.com/myapp (letters, digits and . _ ~ - / only)", module)
	}
	base := path.Base(module)
	app := newApp{Module: module, ModuleBase: base, Name: base, Package: packageName(base), Command: base}
	out := &newOutput{Dir: dir, Module: module, Files: []string{}, Diagnostics: []diag.Diagnostic{}, Warnings: []string{}}
	next := []string{"cd " + dir}

	// How the app gets Loom: a local checkout, the version of this loom
	// command, or nothing yet (a loom built from source has no version).
	switch v := loomVersion(); {
	case loomDir != "":
		d, err := loomCheckout(loomDir)
		if err != nil {
			return nil, err
		}
		reqs, err := loomRequires(d)
		if err != nil {
			return nil, err
		}
		app.LoomDir, app.LoomReqs = d, reqs
		app.LoomVersion = "v0.0.0-00010101000000-000000000000" // any version: the replace wins
		out.Loom = "replace"
	case v != "":
		app.LoomVersion = v
		out.Loom = "version"
	default:
		out.Loom = "missing"
		out.Warnings = append(out.Warnings, "this loom command has no version (it was built from source), so go.mod doesn't require Loom yet: run `go get "+loomModule+"@main`, or use --loom DIR with a local checkout")
		next = append(next, "go get "+loomModule+"@main")
	}

	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	written, err := writeNewApp(abs, app)
	if err != nil {
		return nil, err
	}
	out.Files = append(out.Files, written...)

	// Loom's own tables, as the app's first migrations, which `loom db
	// install` would copy.
	own, err := data.LoadMigrations(loom.Migrations())
	if err != nil {
		return nil, err
	}
	created, err := installMigrations(filepath.Join(abs, "migrations"), loomModule, own)
	if err != nil {
		return nil, err
	}
	for _, c := range created {
		rel, _ := filepath.Rel(abs, c.File)
		out.Files = append(out.Files, filepath.ToSlash(rel))
	}
	if app.LoomDir != "" {
		// Loom's go.sum covers the requirements copied from its go.mod, so
		// the app builds without the network.
		if sum, err := os.ReadFile(filepath.Join(app.LoomDir, "go.sum")); err == nil {
			if err := os.WriteFile(filepath.Join(abs, "go.sum"), sum, 0o644); err != nil {
				return nil, err
			}
			out.Files = append(out.Files, "go.sum")
		}
	}

	// Generate the *_hx.go files, so the app builds without running loom
	// build first.
	res, generated, err := compiler.Build(abs)
	if err != nil {
		return nil, err
	}
	out.Diagnostics = diagnostics(res.Diagnostics)
	for _, g := range generated {
		rel, err := filepath.Rel(abs, g)
		if err != nil {
			rel = g
		}
		out.Files = append(out.Files, filepath.ToSlash(rel))
	}

	// Fill in go.mod's requirements and go.sum. With a version this may
	// download modules; if it can't, the user runs it later. With a
	// checkout they are already complete: tidy would also want the test
	// dependencies of Loom's dependencies, which may not be downloaded.
	if out.Loom == "version" {
		if err := goModTidy(abs); err != nil {
			out.Warnings = append(out.Warnings, err.Error())
			next = append(next, "go mod tidy")
		} else if !contains(out.Files, "go.sum") {
			out.Files = append(out.Files, "go.sum")
		}
	} else if out.Loom == "missing" {
		next = append(next, "go mod tidy")
	}
	sort.Strings(out.Files)
	out.Next = append(next, "loom dev")
	return out, nil
}

// writeNewApp renders the templates in newapp/ into dir and returns the
// files written, relative to dir.
func writeNewApp(dir string, app newApp) ([]string, error) {
	var written []string
	err := fs.WalkDir(newAppFiles, "newapp", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		src, err := newAppFiles.ReadFile(name)
		if err != nil {
			return err
		}
		t, err := template.New(name).Option("missingkey=error").Parse(string(src))
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, app); err != nil {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(name, "newapp/"), ".tmpl")
		if strings.HasPrefix(rel, "cmd/app/") {
			rel = "cmd/" + app.Command + "/" + strings.TrimPrefix(rel, "cmd/app/")
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, buf.Bytes(), 0o644); err != nil {
			return err
		}
		written = append(written, rel)
		return nil
	})
	return written, err
}

// loomVersion returns the version of Loom this command was built from,
// when it is one go.mod can require: set by `go install ...@version`,
// and not "(devel)" or a local build's "+dirty" version.
func loomVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	v := ""
	if info.Main.Path == loomModule {
		v = info.Main.Version
	}
	for _, d := range info.Deps {
		if d.Path == loomModule && d.Replace == nil {
			v = d.Version
		}
	}
	if !strings.HasPrefix(v, "v") || strings.Contains(v, "+") {
		return ""
	}
	return v
}

// loomCheckout returns the absolute path of a Loom checkout, checking
// that its go.mod is Loom's.
func loomCheckout(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	mod, err := os.ReadFile(filepath.Join(abs, "go.mod"))
	if err != nil || !regexp.MustCompile(`(?m)^module\s+`+regexp.QuoteMeta(loomModule)+`\s*$`).Match(mod) {
		return "", fmt.Errorf("--loom %s isn't a checkout of Loom: it needs a go.mod for module %s", dir, loomModule)
	}
	return abs, nil
}

// requireLine matches a requirement in a go.mod, in a block or not.
var requireLine = regexp.MustCompile(`(?m)^(?:require\s+|\t)([\w./-]+\.[\w./-]+ v\S+)`)

// loomRequires returns the requirements in the go.mod of the Loom
// checkout dir, for an app that replaces Loom with it: since Go 1.17 a
// module lists the modules its dependencies need too.
func loomRequires(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	var reqs []string
	for _, m := range requireLine.FindAllStringSubmatch(string(b), -1) {
		reqs = append(reqs, m[1])
	}
	return reqs, nil
}

// goModTidy runs `go mod tidy` in dir.
func goModTidy(dir string) error {
	if _, err := exec.LookPath("go"); err != nil {
		return errors.New("go isn't on the PATH, so go.mod's requirements aren't filled in: install Go, then run `go mod tidy`")
	}
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod tidy failed, so go.mod's requirements may be incomplete; run it again when the cause is fixed:\n%s", strings.TrimSpace(string(out)))
	}
	return nil
}

var modulePath = regexp.MustCompile(`^[A-Za-z0-9._~-]+(/[A-Za-z0-9._~-]+)*$`)

// validModule reports whether a module path is simple enough to use: the
// go tool's rules, roughly, without needing golang.org/x/mod.
func validModule(m string) bool {
	return modulePath.MatchString(m) && !strings.HasPrefix(m, "-") && !strings.HasPrefix(m, ".")
}

// packageName turns a module's last element into a Go package name:
// lower-case letters and digits, not a keyword, and not a name main.go
// imports.
func packageName(base string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	name := b.String()
	switch {
	case name == "":
		return "web"
	case name[0] >= '0' && name[0] <= '9':
		return "app" + name
	case token.IsKeyword(name), name == "main", name == "loom", name == "log", name == "os":
		return name + "app"
	case name == "app", name == "addr", name == "port": // main.go's variables
		return name + "web"
	}
	return name
}
