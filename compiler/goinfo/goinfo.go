// Package goinfo reads what templates need to know about the Go package
// they live in: its name, its struct types and its helper functions.
//
// It reads Go source with go/parser only; it does not type-check or load
// dependencies, so it stays fast and stdlib-only. The cost is that only
// types declared in the template's own package are visible to templates.
package goinfo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GeneratedSuffix marks files written by the Loom code generator. They are
// skipped when reading a package, since they are derived from templates.
const GeneratedSuffix = "_hx.go"

// Package is the Go side of a template package.
type Package struct {
	Dir     string
	Name    string // Go package name; empty if the directory has no Go files
	Structs map[string]*Struct
	Helpers map[string]*Func  // by Go name
	Files   map[string]string // file name -> source, for positions in errors
	// IsPackage is set when a Go file has a //loom:package line: the
	// templates are a reusable package (a sub-app) that may be mounted
	// at any prefix, so the checker warns about bare absolute URLs.
	IsPackage bool
}

// Struct is a struct type declared in the package.
type Struct struct {
	Name   string
	Fields []*Field
}

// Field is a struct field. Type is the field's Go type as source text,
// e.g. "string", "[]Todo" or "time.Time".
type Field struct {
	Name string
	Type ast.Expr
}

// Func is a function marked with a `//loom:helper` comment.
type Func struct {
	Name    string
	Params  []ast.Expr // one entry per parameter
	Results []ast.Expr
	File    string
	Offset  int // byte offset of the function name in File
}

// HelperDirective marks a Go function as a template helper.
const HelperDirective = "//loom:helper"

// PackageDirective marks a template package as reusable: a sub-app or
// component library mounted by other apps. It goes in a Go file's
// comments, usually the package doc.
const PackageDirective = "//loom:package"

// Load reads the non-test, non-generated Go files in dir.
func Load(dir string) (*Package, error) {
	pkg := &Package{Dir: dir, Structs: map[string]*Struct{}, Helpers: map[string]*Func{}, Files: map[string]string{}}
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	fset := token.NewFileSet()
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, GeneratedSuffix) {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		f, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		pkg.Files[path] = string(src)
		if pkg.Name == "" {
			pkg.Name = f.Name.Name
		}
		pkg.addFile(fset, path, f)
		for _, cg := range f.Comments {
			if hasDirective(cg, PackageDirective) {
				pkg.IsPackage = true
			}
		}
	}
	return pkg, nil
}

func (pkg *Package) addFile(fset *token.FileSet, path string, f *ast.File) {
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.TypeParams != nil {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				s := &Struct{Name: ts.Name.Name}
				for _, fld := range st.Fields.List {
					for _, name := range fld.Names { // embedded fields have no names and are skipped
						s.Fields = append(s.Fields, &Field{Name: name.Name, Type: fld.Type})
					}
				}
				pkg.Structs[s.Name] = s
			}
		case *ast.FuncDecl:
			if d.Recv != nil || d.Type.TypeParams != nil || !hasDirective(d.Doc, HelperDirective) {
				continue
			}
			fn := &Func{Name: d.Name.Name, File: path, Offset: fset.Position(d.Name.Pos()).Offset}
			for _, p := range d.Type.Params.List {
				for range max(1, len(p.Names)) {
					fn.Params = append(fn.Params, p.Type)
				}
			}
			if d.Type.Results != nil {
				for _, r := range d.Type.Results.List {
					for range max(1, len(r.Names)) {
						fn.Results = append(fn.Results, r.Type)
					}
				}
			}
			pkg.Helpers[fn.Name] = fn
		}
	}
}

func hasDirective(doc *ast.CommentGroup, directive string) bool {
	if doc == nil {
		return false
	}
	for _, c := range doc.List {
		if strings.TrimSpace(c.Text) == directive {
			return true
		}
	}
	return false
}

// ImportPath returns the import path of the package in dir, from the
// module path in the nearest go.mod, or "" if there is no go.mod.
func ImportPath(dir string) string {
	root, mod := Module(dir)
	if root == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(mod+"/"+filepath.ToSlash(rel), "/.")
}

// Module returns the directory of the nearest go.mod at or above dir and
// its module path, or "" and "" if there is none.
func Module(dir string) (root, path string) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", ""
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if f := strings.Fields(line); len(f) == 2 && f[0] == "module" {
					return dir, strings.Trim(f[1], `"`)
				}
			}
			return "", ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}

// Dir returns the directory of the package with the given import path in
// the module containing from, or "" if the path is outside that module.
func Dir(from, importPath string) string {
	root, mod := Module(from)
	switch {
	case root == "":
		return ""
	case importPath == mod:
		return root
	case strings.HasPrefix(importPath, mod+"/"):
		return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(importPath, mod+"/")))
	}
	return ""
}
