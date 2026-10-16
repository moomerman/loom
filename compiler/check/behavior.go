package check

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/moomerman/loom/compiler/ast"
	"github.com/moomerman/loom/compiler/diag"
)

// Behavior is a checked behavior declaration: client-side code in an ES
// module next to the template, attached to elements with use:name.
type Behavior struct {
	Name   string // snake_case, as in use:name
	Decl   *ast.Behavior
	File   *ast.File
	Params []*Param // the props the module's mount and update receive
	Emits  []*Emit
	// ID names the behavior in the page, unique across packages: its
	// name and a hash of its package.
	ID string
	// GoName is the exported Go variable that holds it, e.g. DropdownBehavior.
	GoName string
	// JS is the module's source, from name.js beside the template.
	JS string
}

// Emit is an event a behavior may send, and its payload.
type Emit struct {
	Name   string
	Params []*Param
}

// Param returns the prop with the given name, or nil.
func (b *Behavior) Param(name string) *Param {
	for _, p := range b.Params {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// Emits reports whether the behavior may send event name.
func (b *Behavior) emits(name string) bool {
	for _, e := range b.Emits {
		if e.Name == name {
			return true
		}
	}
	return false
}

// EmitNames lists the events the behavior may send.
func (b *Behavior) EmitNames() []string {
	var names []string
	for _, e := range b.Emits {
		names = append(names, e.Name)
	}
	return names
}

func (b *Behavior) paramNames() []string {
	var names []string
	for _, p := range b.Params {
		names = append(names, p.Name)
	}
	return names
}

// UseInfo is what the code generator needs about an element's use:.
type UseInfo struct {
	Behavior *Behavior
	// Requires lists the requirements the checker couldn't decide because
	// the attribute's value is dynamic, as a CSS selector checked in the
	// browser in dev, or "".
	Requires string
}

// domEvents are the DOM events loom.js listens for: an on:<name> on an
// element must be one of these, or an event its behavior emits.
var domEvents = map[string]bool{"click": true, "change": true, "input": true, "submit": true, "keydown": true, "keyup": true, "focus": true, "blur": true}

// mountExport finds the module's mount export.
var mountExport = regexp.MustCompile(`export\s+(async\s+)?function\s*\*?\s*mount\b|export\s+(const|let|var)\s+mount\b|export\s*\{[^}]*\bmount\b`)

// declareBehaviors checks a file's behavior declarations and reads their
// modules.
func (c *checker) declareBehaviors(f *ast.File) {
	c.file = f.Source
	for _, decl := range f.Behaviors {
		start, end := decl.Pos.Offset, decl.Pos.Offset+len(decl.Name)
		if other, ok := c.behaviors[decl.Name]; ok {
			c.errorAt(diag.TC343, start, end, "behavior %s is also declared in %s", decl.Name, other.File.Source.Name).
				WithFix("behavior names must be unique within a package; rename one of them")
			continue
		}
		sum := sha256.Sum256([]byte(c.cfg.PkgPath + "." + decl.Name))
		b := &Behavior{Name: decl.Name, Decl: decl, File: f, ID: decl.Name + "-" + hex.EncodeToString(sum[:3]), GoName: PascalCase(decl.Name) + "Behavior"}
		if _, ok := c.pkg.Structs[b.GoName]; ok {
			c.errorAt(diag.TC328, start, end, "behavior %s generates the Go name %s, which the package already declares as a type", decl.Name, b.GoName).
				WithFix("rename the behavior or the Go type")
		}
		b.Params = c.behaviorParams(decl.Name, decl.Params, true)
		for _, e := range decl.Emits {
			b.Emits = append(b.Emits, &Emit{Name: e.Name, Params: c.behaviorParams(decl.Name, e.Params, false)})
		}
		c.readModule(b)
		c.behaviors[decl.Name] = b
		c.info.Behaviors = append(c.info.Behaviors, b)
	}
}

// behaviorParams checks a behavior's props or an emit's payload. Props
// are sent to the browser as JSON, so they are scalars, enums or lists of
// them; payload values arrive as strings, so they are scalars.
func (c *checker) behaviorParams(owner string, params []*ast.Param, props bool) []*Param {
	var out []*Param
	for _, p := range params {
		goName := PascalCase(p.Name)
		var t *Type
		if e, ok := p.Type.(*ast.EnumType); ok {
			t = &Type{Kind: Enum, Enum: &EnumType{GoName: PascalCase(owner) + goName, Values: e.Values}}
		} else {
			t = c.sigType(p.Type)
		}
		ok := isScalar(t) || t.Kind == Invalid
		if props && t.Kind == List {
			ok = isScalar(t.Elem)
		}
		if !props && t.Kind == Enum {
			ok = false
		}
		if !ok {
			pos := p.Type.Position()
			e := c.errorAt(diag.TC343, pos.Offset, pos.Offset+len(ast.TypeString(p.Type)), "%s is %s, which a behavior can't take", p.Name, t)
			if props {
				e.WithFix("props are sent to the browser as JSON: use string, int, float, bool, an enum, or a list of those")
			} else {
				e.WithFix("event payloads are strings on the wire: use string, int, float or bool")
			}
			t = TInvalid
		}
		prm := &Param{Name: p.Name, GoName: goName, Type: t, Default: p.Default}
		c.paramDefault(prm)
		out = append(out, prm)
	}
	return out
}

// readModule reads the behavior's ES module, name.js next to the template.
func (c *checker) readModule(b *Behavior) {
	start, end := b.Decl.Pos.Offset, b.Decl.Pos.Offset+len(b.Name)
	path := filepath.Join(c.pkg.Dir, b.Name+".js")
	src, err := os.ReadFile(path)
	if err != nil {
		c.errorAt(diag.TC343, start, end, "behavior %s has no module: %s doesn't exist", b.Name, path).
			WithFix("write %s.js next to %s, exporting function mount(el, props, emit) that returns {update, destroy}", b.Name, filepath.Base(b.File.Source.Name))
		return
	}
	if !mountExport.Match(src) {
		c.errorAt(diag.TC343, start, end, "%s doesn't export mount", path).
			WithFix("add export function mount(el, props, emit) { ...; return {update(props) {...}, destroy() {...}} }")
	}
	b.JS = string(src)
}

// behavior returns the behavior used as use:name: one of the package's, or
// one imported by the file being checked.
func (c *checker) behavior(name string) *Behavior {
	if b := c.behaviors[name]; b != nil {
		return b
	}
	if b := c.importedBehaviors[name]; b != nil {
		pkg := c.behaviorImport[b]
		c.used[c.comp.File][pkg] = true
		c.info.ImportedBehaviors[b] = pkg
		return b
	}
	return nil
}

func (c *checker) behaviorNames() []string {
	var names []string
	for n := range c.behaviors {
		names = append(names, n)
	}
	for n := range c.importedBehaviors {
		names = append(names, n)
	}
	return names
}

// use checks an element's use:, its on: bindings against what the
// behavior emits, and the behavior's requires list.
func (c *checker) use(n *ast.Element, sc *scope) *Behavior {
	u := n.Use
	if u == nil {
		return nil
	}
	start, end := u.Pos.Offset, u.Pos.Offset+len("use:")+len(u.Name)
	b := c.behavior(u.Name)
	if b == nil {
		e := c.errorAt(diag.TC344, start, end, "unknown behavior %s", u.Name)
		if s := closest(u.Name, c.behaviorNames()); s != "" {
			e.WithFix("did you mean use:%s?", s)
		} else {
			e.WithFix("declare it in a .hx file in this package, behavior %s(...), with its code in %s.js; or import the package that has it", u.Name, u.Name)
		}
		for _, p := range u.Props {
			c.expr(p.X, sc)
		}
		return nil
	}
	info := &UseInfo{Behavior: b}
	c.info.Uses[n] = info
	if !contains(c.comp.Behaviors, b) {
		c.comp.Behaviors = append(c.comp.Behaviors, b)
	}

	given := map[string]bool{}
	unknown := false
	for _, p := range u.Props {
		prm := b.Param(p.Name)
		if prm == nil {
			unknown = true
			e := c.errorAt(diag.TC344, p.Pos.Offset, p.Pos.Offset+len(p.Name), "behavior %s has no prop %s", b.Name, p.Name)
			if s := closest(p.Name, b.paramNames()); s != "" {
				e.WithFix("did you mean %s? Its props are: %s", s, strings.Join(b.paramNames(), ", "))
			} else if len(b.Params) == 0 {
				e.WithFix("it takes no props: write use:%s alone", b.Name)
			} else {
				e.WithFix("its props are: %s", strings.Join(b.paramNames(), ", "))
			}
			c.expr(p.X, sc)
			continue
		}
		given[p.Name] = true
		c.propValue(p, prm, sc)
	}
	var missing []string
	for _, p := range b.Params {
		if p.Default == nil && !given[p.Name] {
			missing = append(missing, p.Name)
		}
	}
	if len(missing) > 0 && !unknown { // a misspelt prop is reported once
		c.errorAt(diag.TC344, start, end, "use:%s is missing %s", b.Name, strings.Join(missing, ", ")).
			WithFix("pass every prop without a default: use:%s={%s: ...}", b.Name, missing[0])
	}

	var dynamic []string
	for _, r := range b.Decl.Requires {
		a := attr(n, r.Attr)
		switch {
		case a == nil:
			e := c.errorAt(diag.TC345, start, end, "use:%s requires %s, but <%s> has no %s", b.Name, r.Selector(), n.Tag, r.Attr)
			if r.HasValue {
				e.WithFix(`add %s="%s" to the <%s>`, r.Attr, r.Value, n.Tag)
			} else {
				e.WithFix("add a %s attribute to the <%s>", r.Attr, n.Tag)
			}
		case a.Kind == ast.AttrExpr:
			dynamic = append(dynamic, r.Selector())
		case r.HasValue && a.Value != r.Value:
			c.errorAt(diag.TC345, a.Pos.Offset, a.Pos.Offset+len(a.Name), "use:%s requires %s, but this is %s=%q", b.Name, r.Selector(), a.Name, a.Value).
				WithFix(`write %s="%s", or use another behavior`, r.Attr, r.Value)
		}
	}
	info.Requires = strings.Join(dynamic, "")
	return b
}

// propValue checks the value passed to a prop.
func (c *checker) propValue(p *ast.Prop, prm *Param, sc *scope) {
	if prm.Type.Kind == Enum {
		if lit, ok := p.X.(*ast.StringLit); ok {
			c.info.Types[p.X] = prm.Type
			if !prm.Type.Enum.Has(lit.Value) {
				s, e := span(p.X)
				c.enumValueError(s, e, prm, lit.Value)
			}
			return
		}
	}
	t := c.expr(p.X, sc)
	switch {
	case Identical(t, prm.Type):
	case prm.Type.Kind == Enum && t.Kind == Enum && subset(t.Enum, prm.Type.Enum):
		// Props travel as JSON strings, so any enum whose values the
		// behavior accepts will do.
	case prm.Type.Kind == Enum && t.Kind == String:
		c.errorExpr(diag.TC338, p.X, "%s is a string, but prop %s takes one of %s", c.src(p.X), prm.Name, prm.Type).
			WithFix(`pass a value as written, e.g. %s: "%s", or an enum parameter of the same values`, prm.Name, prm.Type.Enum.Values[0])
	default:
		c.errorExpr(diag.TC344, p.X, "prop %s is %s, but %s is %s", prm.Name, prm.Type, c.src(p.X), t).
			WithFix("pass a value of type %s", prm.Type)
	}
}

// eventBinding checks on:name="event" on an element: name must be a DOM
// event loom.js sends, or one the element's behavior emits.
func (c *checker) eventBinding(a *ast.Attr, b *Behavior) {
	name := strings.TrimPrefix(a.Name, "on:")
	if domEvents[name] || b != nil && b.emits(name) {
		return
	}
	e := c.errorAt(diag.TC346, a.Pos.Offset, a.Pos.Offset+len(a.Name), "%s isn't an event this element sends", a.Name)
	switch {
	case b != nil && len(b.Emits) > 0:
		e.WithFix("the DOM events are click, change, input, submit, keydown, keyup, focus and blur; behavior %s emits %s", b.Name, strings.Join(b.EmitNames(), ", "))
	case b != nil:
		e.WithFix("the DOM events are click, change, input, submit, keydown, keyup, focus and blur; behavior %s emits none", b.Name)
	default:
		e.WithFix("the DOM events are click, change, input, submit, keydown, keyup, focus and blur; other events come from a behavior, declared with emits (%s) and attached with use:", name)
	}
}

func attr(n *ast.Element, name string) *ast.Attr {
	for _, a := range n.Attrs {
		if a.Name == name {
			return a
		}
	}
	return nil
}

func contains(bs []*Behavior, b *Behavior) bool {
	for _, x := range bs {
		if x == b {
			return true
		}
	}
	return false
}

// resolveBehaviorImports finds the behaviors each imported package offers
// the file. As with components, a name may mean only one behavior.
func (c *checker) resolveBehaviorImports(f *ast.File) map[string]*Behavior {
	out := map[string]*Behavior{}
	for _, imp := range f.Imports {
		pkg := c.cfg.Imports[imp.Path]
		if pkg == nil || pkg.Info == nil {
			continue // reported by resolveImports
		}
		start, end := imp.Pos.Offset, imp.Pos.Offset+len(strconv.Quote(imp.Path))
		for _, b := range pkg.Info.Behaviors {
			if c.behaviors[b.Name] != nil {
				c.used[f][pkg] = true
				c.errorAt(diag.TC342, start, end, "%q exports behavior %s, which this package also declares", imp.Path, b.Name).
					WithFix("rename the local behavior %s; use: isn't qualified, so each name must mean one behavior", b.Name)
				continue
			}
			if other := out[b.Name]; other != nil {
				c.used[f][pkg] = true
				c.errorAt(diag.TC342, start, end, "%q and %q both export behavior %s", c.behaviorImport[other].Path, imp.Path, b.Name).
					WithFix("import only one of them in this file")
				continue
			}
			out[b.Name] = b
			c.behaviorImport[b] = pkg
		}
	}
	return out
}

// subset reports whether every value of a is one of b's.
func subset(a, b *EnumType) bool {
	for _, v := range a.Values {
		if !b.Has(v) {
			return false
		}
	}
	return true
}
