// Package check type-checks the templates of one package against their
// component signatures and the Go types they use.
//
// The checker reports every error it finds, not just the first: unlike
// syntax errors, type errors rarely cascade, and an invalid expression is
// given the Invalid type, which is compatible with everything.
package check

import (
	"fmt"
	goast "go/ast"
	"go/types"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/moomerman/loom/compiler/ast"
	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/compiler/goinfo"
	"github.com/moomerman/loom/compiler/source"
	"github.com/moomerman/loom/compiler/style"
)

// Component is a checked component declaration.
type Component struct {
	Name   string // PascalCase, as declared
	Decl   *ast.Component
	File   *ast.File
	Params []*Param
	// RootLayout is set for a root layout: a component with an <html>
	// element, which may use <.live_title>, <.loom_styles /> and
	// <.loom_scripts />.
	RootLayout bool
	// Class is the scope class added to the component's elements, and CSS
	// its compiled <style> block; both are empty if it has none. Unsafe
	// is set for <style unsafe>.
	Class  string
	CSS    string
	Unsafe bool
	// Behaviors are the behaviors its markup attaches with use:, so its
	// pages load their modules.
	Behaviors []*Behavior
	// Forms is set when its generated code names a form type, in a
	// parameter or an inline if, so the file imports the form package.
	Forms bool
}

// Import is a template package another package imports: its import path,
// Go package name and checked components.
type Import struct {
	Path string
	Name string
	Info *Info
}

// Param returns the parameter with the given name, or nil.
func (c *Component) Param(name string) *Param {
	for _, p := range c.Params {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func (c *Component) paramNames() []string {
	var names []string
	for _, p := range c.Params {
		names = append(names, p.Name)
	}
	return names
}

// Param is a component parameter. GoName is its field in the generated
// assigns struct: todos -> Todos, on:click -> OnClick.
type Param struct {
	Name    string
	GoName  string
	Type    *Type
	Default ast.Expr // the value a call that leaves it out gets, or nil
	// Previews are sample values for the preview gallery (@preview).
	Previews []ast.Expr
}

// VarName is the parameter's name in expressions: its name, or for an
// event parameter on:click, on_click.
func (p *Param) VarName() string { return strings.Replace(p.Name, ":", "_", 1) }

// Optional reports whether a call may leave the parameter out: it has a
// default, or it is a slot, an event or a form field (no content, no
// handler, no field).
func (p *Param) Optional() bool {
	return p.Default != nil || p.Type.Kind == Slot || p.Type.Kind == Event || p.Type.Kind == FormField
}

// events reports whether the component has an event parameter.
func (c *Component) events() bool {
	for _, p := range c.Params {
		if p.Type.Kind == Event {
			return true
		}
	}
	return false
}

// Var is a name in scope: a component parameter or a :for variable.
type Var struct {
	Name  string
	Type  *Type
	Param *Param // nil for a :for variable
}

// Helper is a function templates can call: a builtin or a Go function
// marked //loom:helper.
type Helper struct {
	Name    string // template name, e.g. format_date
	GoName  string // Go name, e.g. FormatDate; empty for builtins
	Builtin bool
	Params  []*Type
	Result  *Type
}

// Info is the result of checking: the components, and what each name,
// field, call and expression in them refers to. The code generator works
// from this.
type Info struct {
	Package    string // Go package name
	PkgPath    string // Go import path, or the name if it isn't known
	Components []*Component
	Types      map[ast.Expr]*Type
	Vars       map[*ast.Ident]*Var
	LoopVars   map[*ast.For]*Var
	Fields     map[*ast.Field]*Field
	Helpers    map[*ast.Call]*Helper
	Calls      map[*ast.ComponentCall]*Component
	// Imported maps each imported component the package calls to the
	// package it comes from. Components missing here are the package's own.
	Imported map[*Component]*Import
	// Tokens is set when the package has design tokens and styles that
	// use them, so its pages need the tokens' stylesheet.
	Tokens bool
	// IsPackage is set for a package shipped to other apps (a
	// //loom:package line): its tokens are defaults the host's override.
	IsPackage bool
	// Behaviors are the package's behavior declarations.
	Behaviors []*Behavior
	// Uses are the elements with use:, and the behavior each attaches.
	Uses map[*ast.Element]*UseInfo
	// ImportedBehaviors maps each imported behavior the package uses to the
	// package it comes from.
	ImportedBehaviors map[*Behavior]*Import
}

// Config is what the checker needs to know beyond the templates and their
// Go package.
type Config struct {
	// Tokens are the app's design tokens (tokens.toml), or nil.
	Tokens *style.Tokens
	// PkgPath identifies the package in scope classes: its import path,
	// or its name if the import path isn't known.
	PkgPath string
	// Imports are the template packages the files import, by import
	// path. A path missing here couldn't be found or checked.
	Imports map[string]*Import
}

// Check checks the parsed templates of one package. pkg describes the Go
// files in the same directory.
func Check(files []*ast.File, pkg *goinfo.Package, cfg Config) (*Info, diag.List) {
	if cfg.PkgPath == "" {
		cfg.PkgPath = pkg.Name
	}
	c := &checker{
		cfg: cfg,
		pkg: pkg,
		info: &Info{
			Package:   pkg.Name,
			PkgPath:   cfg.PkgPath,
			IsPackage: pkg.IsPackage,
			Types:     map[ast.Expr]*Type{},
			Vars:      map[*ast.Ident]*Var{},
			LoopVars:  map[*ast.For]*Var{},
			Fields:    map[*ast.Field]*Field{},
			Helpers:   map[*ast.Call]*Helper{},
			Calls:     map[*ast.ComponentCall]*Component{},
			Imported:  map[*Component]*Import{},

			Uses:              map[*ast.Element]*UseInfo{},
			ImportedBehaviors: map[*Behavior]*Import{},
		},
		structs:        map[string]*StructType{},
		helpers:        map[string]*Helper{},
		components:     map[string]*Component{},
		behaviors:      map[string]*Behavior{},
		behaviorImport: map[*Behavior]*Import{},
		fileBehaviors:  map[*ast.File]map[string]*Behavior{},
	}
	c.loadHelpers()
	for _, f := range files {
		c.declare(f)
		c.declareBehaviors(f)
	}
	for _, f := range files {
		c.resolveImports(f)
		c.fileBehaviors[f] = c.resolveBehaviorImports(f)
	}
	for _, comp := range c.info.Components {
		c.file = comp.File.Source
		c.imports = c.fileImports[comp.File]
		c.importedBehaviors = c.fileBehaviors[comp.File]
		c.comp, c.builtinsSeen = comp, map[string]bool{}
		sc := &scope{vars: map[string]*Var{}}
		for _, p := range comp.Params {
			sc.vars[p.VarName()] = &Var{Name: p.VarName(), Type: p.Type, Param: p}
		}
		c.styles(comp)
		c.nodes(comp.Decl.Body, sc)
		if comp.RootLayout {
			c.rootLayout(comp)
		}
		c.info.Tokens = c.info.Tokens || (cfg.Tokens != nil && comp.CSS != "")
	}
	c.unusedImports(files)
	return c.info, c.errs
}

type checker struct {
	cfg  Config
	pkg  *goinfo.Package
	info *Info
	errs diag.List
	file *source.File // template being checked
	comp *Component   // component being checked
	// builtinsSeen records the builtin components comp calls.
	builtinsSeen map[string]bool
	structs      map[string]*StructType
	helpers      map[string]*Helper    // by template name
	components   map[string]*Component // by snake_case name, as called
	// fileImports are the components each file imports, by snake_case
	// name, and imports those of the file being checked.
	fileImports map[*ast.File]map[string]*Component
	imports     map[string]*Component
	used        map[*ast.File]map[*Import]bool // imports each file calls
	importOf    map[*Component]*Import
	// behaviors are the package's behaviors by name; fileBehaviors those
	// each file imports, and importedBehaviors those of the file being
	// checked.
	behaviors         map[string]*Behavior
	fileBehaviors     map[*ast.File]map[string]*Behavior
	importedBehaviors map[string]*Behavior
	behaviorImport    map[*Behavior]*Import
}

type scope struct {
	parent *scope
	vars   map[string]*Var
}

func (s *scope) lookup(name string) *Var {
	for ; s != nil; s = s.parent {
		if v, ok := s.vars[name]; ok {
			return v
		}
	}
	return nil
}

func (s *scope) names() []string {
	var names []string
	for ; s != nil; s = s.parent {
		for n := range s.vars {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

func (c *checker) errorAt(code diag.Code, start, end int, format string, args ...any) *diag.Error {
	e := diag.Errorf(code, c.file, start, end, format, args...)
	c.errs = append(c.errs, e)
	return e
}

func (c *checker) warnAt(code diag.Code, start, end int, format string, args ...any) *diag.Error {
	e := diag.Warnf(code, c.file, start, end, format, args...)
	c.errs = append(c.errs, e)
	return e
}

func (c *checker) errorExpr(code diag.Code, e ast.Expr, format string, args ...any) *diag.Error {
	start, end := span(e)
	return c.errorAt(code, start, end, format, args...)
}

// --- Declarations ---

var builtins = map[string]bool{"len": true, "raw": true, "render_slot": true, "path": true, "asset": true}

func (c *checker) loadHelpers() {
	names := make([]string, 0, len(c.pkg.Helpers))
	for n := range c.pkg.Helpers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, goName := range names {
		fn := c.pkg.Helpers[goName]
		name := SnakeCase(goName)
		f := source.NewFile(fn.File, c.pkg.Files[fn.File])
		fail := func(format string, args ...any) *diag.Error {
			e := diag.Errorf(diag.TC326, f, fn.Offset, fn.Offset+len(goName), format, args...)
			c.errs = append(c.errs, e)
			return e
		}
		if builtins[name] {
			fail("helper %s clashes with the builtin %s", goName, name).
				WithFix("rename the Go function; builtins are len, raw, render_slot, path and asset")
			continue
		}
		h := &Helper{Name: name, GoName: goName}
		ok := true
		for _, p := range fn.Params {
			t := c.goType(p)
			if t == nil {
				fail("helper %s has a parameter of type %s, which templates can't pass", goName, types.ExprString(p)).
					WithFix("use parameters of type string, int, float64, bool, a struct from this package, or a slice of those")
				ok = false
				break
			}
			h.Params = append(h.Params, t)
		}
		if !ok {
			continue
		}
		if len(fn.Results) != 1 {
			fail("helper %s must return exactly one value", goName).
				WithFix("helpers are pure functions from arguments to one result; move errors and side effects into Go code")
			continue
		}
		if h.Result = c.goType(fn.Results[0]); h.Result == nil {
			fail("helper %s returns %s, which templates can't use", goName, types.ExprString(fn.Results[0])).
				WithFix("return string, int, float64, bool, a struct from this package, or a slice of those")
			continue
		}
		c.helpers[name] = h
	}
}

func (c *checker) declare(f *ast.File) {
	c.file = f.Source
	for _, decl := range f.Components {
		name := SnakeCase(decl.Name)
		if other, ok := c.components[name]; ok {
			c.errorAt(diag.TC325, decl.Pos.Offset, decl.Pos.Offset+len(decl.Name), "component %s is also declared in %s", decl.Name, other.File.Source.Name).
				WithFix("component names must be unique within a package; rename one of them")
			continue
		}
		if builtinComponents[name] {
			c.errorAt(diag.TC334, decl.Pos.Offset, decl.Pos.Offset+len(decl.Name), "component name %s is taken by the builtin <.%s>", decl.Name, name).
				WithFix("rename the component")
			continue
		}
		for _, goName := range []string{decl.Name, decl.Name + "Assigns"} {
			if _, ok := c.pkg.Structs[goName]; ok {
				c.errorAt(diag.TC328, decl.Pos.Offset, decl.Pos.Offset+len(decl.Name), "component %s generates the Go name %s, which the package already declares as a type", decl.Name, goName).
					WithFix("rename the component or the Go type; a component becomes a Go value %s and a type %sAssigns", decl.Name, decl.Name)
			}
		}
		comp := &Component{Name: decl.Name, Decl: decl, File: f, RootLayout: isRootLayout(decl.Body)}
		for _, p := range decl.Params {
			goName := PascalCase(strings.Replace(p.Name, ":", "_", 1))
			var t *Type
			if e, ok := p.Type.(*ast.EnumType); ok {
				t = &Type{Kind: Enum, Enum: &EnumType{GoName: decl.Name + goName, Values: e.Values}}
			} else {
				t = c.sigType(p.Type)
			}
			prm := &Param{Name: p.Name, GoName: goName, Type: t, Default: p.Default, Previews: p.Previews}
			comp.Forms = comp.Forms || IsForm(t)
			c.paramDefault(prm)
			c.paramPreviews(prm)
			comp.Params = append(comp.Params, prm)
		}
		c.components[name] = comp
		c.info.Components = append(c.info.Components, comp)
	}
}

// paramDefault checks that a parameter's default fits its type.
func (c *checker) paramDefault(p *Param) {
	d := p.Default
	if d == nil || p.Type.Kind == Invalid {
		return
	}
	start, end := span(d)
	if a, ok := d.(*ast.Atom); ok {
		end = a.Pos.Offset + 1 + len(a.Name)
	}
	var ok bool
	switch p.Type.Kind {
	case Enum:
		a := d.(*ast.Atom)
		if !p.Type.Enum.Has(a.Name) {
			c.enumValueError(start, end, p, a.Name)
		}
		return
	case String:
		_, ok = d.(*ast.StringLit)
	case Bool:
		_, ok = d.(*ast.BoolLit)
	case Int:
		ok = isIntLit(d)
	case Float:
		_, isFloat := d.(*ast.FloatLit)
		if u, neg := d.(*ast.Unary); neg {
			_, isFloat = u.X.(*ast.FloatLit)
		}
		ok = isFloat || isIntLit(d)
	default:
		c.errorAt(diag.TC339, start, end, "%s is %s, which can't have a default", p.Name, p.Type).
			WithFix("remove the default; a %s parameter is required, or optional only when it is a slot", p.Type)
		return
	}
	if !ok {
		c.errorAt(diag.TC339, start, end, "the default of %s must be %s %s", p.Name, article(p.Type.String()), p.Type).
			WithFix("write %s %s literal, e.g. %s", article(p.Type.String()), p.Type, exampleLiteral(p.Type))
	}
}

// paramPreviews checks a parameter's @preview values: literals of its
// type, or text for a slot. Enums and bools need none, since previews
// show every value.
func (c *checker) paramPreviews(p *Param) {
	if len(p.Previews) == 0 || p.Type.Kind == Invalid {
		return
	}
	first, _ := span(p.Previews[0])
	switch p.Type.Kind {
	case Enum, Bool:
		c.errorAt(diag.TC347, first, first+1, "%s is %s, so previews already show each of its values", p.Name, p.Type).
			WithFix("remove @preview from %s", p.Name)
		return
	case String, Int, Float, Slot:
	default:
		c.errorAt(diag.TC347, first, first+1, "%s is %s, which @preview can't give a value", p.Name, p.Type).
			WithFix("@preview takes strings, numbers and slot text; to preview a component that needs %s, wrap it in one that builds the value", p.Type)
		return
	}
	for _, x := range p.Previews {
		var ok bool
		switch p.Type.Kind {
		case String, Slot:
			_, ok = x.(*ast.StringLit)
		case Int:
			ok = isIntLit(x)
		case Float:
			_, isFloat := x.(*ast.FloatLit)
			if u, neg := x.(*ast.Unary); neg {
				_, isFloat = u.X.(*ast.FloatLit)
			}
			ok = isFloat || isIntLit(x)
		}
		if !ok {
			start, end := span(x)
			want := p.Type.String()
			if p.Type.Kind == Slot {
				want = "markup (string)"
			}
			c.errorAt(diag.TC347, start, end, "the @preview values of %s must be %s literals", p.Name, want).
				WithFix("write values like %s", previewExample(p.Type))
		}
	}
}

func previewExample(t *Type) string {
	switch t.Kind {
	case Int:
		return "@preview(1, 20)"
	case Float:
		return "@preview(0.5, 2.0)"
	}
	if t.Kind == Slot {
		return `@preview("<p>Some content</p>")`
	}
	return `@preview("Save", "A longer label")`
}

func article(word string) string {
	if strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}

func isIntLit(e ast.Expr) bool {
	if u, ok := e.(*ast.Unary); ok {
		e = u.X
	}
	_, ok := e.(*ast.IntLit)
	return ok
}

func exampleLiteral(t *Type) string {
	switch t.Kind {
	case String:
		return `""`
	case Bool:
		return "false"
	case Int:
		return "0"
	}
	return "0.0"
}

// enumValueError reports a value that isn't one of p's enum values.
func (c *checker) enumValueError(start, end int, p *Param, v string) {
	e := c.errorAt(diag.TC338, start, end, "%s isn't one of %s's values: %s", strconv.Quote(v), p.Name, p.Type.Enum.List())
	if s := closest(v, p.Type.Enum.Values); s != "" {
		e.WithFix("did you mean %q?", s)
	} else {
		e.WithFix("use one of %s, or add the value to the enum", p.Type.Enum.List())
	}
}

// resolveImports finds the components each imported package offers the
// file. A name that the package or another import also declares is an
// error: calls are unqualified, so each name must mean one component.
func (c *checker) resolveImports(f *ast.File) {
	c.file = f.Source
	if c.fileImports == nil {
		c.fileImports = map[*ast.File]map[string]*Component{}
		c.used = map[*ast.File]map[*Import]bool{}
		c.importOf = map[*Component]*Import{}
	}
	c.used[f] = map[*Import]bool{}
	comps := map[string]*Component{}
	c.fileImports[f] = comps
	for _, imp := range f.Imports {
		start, end := imp.Pos.Offset, imp.Pos.Offset+len(strconv.Quote(imp.Path))
		pkg := c.cfg.Imports[imp.Path]
		if pkg == nil {
			c.errorAt(diag.TC342, start, end, "can't find a template package at %q", imp.Path).
				WithFix("import a directory of .hx files in this module by its full import path: the module path from go.mod, then the directory; loom components add creates components/")
			continue
		}
		if pkg.Info == nil {
			c.errorAt(diag.TC342, start, end, "package %q has template errors", imp.Path).
				WithFix("run loom check on it and fix them first")
			continue
		}
		for _, comp := range pkg.Info.Components {
			name := SnakeCase(comp.Name)
			if local := c.components[name]; local != nil {
				c.used[f][pkg] = true // reported here, not as unused
				c.errorAt(diag.TC342, start, end, "%q exports component %s, which this package also declares", imp.Path, comp.Name).
					WithFix("rename the local component %s; calls aren't qualified, so each name must mean one component", local.Name)
				continue
			}
			if other := comps[name]; other != nil {
				c.used[f][pkg] = true
				c.errorAt(diag.TC342, start, end, "%q and %q both export component %s", c.importOf[other].Path, imp.Path, comp.Name).
					WithFix("import only one of them in this file")
				continue
			}
			comps[name] = comp
			c.importOf[comp] = pkg
		}
	}
}

// unusedImports reports imports no component in their file calls.
func (c *checker) unusedImports(files []*ast.File) {
	for _, f := range files {
		c.file = f.Source
		for _, imp := range f.Imports {
			pkg := c.cfg.Imports[imp.Path]
			if pkg == nil || pkg.Info == nil || c.used[f][pkg] {
				continue
			}
			c.errorAt(diag.TC342, imp.Pos.Offset, imp.Pos.Offset+len(strconv.Quote(imp.Path)), "%q is imported but none of its components or behaviors is used", imp.Path).
				WithFix("remove the import")
		}
	}
}

// component returns the component called name: one of the package's, or
// one imported by the file being checked.
func (c *checker) component(name string) *Component {
	if comp := c.components[name]; comp != nil {
		return comp
	}
	if comp := c.imports[name]; comp != nil {
		pkg := c.importOf[comp]
		c.used[c.comp.File][pkg] = true
		c.info.Imported[comp] = pkg
		return comp
	}
	return nil
}

// sigType resolves a type written in a component signature.
func (c *checker) sigType(t ast.Type) *Type {
	switch t := t.(type) {
	case *ast.ListType:
		elem := c.sigType(t.Elem)
		if elem.Kind == Slot {
			c.errorAt(diag.TC304, t.Pos.Offset, t.Pos.Offset+len(ast.TypeString(t)), "a list of slots isn't supported").
				WithFix("declare a single slot parameter")
			return TInvalid
		}
		return ListOf(elem)
	case *ast.NamedType:
		if t.Name == "form" {
			return c.formType(t)
		}
		if t.Arg != nil {
			c.errorAt(diag.TC304, t.Pos.Offset, t.Pos.Offset+len(ast.TypeString(t)), "%s takes no type argument", t.Name).
				WithFix("only form takes one, as in form[SettleInput]")
			return TInvalid
		}
		if b, ok := basicTypes[t.Name]; ok {
			return b
		}
		if st := c.structType(t.Name); st != nil {
			return &Type{Kind: Struct, Struct: st}
		}
		e := c.errorAt(diag.TC304, t.Pos.Offset, t.Pos.Offset+len(t.Name), "unknown type %s", t.Name)
		switch t.Name {
		case "float64", "float32":
			e.WithFix("template floats are written float")
		case "int64", "int32", "uint", "uint64", "byte", "rune":
			e.WithFix("template integers are written int")
		default:
			pkg := c.pkg.Name
			if pkg == "" {
				pkg = "templates"
			}
			e.WithFix("types are string, int, float, bool, slot, or a struct declared in a .go file next to the template (package %s)", pkg)
		}
	}
	return TInvalid
}

// formType resolves form[Input]: a form of a struct declared in the
// package, every exported field of which a form can decode.
func (c *checker) formType(t *ast.NamedType) *Type {
	start, end := t.Pos.Offset, t.Pos.Offset+len(ast.TypeString(t))
	arg, ok := t.Arg.(*ast.NamedType)
	if !ok || arg.Arg != nil {
		e := c.errorAt(diag.TC304, start, end, "form needs the struct it decodes to, in brackets")
		e.WithFix("write form[SettleInput], naming a struct declared in a .go file next to the template")
		return TInvalid
	}
	st := c.structType(arg.Name)
	if st == nil {
		e := c.errorAt(diag.TC304, start, end, "unknown type %s in form[%s]", arg.Name, arg.Name)
		if s := closest(arg.Name, c.structNames()); s != "" {
			e.WithFix("did you mean form[%s]?", s)
		} else {
			e.WithFix("declare the form's input as a struct in a .go file next to the template (package %s)", c.pkg.Name)
		}
		return TInvalid
	}
	fields := 0
	for _, f := range st.Fields {
		if !isExported(f.GoName) {
			continue
		}
		fields++
		if formFieldKind(f.GoType) == Invalid {
			c.errorAt(diag.TC352, start, end, "field %s.%s has Go type %s, which a form can't decode", st.Name, f.GoName, f.GoType).
				WithFix("form fields are string, int, int64, float64, bool, map[string]string or map[string]bool; use a string and parse it in Validate")
		}
	}
	if fields == 0 {
		c.errorAt(diag.TC352, start, end, "%s has no exported fields, so its form has none", st.Name).
			WithFix("export the fields of %s: Amount, not amount", st.Name)
	}
	return &Type{Kind: Form, Struct: st}
}

// formFieldKind is the kind of a form's field of the given Go type: a
// FormField, FormEntries for a map, or Invalid if a form can't decode it.
func formFieldKind(goType string) Kind {
	switch goType {
	case "string", "int", "int64", "float64", "bool":
		return FormField
	case "map[string]string", "map[string]bool":
		return FormEntries
	}
	return Invalid
}

func isExported(goName string) bool { return goName != "" && goName[0] >= 'A' && goName[0] <= 'Z' }

// structNames lists the package's struct types, for did-you-mean.
func (c *checker) structNames() []string {
	var names []string
	for name := range c.pkg.Structs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// structType returns the template view of a Go struct declared in the
// package, or nil.
func (c *checker) structType(name string) *StructType {
	if st, ok := c.structs[name]; ok {
		return st
	}
	gs := c.pkg.Structs[name]
	if gs == nil {
		return nil
	}
	st := &StructType{Name: name}
	c.structs[name] = st // before resolving fields, so recursive types work
	for _, f := range gs.Fields {
		st.Fields = append(st.Fields, &Field{Name: SnakeCase(f.Name), GoName: f.Name, Type: c.goType(f.Type), GoType: types.ExprString(f.Type)})
	}
	return st
}

// goType maps a Go type expression to a template type, or nil if it has
// no template equivalent.
func (c *checker) goType(e goast.Expr) *Type {
	switch e := e.(type) {
	case *goast.Ident:
		switch e.Name {
		case "string":
			return TString
		case "int":
			return TInt
		case "float64":
			return TFloat
		case "bool":
			return TBool
		}
		if st := c.structType(e.Name); st != nil {
			return &Type{Kind: Struct, Struct: st}
		}
	case *goast.SelectorExpr: // form.Field
		if x, ok := e.X.(*goast.Ident); ok && x.Name == "form" && e.Sel.Name == "Field" {
			return TField
		}
	case *goast.IndexExpr: // form.Form[Input]
		sel, ok := e.X.(*goast.SelectorExpr)
		arg, ok2 := e.Index.(*goast.Ident)
		if ok && ok2 && types.ExprString(sel) == "form.Form" {
			if st := c.structType(arg.Name); st != nil {
				return &Type{Kind: Form, Struct: st}
			}
		}
	case *goast.ArrayType:
		if e.Len == nil {
			if elem := c.goType(e.Elt); elem != nil {
				return ListOf(elem)
			}
		}
	}
	return nil
}

// --- Markup ---

func (c *checker) nodes(nodes []ast.Node, sc *scope) {
	for _, n := range nodes {
		c.node(n, sc)
	}
}

func (c *checker) node(n ast.Node, sc *scope) {
	switch n := n.(type) {
	case *ast.Interp:
		c.textValue(n.X, c.expr(n.X, sc))
	case *ast.Element:
		if strings.EqualFold(n.Tag, "style") {
			c.nestedStyle(n)
			return
		}
		inner := c.directives(&n.Directives, sc)
		b := c.use(n, inner)
		c.inlineScript(n)
		for _, a := range n.Attrs {
			if strings.HasPrefix(a.Name, "on:") {
				c.eventBinding(a, b)
			}
			switch {
			case isHint(a):
				c.hint(a, n.Attrs)
			case a.Kind == ast.AttrExpr && strings.HasPrefix(a.Name, "on:"):
				c.forwardEvent(a, inner)
			case a.Name == "class" && a.Kind == ast.AttrExpr:
				c.classValue(a.X, inner)
			case a.Name == "data-theme":
				c.themeAttr(a, inner)
			default:
				c.elementAttr(a, inner)
			}
		}
		c.nodes(n.Children, inner)
	case *ast.ComponentCall:
		c.call(n, c.directives(&n.Directives, sc))
	}
}

// directives checks :for, :if and :key, returning the scope for the tag's
// attributes and children.
func (c *checker) directives(d *ast.Directives, sc *scope) *scope {
	if d.For != nil {
		elem := TInvalid
		lt := c.expr(d.For.List, sc)
		switch lt.Kind {
		case List:
			elem = lt.Elem
		case Invalid:
		default:
			c.errorExpr(diag.TC309, d.For.List, ":for needs a list, but %s is %s", c.src(d.For.List), lt).
				WithFix("loop over a list, e.g. :for={item <- items} where items is a []T parameter or field")
		}
		if prev := sc.lookup(d.For.Var); prev != nil {
			c.errorAt(diag.TC328, d.For.VarPos.Offset, d.For.VarPos.Offset+len(d.For.Var), "%s is already defined", d.For.Var).
				WithFix("pick another name for the :for variable, so each name means one thing")
		}
		v := &Var{Name: d.For.Var, Type: elem}
		c.info.LoopVars[d.For] = v
		sc = &scope{parent: sc, vars: map[string]*Var{d.For.Var: v}}
	}
	if d.If != nil {
		c.condition(d.If, sc, ":if")
	}
	if d.For != nil && d.Key == nil {
		c.missingKey(d.For)
	}
	if d.Key != nil {
		if t := c.expr(d.Key, sc); t.Kind != String && t.Kind != Int && t.Kind != Invalid {
			c.errorExpr(diag.TC323, d.Key, ":key must be a string or int, but %s is %s", c.src(d.Key), t).
				WithFix("use a unique id field, e.g. :key={item.id}")
		}
	}
	return sc
}

// missingKey warns about a :for over records with no :key: without one,
// the patcher matches items by position, so removing the first item
// rewrites every row and moves focus and behaviors to the wrong item.
// Lists of strings and numbers aren't flagged: their values may repeat,
// and :key values must be unique.
func (c *checker) missingKey(f *ast.For) {
	v := c.info.LoopVars[f]
	if v == nil || v.Type.Kind != Struct {
		return
	}
	e := c.errorAt(diag.TC348, f.VarPos.Offset, f.VarPos.Offset+len(f.Var), ":for over %s has no :key, so items are matched by position", v.Type)
	e.Warning = true
	if slices.Contains(v.Type.Struct.FieldNames(), "id") {
		e.WithFix("add :key={%s.id}", f.Var)
	} else {
		e.WithFix("add :key with a field unique to each item, e.g. :key={%s.%s}", f.Var, v.Type.Struct.FieldNames()[0])
	}
}

func (c *checker) condition(e ast.Expr, sc *scope, what string) {
	if t := c.expr(e, sc); t.Kind != Bool && t.Kind != Invalid {
		err := c.errorExpr(diag.TC306, e, "%s needs a bool, but %s is %s", what, c.src(e), t)
		switch t.Kind {
		case List, String:
			err.WithFix("templates don't treat values as true or false; compare explicitly, e.g. len(%s) > 0", c.src(e))
		case Int, Float:
			err.WithFix("templates don't treat values as true or false; compare explicitly, e.g. %s > 0", c.src(e))
		default:
			err.WithFix("use a bool expression, e.g. a comparison")
		}
	}
}

// textValue checks that a value of type t can be rendered as text.
func (c *checker) textValue(e ast.Expr, t *Type) {
	switch t.Kind {
	case Slot:
		c.errorExpr(diag.TC324, e, "%s is a slot and can't be rendered directly", c.src(e)).
			WithFix("render it with {render_slot(%s)}", c.src(e))
	case List:
		c.errorExpr(diag.TC313, e, "%s is a list (%s) and can't be rendered as text", c.src(e), t).
			WithFix("render each item with :for, e.g. <li :for={item <- %s}>...</li>", c.src(e))
	case Struct:
		c.errorExpr(diag.TC313, e, "%s is a %s and can't be rendered as text", c.src(e), t).
			WithFix("render one of its fields: %s", strings.Join(t.Struct.FieldNames(), ", "))
	case Form, FormField, FormEntries:
		c.errorExpr(diag.TC313, e, "%s is a %s and can't be rendered as text", c.src(e), t).
			WithFix("%s", formFix(c.src(e), t))
	case Event:
		c.errorExpr(diag.TC340, e, "%s is an event parameter and can't be rendered", c.src(e)).
			WithFix("forward it to an element: <button %s={%s}>", strings.Replace(c.src(e), "_", ":", 1), c.src(e))
	}
}

// mountArg checks a literal argument of path or asset, which resolve
// against the mount prefix: a path starts with "/", an asset name doesn't.
func (c *checker) mountArg(name string, arg ast.Expr) {
	lit, ok := arg.(*ast.StringLit)
	if !ok {
		return
	}
	switch {
	case name == "path" && !strings.HasPrefix(lit.Value, "/"):
		c.errorExpr(diag.TC335, arg, "path(%s) must start with /: it is a path within the mount", c.src(arg)).
			WithFix(`write path(%q)`, "/"+lit.Value)
	case name == "path" && strings.HasPrefix(lit.Value, "//"):
		c.errorExpr(diag.TC335, arg, "path(%s) is a URL on another host, not a path within the mount", c.src(arg)).
			WithFix(`link to other hosts with a plain href="https://..."`)
	case name == "asset" && (lit.Value == "" || strings.HasPrefix(lit.Value, "/") || strings.Contains(lit.Value, "..")):
		c.errorExpr(diag.TC335, arg, "asset(%s) must name a file in the group's embedded assets, like \"app.css\"", c.src(arg)).
			WithFix(`write the file's name relative to the fs.FS given to loom.Assets, e.g. asset(%q)`, strings.TrimPrefix(strings.TrimLeft(lit.Value, "/."), "_assets/"))
	}
}

// urlAttrs are the attributes that hold a URL.
var urlAttrs = map[string]bool{"href": true, "src": true, "action": true, "formaction": true, "poster": true}

// bareURL warns about a static absolute URL in a reusable package's
// templates: it ignores the prefix the package is mounted at.
func (c *checker) bareURL(a *ast.Attr) {
	v := a.Value
	if !c.pkg.IsPackage || !urlAttrs[a.Name] || !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.HasPrefix(v, "/_loom/") {
		return
	}
	e := c.errorAt(diag.TC336, a.Pos.Offset, a.Pos.Offset+len(a.Name)+len(v)+3, "%s=%q ignores the prefix this package is mounted at", a.Name, v)
	e.Warning = true
	if strings.HasPrefix(v, "/_assets/") {
		e.WithFix(`write %s={asset(%q)}`, a.Name, strings.TrimPrefix(v, "/_assets/"))
	} else {
		e.WithFix(`write %s={path(%q)}, which resolves against the mount prefix`, a.Name, v)
	}
}

// dataScripts are <script> types that hold data, which a
// Content-Security-Policy doesn't block.
var dataScripts = map[string]bool{"application/json": true, "application/ld+json": true, "importmap": true}

// inlineScript warns about script the app's Content-Security-Policy
// blocks (docs/live.md): a <script> with no src, or an onclick-style
// handler attribute.
func (c *checker) inlineScript(n *ast.Element) {
	var src, typ *ast.Attr
	for _, a := range n.Attrs {
		name := strings.ToLower(a.Name)
		switch {
		case name == "src":
			src = a
		case name == "type":
			typ = a
		case len(name) > 2 && strings.HasPrefix(name, "on") && !strings.Contains(name, ":"):
			c.warnAt(diag.TC353, a.Pos.Offset, a.Pos.Offset+len(a.Name), "the inline handler %s won't run: the Content-Security-Policy blocks inline script", a.Name).
				WithFix("bind the event with on:%s=\"name\" and handle it in Go, or attach the code with a behavior (use:name), see docs/components.md", name[2:])
		}
	}
	if !strings.EqualFold(n.Tag, "script") || src != nil || (typ != nil && typ.Kind == ast.AttrString && dataScripts[strings.ToLower(typ.Value)]) {
		return
	}
	c.warnAt(diag.TC353, n.Pos.Offset, n.Pos.Offset+len("<script"), "this inline <script> won't run: the Content-Security-Policy blocks inline script").
		WithFix("move the code to a file the app serves and load it with <script src=...>, or write a behavior (use:name), see docs/components.md")
}

func (c *checker) elementAttr(a *ast.Attr, sc *scope) {
	if a.Kind == ast.AttrString {
		c.bareURL(a)
	}
	if a.Kind != ast.AttrExpr {
		return
	}
	t := c.expr(a.X, sc)
	switch t.Kind {
	case HTML:
		c.errorExpr(diag.TC314, a.X, "attribute %s can't take html", a.Name).
			WithFix("attribute values are text; pass a string without raw()")
	case Slot:
		c.errorExpr(diag.TC324, a.X, "%s is a slot and can't be an attribute value", c.src(a.X)).
			WithFix("render slots in the body with {render_slot(%s)}", c.src(a.X))
	case List, Struct:
		c.errorExpr(diag.TC314, a.X, "attribute %s can't take a %s", a.Name, t).
			WithFix("pass a string, int, float or bool, e.g. a field of %s", c.src(a.X))
	case Form, FormField, FormEntries:
		e := c.errorExpr(diag.TC314, a.X, "attribute %s can't take a %s", a.Name, t)
		if t.Kind == FormField && (a.Name == "name" || a.Name == "value") {
			e.WithFix("write %s={%s.%s}, or pass the field to a kit input: <.input field={%s} />", a.Name, c.src(a.X), a.Name, c.src(a.X))
		} else {
			e.WithFix("%s", formFix(c.src(a.X), t))
		}
	case Event:
		c.errorExpr(diag.TC340, a.X, "%s is an event parameter, so it goes in an on: attribute, not %s", c.src(a.X), a.Name).
			WithFix("write %s={%s}", strings.Replace(c.src(a.X), "_", ":", 1), c.src(a.X))
	}
}

// call checks a component call against the callee's signature.
func (c *checker) call(n *ast.ComponentCall, sc *scope) {
	if builtinComponents[n.Name] {
		c.builtinCall(n, sc)
		return
	}
	tagEnd := n.Pos.Offset + 2 + len(n.Name)
	comp := c.component(n.Name)
	if comp == nil {
		e := c.errorAt(diag.TC315, n.Pos.Offset, tagEnd, "unknown component <.%s>", n.Name)
		if n.Name == "link" { // HEEx's <.link navigate={...}>
			e.WithFix("Loom has no built-in <.link>: write <a href={...}>, which navigates live to pages with the same root layout")
		} else if s := closest(n.Name, c.componentNames()); s != "" {
			e.WithFix("did you mean <.%s>?", s)
		} else {
			e.WithFix("declare it in a .hx file in this package: component %s(...), or import the package that has it", PascalCase(n.Name))
		}
		for _, a := range n.Attrs {
			if a.Kind == ast.AttrExpr {
				c.expr(a.X, sc)
			}
		}
		for _, s := range n.Slots {
			c.nodes(s.Children, sc)
		}
		c.nodes(n.Children, sc)
		return
	}
	c.info.Calls[n] = comp

	given := map[string]bool{}
	for _, a := range n.Attrs {
		start, end := a.Pos.Offset, a.Pos.Offset+len(a.Name)
		if strings.HasPrefix(a.Name, "val-") {
			if !comp.events() {
				c.errorAt(diag.TC340, start, end, "%s is sent with an event, but component %s has no event parameter", a.Name, comp.Name).
					WithFix("declare one in %s's signature, e.g. on:click event, and forward it with on:click={on_click} on an element", comp.Name)
			}
			if a.Kind == ast.AttrExpr {
				c.elementAttr(a, sc)
			}
			continue
		}
		if isHint(a) {
			c.hint(a, n.Attrs)
			continue
		}
		p := comp.Param(a.Name)
		if p == nil {
			if strings.HasPrefix(a.Name, "on:") {
				c.errorAt(diag.TC327, start, end, "component %s has no event parameter %s", comp.Name, a.Name).
					WithFix("declare %s event in %s's signature and forward it with %s={%s} on an element inside it", a.Name, comp.Name, a.Name, (&Param{Name: a.Name}).VarName())
				continue
			}
			e := c.errorAt(diag.TC316, start, end, "component %s has no parameter %s", comp.Name, a.Name)
			if (a.Name == "navigate" || a.Name == "patch") && comp.Param("href") != nil { // HEEx's <.link navigate={...}>
				e.WithFix("pass the URL as href: in Loom every link navigates live to pages with the same root layout")
			} else if s := closest(a.Name, comp.paramNames()); s != "" {
				e.WithFix("did you mean %s? Its parameters are: %s", s, strings.Join(comp.paramNames(), ", "))
			} else {
				e.WithFix("its parameters are: %s", strings.Join(comp.paramNames(), ", "))
			}
			if a.Kind == ast.AttrExpr {
				c.expr(a.X, sc)
			}
			continue
		}
		given[p.Name] = true
		switch p.Type.Kind {
		case Slot:
			c.errorAt(diag.TC324, start, end, "%s is a slot, so it can't be passed as an attribute", p.Name).
				WithFix("pass markup as <:%s>...</:%s> inside the call", p.Name, p.Name)
			continue
		case Event:
			if a.Kind == ast.AttrExpr {
				c.forwardEvent(a, sc)
			}
			continue // the parser allows only on:x="name" and on:x={...}
		case Enum:
			c.enumArg(a, p, sc)
			continue
		}
		if a.Name == "class" && a.Kind == ast.AttrExpr {
			c.classValue(a.X, sc)
			continue
		}
		switch a.Kind {
		case ast.AttrString:
			if p.Type.Kind != String && p.Type.Kind != Invalid {
				c.errorAt(diag.TC318, start, end, "%s is %s, but a quoted value is a string", p.Name, p.Type).
					WithFix("pass an expression in braces: %s={...}", p.Name)
			}
		case ast.AttrBare:
			if p.Type.Kind != Bool && p.Type.Kind != Invalid {
				c.errorAt(diag.TC318, start, end, "%s is %s, but a bare attribute means true", p.Name, p.Type).
					WithFix("pass a value: %s={...}", p.Name)
			}
		case ast.AttrExpr:
			if t := c.expr(a.X, sc); !Identical(t, p.Type) {
				c.errorExpr(diag.TC318, a.X, "%s is %s, but %s is %s", p.Name, p.Type, c.src(a.X), t).
					WithFix("pass a value of type %s", p.Type)
			}
		}
	}

	for _, s := range n.Slots {
		start, end := s.Pos.Offset, s.Pos.Offset+2+len(s.Name)
		p := comp.Param(s.Name)
		switch {
		case p == nil || p.Type.Kind != Slot:
			c.errorAt(diag.TC319, start, end, "component %s has no slot %s", comp.Name, s.Name).
				WithFix("declare it in the signature (%s slot), or use one of its slots: %s", s.Name, strings.Join(slotNames(comp), ", "))
		case given[s.Name]:
			c.errorAt(diag.TC320, start, end, "slot %s is given twice", s.Name).
				WithFix("pass all of the markup in a single <:%s>", s.Name)
		}
		given[s.Name] = true
		if len(s.Attrs) > 0 {
			a := s.Attrs[0]
			c.errorAt(diag.TC321, a.Pos.Offset, a.Pos.Offset+len(a.Name), "named slots can't take attributes yet").
				WithFix("pass the value as a parameter of %s instead", comp.Name)
		}
		c.nodes(s.Children, sc)
	}

	if len(n.Children) > 0 {
		if p := comp.Param("inner_block"); p == nil || p.Type.Kind != Slot {
			c.errorAt(diag.TC319, n.Pos.Offset, tagEnd, "component %s doesn't take inner content", comp.Name).
				WithFix("add an inner_block slot parameter to %s and render it with {render_slot(inner_block)}, or remove the content", comp.Name)
		}
		c.nodes(n.Children, sc)
	}

	var missing []string
	for _, p := range comp.Params {
		if !p.Optional() && !given[p.Name] {
			missing = append(missing, p.Name)
		}
	}
	if len(missing) > 0 {
		c.errorAt(diag.TC317, n.Pos.Offset, tagEnd, "<.%s> is missing %s", n.Name, strings.Join(missing, ", ")).
			WithFix("pass every parameter without a default, e.g. %s={...}", missing[0])
	}
}

// enumArg checks a value passed to an enum parameter: a quoted value, or
// an expression of the same enum.
func (c *checker) enumArg(a *ast.Attr, p *Param, sc *scope) {
	start, end := a.Pos.Offset, a.Pos.Offset+len(a.Name)
	switch a.Kind {
	case ast.AttrString:
		if !p.Type.Enum.Has(a.Value) {
			c.enumValueError(start+len(a.Name)+1, start+len(a.Name)+3+len(a.Value), p, a.Value)
		}
	case ast.AttrBare:
		c.errorAt(diag.TC318, start, end, "%s is one of %s, but a bare attribute means true", p.Name, p.Type).
			WithFix(`pass a value: %s="%s"`, p.Name, p.Type.Enum.Values[0])
	case ast.AttrExpr:
		if lit, ok := a.X.(*ast.StringLit); ok {
			c.info.Types[a.X] = p.Type
			if !p.Type.Enum.Has(lit.Value) {
				s, e := span(a.X)
				c.enumValueError(s, e, p, lit.Value)
			}
			return
		}
		t := c.expr(a.X, sc)
		switch {
		case Identical(t, p.Type):
		case t.Kind == String:
			c.errorExpr(diag.TC338, a.X, "%s is a string, but %s takes one of %s", c.src(a.X), p.Name, p.Type).
				WithFix(`pass a value as written, e.g. %s="%s", or declare the parameter you pass from as an enum too`, p.Name, p.Type.Enum.Values[0])
		default:
			c.errorExpr(diag.TC318, a.X, "%s is one of %s, but %s is %s", p.Name, p.Type, c.src(a.X), t).
				WithFix(`pass a value as written, e.g. %s="%s"`, p.Name, p.Type.Enum.Values[0])
		}
	}
}

// forwardEvent checks on:x={...}, which forwards one of the component's
// event parameters: the handler its caller gave.
func (c *checker) forwardEvent(a *ast.Attr, sc *scope) {
	if t := c.expr(a.X, sc); t.Kind != Event && t.Kind != Invalid {
		c.errorExpr(diag.TC340, a.X, "%s={...} forwards an event parameter, but %s is %s", a.Name, c.src(a.X), t).
			WithFix(`name the server event in quotes, %s="save", or declare %s event in %s's signature and write %s={%s}`, a.Name, a.Name, c.comp.Name, a.Name, strings.Replace(a.Name, ":", "_", 1))
	}
}

// classValue checks a class attribute: a text value, or a list of them,
// whose inline ifs may leave out the else part. Class names are listed,
// never joined with +.
func (c *checker) classValue(x ast.Expr, sc *scope) {
	if c.joinedClass(x) {
		return
	}
	t := c.expr(x, sc)
	if !IsText(t) {
		c.errorExpr(diag.TC314, x, "class takes strings, but %s is %s", c.src(x), t).
			WithFix(`list class names: class={"card" size if selected, "selected"}`)
	}
}

// joinedClass reports class names joined with + rather than listed.
func (c *checker) joinedClass(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Binary:
		if x.Op == "+" {
			c.errorExpr(diag.TC341, x, "class names are listed, not joined with +").
				WithFix(`list each class as a value, e.g. class={"btn" size}, and style the combination (.btn.sm) in the component's <style>`)
			return true
		}
	case *ast.ClassList:
		found := false
		for _, item := range x.Items {
			found = c.joinedClass(item) || found
		}
		return found
	case *ast.Cond:
		return c.joinedClass(x.Then) || (x.Else != nil && c.joinedClass(x.Else))
	}
	return false
}

func slotNames(comp *Component) []string {
	var names []string
	for _, p := range comp.Params {
		if p.Type.Kind == Slot {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		return []string{"(none)"}
	}
	return names
}

func (c *checker) componentNames() []string {
	var names []string
	for n := range c.components {
		names = append(names, n)
	}
	for n := range c.imports {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// --- Expressions ---

// expr checks e and returns its type, recording it in Info.Types.
func (c *checker) expr(e ast.Expr, sc *scope) *Type {
	t := c.exprType(e, sc)
	c.info.Types[e] = t
	return t
}

func (c *checker) exprType(e ast.Expr, sc *scope) *Type {
	switch e := e.(type) {
	case *ast.StringLit:
		return TString
	case *ast.IntLit:
		return TInt
	case *ast.FloatLit:
		return TFloat
	case *ast.BoolLit:
		return TBool
	case *ast.Ident:
		v := sc.lookup(e.Name)
		if v == nil {
			err := c.errorExpr(diag.TC301, e, "unknown name %s", e.Name)
			if s := closest(e.Name, sc.names()); s != "" {
				err.WithFix("did you mean %s?", s)
			} else {
				err.WithFix("names in scope are the component's parameters and :for variables: %s", strings.Join(sc.names(), ", "))
			}
			return TInvalid
		}
		c.info.Vars[e] = v
		return v.Type
	case *ast.Field:
		return c.field(e, sc)
	case *ast.Index:
		xt, it := c.expr(e.X, sc), c.expr(e.Index, sc)
		if xt.Kind == FormEntries {
			if !IsText(it) {
				c.errorExpr(diag.TC308, e.Index, "a map field's key must be a string, but %s is %s", c.src(e.Index), it).
					WithFix("index with a string, e.g. %s[person.id]", c.src(e.X))
			}
			return TField
		}
		if xt.Kind != List && xt.Kind != Invalid {
			c.errorExpr(diag.TC308, e, "%s is %s, which can't be indexed", c.src(e.X), xt).
				WithFix("only lists can be indexed, e.g. items[0]")
			return TInvalid
		}
		if it.Kind != Int && it.Kind != Invalid {
			c.errorExpr(diag.TC308, e.Index, "list index must be an int, but %s is %s", c.src(e.Index), it).
				WithFix("index with an int, e.g. items[0]")
		}
		if xt.Kind == Invalid {
			return TInvalid
		}
		return xt.Elem
	case *ast.Call:
		return c.helperCall(e, sc)
	case *ast.Unary:
		xt := c.expr(e.X, sc)
		switch {
		case xt.Kind == Invalid:
			return TInvalid
		case e.Op == "!" && xt.Kind == Bool:
			return TBool
		case e.Op == "-" && (xt.Kind == Int || xt.Kind == Float):
			return xt
		}
		c.errorExpr(diag.TC305, e, "%s can't be applied to %s (%s)", e.Op, c.src(e.X), xt).
			WithFix("! needs a bool; - needs an int or float")
		return TInvalid
	case *ast.Binary:
		return c.binary(e, sc)
	case *ast.ClassList:
		for _, item := range e.Items {
			if t := c.expr(item, sc); !IsText(t) {
				c.errorExpr(diag.TC314, item, "class takes strings, but %s is %s", c.src(item), t).
					WithFix(`list class names: class={"card" size if selected, "selected"}`)
			}
		}
		return TString
	case *ast.Cond:
		c.condition(e.Cond, sc, "inline if")
		if e.Else == nil { // in a class list: the else value is ""
			tt := c.expr(e.Then, sc)
			if !IsText(tt) {
				return TInvalid
			}
			return TString
		}
		tt, et := c.expr(e.Then, sc), c.expr(e.Else, sc)
		if c.comp != nil && (IsForm(tt) || IsForm(et)) {
			c.comp.Forms = true // the inline if's Go func names the type
		}
		if !Identical(tt, et) {
			c.errorExpr(diag.TC307, e, "inline if branches have different types: %s and %s", tt, et).
				WithFix("make both branches the same type")
			return TInvalid
		}
		if tt.Kind == Invalid {
			return et
		}
		return tt
	}
	return TInvalid
}

func (c *checker) field(e *ast.Field, sc *scope) *Type {
	xt := c.expr(e.X, sc)
	start := e.Pos.Offset + 1 // the field name, after the dot
	end := start + len(e.Name)
	st := xt.Struct
	switch xt.Kind {
	case Invalid:
		return TInvalid
	case Struct:
	case Form:
		return c.formField(e, xt, start, end)
	case FormField:
		st = fieldStruct
	case FormEntries:
		c.errorAt(diag.TC303, start, end, "%s is a map field, which has an entry per key, not fields", c.src(e.X)).
			WithFix("index it with a key: %s[%s]", c.src(e.X), e.Name)
		return TInvalid
	default:
		c.errorAt(diag.TC303, start, end, "%s is %s, which has no fields", c.src(e.X), xt).
			WithFix("only structs, forms and form fields have fields")
		return TInvalid
	}
	f := st.Field(e.Name)
	if f == nil {
		err := c.errorAt(diag.TC302, start, end, "%s has no field %s", st.Name, e.Name)
		if g := goField(st, e.Name); g != nil {
			err.WithFix("template field names are snake_case: write %s.%s", c.src(e.X), g.Name)
		} else if s := closest(e.Name, st.FieldNames()); s != "" {
			err.WithFix("did you mean %s? %s's fields are: %s", s, st.Name, strings.Join(st.FieldNames(), ", "))
		} else {
			err.WithFix("%s's fields are: %s", st.Name, strings.Join(st.FieldNames(), ", "))
		}
		return TInvalid
	}
	if f.Type == nil {
		c.errorAt(diag.TC322, start, end, "field %s.%s has Go type %s, which templates can't use", st.Name, f.GoName, f.GoType).
			WithFix("templates can use string, int, float64, bool, structs from this package and slices of those; add a field with a usable type, e.g. a formatted string")
		return TInvalid
	}
	c.info.Fields[e] = f
	return f.Type
}

// formField checks a form's field, expense.amount: a field of its input
// struct, which the form decodes.
func (c *checker) formField(e *ast.Field, xt *Type, start, end int) *Type {
	st := xt.Struct
	var names []string
	for _, f := range st.Fields {
		if isExported(f.GoName) {
			names = append(names, f.Name)
		}
	}
	f := st.Field(e.Name)
	if f == nil || !isExported(f.GoName) {
		err := c.errorAt(diag.TC302, start, end, "form %s has no field %s", st.Name, e.Name)
		if g := goField(st, e.Name); g != nil && isExported(g.GoName) {
			err.WithFix("template field names are snake_case: write %s.%s", c.src(e.X), g.Name)
		} else if s := closest(e.Name, names); s != "" {
			err.WithFix("did you mean %s? the form's fields are %s's exported fields: %s", s, st.Name, strings.Join(names, ", "))
		} else {
			err.WithFix("add the field to %s, or use one of its exported fields: %s", st.Name, strings.Join(names, ", "))
		}
		return TInvalid
	}
	k := formFieldKind(f.GoType)
	if k == Invalid {
		return TInvalid // reported where the form type is declared
	}
	c.info.Fields[e] = f
	if k == FormEntries {
		return TEntries
	}
	return TField
}

// formFix says how to use a form value where it doesn't fit.
func formFix(src string, t *Type) string {
	switch t.Kind {
	case Form:
		for _, f := range t.Struct.Fields {
			if isExported(f.GoName) {
				return fmt.Sprintf("use one of its fields, e.g. <.input field={%s.%s} />", src, f.Name)
			}
		}
	case FormEntries:
		return fmt.Sprintf("index the map field with a key: %s[key]", src)
	}
	return fmt.Sprintf("use the field's name, value or error: {%s.value}", src)
}

// goField finds a field by its Go name, to explain snake_case names.
func goField(st *StructType, goName string) *Field {
	for _, f := range st.Fields {
		if f.GoName == goName && f.Name != goName {
			return f
		}
	}
	return nil
}

func (c *checker) binary(e *ast.Binary, sc *scope) *Type {
	xt, yt := c.expr(e.X, sc), c.expr(e.Y, sc)
	if xt.Kind == Invalid || yt.Kind == Invalid {
		switch e.Op {
		case "&&", "||", "==", "!=", "<", "<=", ">", ">=":
			return TBool
		}
		return TInvalid
	}
	mismatch := func(fix string) *Type {
		c.errorExpr(diag.TC305, e, "%s can't be applied to %s and %s", e.Op, xt, yt).WithFix("%s", fix)
		return TInvalid
	}
	same := Identical(xt, yt)
	switch e.Op {
	case "&&", "||":
		if xt.Kind != Bool || yt.Kind != Bool {
			return mismatch(fmt.Sprintf("%s needs bools on both sides; compare explicitly, e.g. len(x) > 0", e.Op))
		}
		return TBool
	case "==", "!=":
		if xt.Kind == Enum || yt.Kind == Enum {
			return c.enumCompare(e, xt, yt)
		}
		if !same || !isScalar(xt) {
			return mismatch("compare two values of the same type: string, int, float or bool")
		}
		return TBool
	case "<", "<=", ">", ">=":
		if !same || !(xt.Kind == Int || xt.Kind == Float || xt.Kind == String) {
			return mismatch("order two ints, two floats or two strings")
		}
		return TBool
	case "+":
		if !same || !(xt.Kind == Int || xt.Kind == Float || xt.Kind == String) {
			return mismatch("+ adds two ints or two floats, or joins two strings; both sides must have the same type")
		}
		return xt
	case "-", "*", "/":
		if !same || !(xt.Kind == Int || xt.Kind == Float) {
			return mismatch(fmt.Sprintf("%s needs two ints or two floats", e.Op))
		}
		return xt
	case "%":
		if !same || xt.Kind != Int {
			return mismatch("% needs two ints")
		}
		return TInt
	}
	return TInvalid
}

// enumCompare checks == and != with an enum: against the same enum, or a
// string literal that is one of its values.
func (c *checker) enumCompare(e *ast.Binary, xt, yt *Type) *Type {
	enum, other := xt, e.Y
	if xt.Kind != Enum {
		enum, other = yt, e.X
	}
	if Identical(xt, yt) {
		return TBool
	}
	if lit, ok := other.(*ast.StringLit); ok {
		c.info.Types[other] = enum
		if !enum.Enum.Has(lit.Value) {
			start, end := span(other)
			e := c.errorAt(diag.TC338, start, end, "%q isn't one of %s", lit.Value, enum.Enum.List())
			if s := closest(lit.Value, enum.Enum.Values); s != "" {
				e.WithFix("did you mean %q?", s)
			} else {
				e.WithFix("compare with one of %s", enum.Enum.List())
			}
		}
		return TBool
	}
	c.errorExpr(diag.TC305, e, "%s can't be applied to %s and %s", e.Op, xt, yt).
		WithFix(`compare an enum with one of its values as a string, e.g. %s == "%s"`, c.src(e.X), enum.Enum.Values[0])
	return TInvalid
}

func isScalar(t *Type) bool {
	switch t.Kind {
	case String, Int, Float, Bool, Enum:
		return true
	}
	return false
}

func (c *checker) helperCall(e *ast.Call, sc *scope) *Type {
	var args []*Type
	for _, a := range e.Args {
		args = append(args, c.expr(a, sc))
	}
	name := e.Func.Name
	h := c.builtin(name, args)
	if h == nil {
		h = c.helpers[name]
	}
	if h == nil {
		err := c.errorAt(diag.TC310, e.Func.Pos.Offset, e.Func.Pos.Offset+len(name), "unknown helper %s", name)
		if s := closest(name, c.helperNames()); s != "" {
			err.WithFix("did you mean %s?", s)
		} else {
			err.WithFix("templates can't call Go functions directly; declare a pure Go function %s in this package with a //loom:helper comment, or compute the value in Go and pass it in", PascalCase(name))
		}
		return TInvalid
	}
	c.info.Helpers[e] = h
	if h.Builtin && (name == "path" || name == "asset") && len(e.Args) == 1 {
		c.mountArg(name, e.Args[0])
	}
	if len(args) != len(h.Params) {
		c.errorExpr(diag.TC311, e, "%s takes %d %s, but got %d", name, len(h.Params), plural(len(h.Params), "argument"), len(args)).
			WithFix("call it as %s", signature(h))
		return h.Result
	}
	for i, a := range args {
		if !Identical(a, h.Params[i]) {
			c.errorExpr(diag.TC312, e.Args[i], "argument %d of %s must be %s, but %s is %s", i+1, name, h.Params[i], c.src(e.Args[i]), a).
				WithFix("call it as %s", signature(h))
		}
	}
	return h.Result
}

// builtin returns the builtin helper called name, typed for args, or nil.
// len accepts a list or a string, so its parameter type follows its argument.
func (c *checker) builtin(name string, args []*Type) *Helper {
	switch name {
	case "len":
		param := TString
		if len(args) == 1 && (args[0].Kind == List || args[0].Kind == Invalid) {
			param = args[0]
		}
		return &Helper{Name: name, Builtin: true, Params: []*Type{param}, Result: TInt}
	case "raw":
		return &Helper{Name: name, Builtin: true, Params: []*Type{TString}, Result: THTML}
	case "render_slot":
		return &Helper{Name: name, Builtin: true, Params: []*Type{TSlot}, Result: THTML}
	case "path", "asset":
		return &Helper{Name: name, Builtin: true, Params: []*Type{TString}, Result: TString}
	}
	return nil
}

func (c *checker) helperNames() []string {
	names := []string{"asset", "len", "path", "raw", "render_slot"}
	for n := range c.helpers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func signature(h *Helper) string {
	var params []string
	for _, p := range h.Params {
		params = append(params, p.String())
	}
	if h.Name == "len" {
		params = []string{"list or string"}
	}
	return h.Name + "(" + strings.Join(params, ", ") + ") " + h.Result.String()
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// src returns an expression's source text, for messages.
func (c *checker) src(e ast.Expr) string {
	start, end := span(e)
	return c.file.Text[start:min(end, len(c.file.Text))]
}

// span returns the byte range an expression covers, for underlining.
func span(e ast.Expr) (start, end int) {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Pos.Offset, e.Pos.Offset + len(e.Name)
	case *ast.StringLit:
		return e.Pos.Offset, e.Pos.Offset + len(strconv.Quote(e.Value))
	case *ast.IntLit:
		return e.Pos.Offset, e.Pos.Offset + len(strconv.FormatInt(e.Value, 10))
	case *ast.FloatLit:
		return e.Pos.Offset, e.Pos.Offset + len(strconv.FormatFloat(e.Value, 'g', -1, 64))
	case *ast.BoolLit:
		return e.Pos.Offset, e.Pos.Offset + len(strconv.FormatBool(e.Value))
	case *ast.Field:
		start, _ := span(e.X)
		return start, e.Pos.Offset + 1 + len(e.Name)
	case *ast.Index:
		start, _ := span(e.X)
		_, end := span(e.Index)
		return start, end + 1
	case *ast.Call:
		end := e.Func.Pos.Offset + len(e.Func.Name) + 2
		if len(e.Args) > 0 {
			_, end = span(e.Args[len(e.Args)-1])
			end++
		}
		return e.Func.Pos.Offset, end
	case *ast.Unary:
		_, end := span(e.X)
		return e.Pos.Offset, end
	case *ast.Binary:
		start, _ := span(e.X)
		_, end := span(e.Y)
		return start, end
	case *ast.Cond:
		last := e.Else
		if last == nil {
			last = e.Then
		}
		_, end := span(last)
		return e.Pos.Offset, end
	case *ast.ClassList:
		_, end := span(e.Items[len(e.Items)-1])
		return e.Pos.Offset, end
	}
	p := e.Position().Offset
	return p, p + 1
}

// closest returns the candidate most similar to name, if any is close.
// A candidate that has name as one of its words (due_on for due) wins
// over a nearer spelling (done), if it is the only such candidate.
func closest(name string, candidates []string) string {
	if w := wordMatch(name, candidates); w != "" {
		return w
	}
	best, bestDist := "", max(2, len(name)/3)+1
	for _, c := range candidates {
		if d := distance(name, c); d > 0 && d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// wordMatch returns the only candidate with name as one of its
// snake_case words, or "".
func wordMatch(name string, candidates []string) string {
	match := ""
	for _, c := range candidates {
		if c != name && slices.Contains(strings.Split(c, "_"), name) {
			if match != "" {
				return ""
			}
			match = c
		}
	}
	return match
}

// distance is the Levenshtein edit distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
