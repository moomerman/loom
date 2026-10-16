// Package compiler ties the compiler stages together: it finds .hx
// templates, parses and type-checks them, and generates Go code. The CLI
// (`loom check`, `loom build`) and, later, the dev server and MCP server
// all go through here.
package compiler

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/moomerman/loom/compiler/ast"
	"github.com/moomerman/loom/compiler/check"
	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/compiler/gogen"
	"github.com/moomerman/loom/compiler/goinfo"
	"github.com/moomerman/loom/compiler/parser"
	"github.com/moomerman/loom/compiler/source"
	"github.com/moomerman/loom/compiler/style"
)

// Result is the outcome of checking a set of templates.
type Result struct {
	Packages    []*Package
	Checked     int // number of .hx files read
	Diagnostics diag.List
}

// Package is the templates of one directory, which form one Go package.
type Package struct {
	Dir    string
	Files  []*ast.File   // parsed templates
	Info   *check.Info   // nil if any template failed to parse
	Tokens *style.Tokens // the app's design tokens, or nil
}

// FindTemplates returns the .hx files under each path, sorted, each once
// when paths overlap. A path may name a file or a directory. Like the go tool, it skips directories named
// testdata or starting with '.' or '_'.
func FindTemplates(paths ...string) ([]string, error) {
	var files []string
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() {
				if path != root && (name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(name, ".hx") {
				files = append(files, filepath.Clean(path))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return slices.Compact(files), nil
}

// Check parses and type-checks every template under paths. The error is
// only for I/O problems; template errors are in the Result.
func Check(paths ...string) (*Result, error) {
	files, err := FindTemplates(paths...)
	if err != nil {
		return nil, err
	}
	byDir := map[string][]string{}
	var dirs []string
	for _, f := range files {
		dir := filepath.Dir(f)
		if byDir[dir] == nil {
			dirs = append(dirs, dir)
		}
		byDir[dir] = append(byDir[dir], f)
	}
	res := &Result{}
	ses := newSession()
	for _, dir := range dirs {
		pkg, errs, err := ses.checkPackage(dir, byDir[dir])
		if err != nil {
			return nil, err
		}
		res.Checked += len(byDir[dir])
		res.Packages = append(res.Packages, pkg)
		res.Diagnostics = append(res.Diagnostics, errs...)
	}
	return res, nil
}

// CheckPackage parses and type-checks the given templates, which must all
// be in dir, against the Go files in dir and the app's tokens.toml.
func CheckPackage(dir string, paths []string) (*Package, diag.List, error) {
	return newSession().checkPackage(dir, paths)
}

// session is one run of the checker. It reads each tokens.toml once, so
// its diagnostics are reported once however many packages use it, and
// checks each imported package once.
type session struct {
	tokens  map[string]*style.Tokens
	imports map[string]*check.Import // by directory; nil while being checked
	checked map[string]*checked      // packages checked, by absolute directory
}

type checked struct {
	pkg  *Package
	errs diag.List
}

func newSession() *session {
	return &session{tokens: map[string]*style.Tokens{}, imports: map[string]*check.Import{}, checked: map[string]*checked{}}
}

func (ses *session) loadTokens(dir string) (*style.Tokens, diag.List, error) {
	path := style.Find(dir)
	if path == "" {
		return nil, nil, nil
	}
	if t, ok := ses.tokens[path]; ok {
		return t, nil, nil
	}
	t, errs, err := style.Load(path)
	if err != nil {
		return nil, nil, err
	}
	ses.tokens[path] = t
	return t, errs, nil
}

// importPackage checks the template package imported by a package in
// from, or returns nil if there is none or it imports itself in a cycle.
func (ses *session) importPackage(from, path string) (*check.Import, error) {
	dir := goinfo.Dir(from, path)
	if dir == "" {
		return nil, nil
	}
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, dir); err == nil && !strings.HasPrefix(rel, "..") {
			dir = rel // as `loom check ./...` would name it, for messages
		}
	}
	if imp, ok := ses.imports[dir]; ok {
		return imp, nil
	}
	ses.imports[dir] = nil // a cycle finds nothing
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".hx") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	if len(paths) == 0 {
		return nil, nil
	}
	pkg, _, err := ses.checkPackage(dir, paths)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(dir)
	if pkg.Info != nil && pkg.Info.Package != "" {
		name = pkg.Info.Package
	}
	imp := &check.Import{Path: path, Name: name, Info: pkg.Info}
	ses.imports[dir] = imp
	return imp, nil
}

// checkPackage checks a package, or returns the result of checking it
// earlier in the session (as an import).
func (ses *session) checkPackage(dir string, paths []string) (*Package, diag.List, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	if c := ses.checked[abs]; c != nil {
		return c.pkg, c.errs, nil
	}
	pkg := &Package{Dir: dir}
	var errs diag.List
	for _, path := range paths {
		text, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		f, err := parser.Parse(source.NewFile(path, string(text)))
		if err != nil {
			errs = append(errs, diag.Errors(err)...)
			continue
		}
		pkg.Files = append(pkg.Files, f)
	}
	if len(errs) > 0 {
		return pkg, errs, nil // type errors would only be noise
	}
	gopkg, err := goinfo.Load(dir)
	if err != nil {
		return nil, nil, err
	}
	tokens, tokenErrs, err := ses.loadTokens(dir)
	if err != nil {
		return nil, nil, err
	}
	pkg.Tokens = tokens
	imports := map[string]*check.Import{}
	for _, f := range pkg.Files {
		for _, imp := range f.Imports {
			if _, ok := imports[imp.Path]; ok {
				continue
			}
			ip, err := ses.importPackage(dir, imp.Path)
			if err != nil {
				return nil, nil, err
			}
			if ip != nil {
				imports[imp.Path] = ip
			}
		}
	}
	info, errs := check.Check(pkg.Files, gopkg, check.Config{Tokens: tokens, PkgPath: goinfo.ImportPath(dir), Imports: imports})
	if info.Package == "" { // no Go files yet: the package is named after its directory
		abs, _ := filepath.Abs(dir)
		info.Package = filepath.Base(abs)
	}
	errs = append(tokenErrs, errs...)
	if !errs.HasErrors() {
		pkg.Info = info
	}
	ses.checked[abs] = &checked{pkg, errs}
	return pkg, errs, nil
}

// Build checks the templates under paths and, if there are no errors
// (warnings are fine), writes the generated Go and JavaScript files next
// to each template. It returns the files written.
func Build(paths ...string) (*Result, []string, error) {
	res, err := Check(paths...)
	if err != nil || res.Diagnostics.HasErrors() {
		return res, nil, err
	}
	outs, err := generate(res)
	if err != nil {
		return res, nil, err
	}
	var written []string
	for _, o := range outs {
		if o.src == nil {
			if err := os.Remove(o.path); err != nil && !os.IsNotExist(err) {
				return res, written, err
			}
			continue
		}
		if old, err := os.ReadFile(o.path); err == nil && bytes.Equal(old, o.src) {
			continue // unchanged; don't touch the file's modification time
		}
		if err := os.WriteFile(o.path, o.src, 0o644); err != nil {
			return res, written, err
		}
		written = append(written, o.path)
	}
	return res, written, nil
}

// Stale checks the templates under paths and returns the generated files
// that Build would change: written by an older build, or by none. It
// writes nothing. With template errors it returns them and no files.
func Stale(paths ...string) (*Result, []string, error) {
	res, err := Check(paths...)
	if err != nil || res.Diagnostics.HasErrors() {
		return res, nil, err
	}
	outs, err := generate(res)
	if err != nil {
		return res, nil, err
	}
	var stale []string
	for _, o := range outs {
		old, err := os.ReadFile(o.path)
		switch {
		case o.src == nil && err == nil:
			stale = append(stale, o.path) // Build would remove it
		case o.src != nil && (err != nil || !bytes.Equal(old, o.src)):
			stale = append(stale, o.path)
		}
	}
	return res, stale, nil
}

// output is a file Build generates; src is nil for a file it removes.
type output struct {
	path string
	src  []byte
}

// generate returns the files Build writes for checked templates.
func generate(res *Result) ([]output, error) {
	var outs []output
	for _, pkg := range res.Packages {
		for _, f := range pkg.Files {
			src, err := gogen.File(pkg.Info, f)
			if err != nil {
				return nil, err
			}
			outs = append(outs, output{gogen.OutputName(f.Source.Name), src})
		}
		outs = append(outs, output{filepath.Join(pkg.Dir, gogen.TokensFile), gogen.Tokens(pkg.Info, pkg.Tokens)})
	}
	return outs, nil
}
