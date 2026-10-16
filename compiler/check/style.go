package check

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/moomerman/loom/compiler/ast"
	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/compiler/style"
)

// Scoped styles (docs/components.md). A component's <style> block is
// compiled to CSS scoped to the component: each of its elements gets the
// component's scope class, and each selector requires it. The runtime
// serves the CSS of the components a route renders in one stylesheet.

// ScopeClass returns the scope class of a component: "l-" and a short
// hash of its package and name, stable across builds and machines.
func ScopeClass(pkgPath, component string) string {
	sum := sha256.Sum256([]byte(pkgPath + "." + component))
	return "l-" + hex.EncodeToString(sum[:3])
}

// styles compiles a component's style blocks into comp.CSS.
func (c *checker) styles(comp *Component) {
	decl := comp.Decl
	if len(decl.Styles) == 0 {
		return
	}
	comp.Class = ScopeClass(c.cfg.PkgPath, comp.Name)
	for i, st := range decl.Styles {
		start, end := st.Pos.Offset, st.Pos.Offset+len("<style")
		if i > 0 {
			c.errorAt(diag.ST707, start, end, "component %s has more than one <style> block", comp.Name).
				WithFix("merge them into one <style> block; the first is at line %d", decl.Styles[0].Pos.Line)
			continue
		}
		unsafe := false
		for _, a := range st.Attrs {
			if a.Name == "unsafe" && a.Kind == ast.AttrBare {
				unsafe = true
				continue
			}
			c.errorAt(diag.ST707, a.Pos.Offset, a.Pos.Offset+len(a.Name), "<style> takes no attribute %s", a.Name).
				WithFix("the only attribute is unsafe, which allows raw values: <style unsafe>")
		}
		if st.For != nil || st.If != nil {
			c.errorAt(diag.ST707, start, end, "<style> can't take :for or :if").
				WithFix("a style block is static; style a state with a class, e.g. class={if done, \"done\", \"\"}")
		}
		if unsafe {
			c.warnAt(diag.ST705, start, end+len(" unsafe"), "unsafe style block in %s: raw values aren't checked", comp.Name).
				WithFix("replace raw colors, spacing, radii, font sizes and shadows with token(...) and remove unsafe")
		}
		comp.Unsafe = unsafe
		if len(st.Children) == 0 {
			continue
		}
		text := st.Children[0].(*ast.Text) // <style> contents are one raw text token
		css, errs := style.Compile(c.file, text.Pos.Offset, text.Pos.Offset+len(text.Value), style.Options{
			Class:  comp.Class,
			Tokens: c.cfg.Tokens,
			Unsafe: unsafe,
			Global: comp.RootLayout,
		})
		c.errs = append(c.errs, errs...)
		comp.CSS = css
	}
}

// nestedStyle reports a <style> element that isn't at the top level of a
// component, where it would neither be scoped nor work in a live update.
func (c *checker) nestedStyle(n *ast.Element) {
	c.errorAt(diag.ST707, n.Pos.Offset, n.Pos.Offset+len("<style"), "<style> must be at the top level of component %s", c.comp.Name).
		WithFix("move it to the top level of the component, outside any element; its CSS is scoped to the component and linked by <.loom_styles /> in the root layout")
}

// themeAttr checks data-theme: light or dark (the document's mode), or
// a theme from tokens.toml. Its values are written out, as a string or
// an inline if choosing between strings, so each one is checked here:
// a misspelt theme would otherwise leave the element silently unthemed.
func (c *checker) themeAttr(a *ast.Attr, sc *scope) {
	switch a.Kind {
	case ast.AttrString:
		c.themeName(a.Value, a.Pos.Offset, a.Pos.Offset+len(a.Name)+len(a.Value)+3)
	case ast.AttrExpr:
		c.expr(a.X, sc)
		c.themeValue(a.X)
	}
}

func (c *checker) themeValue(x ast.Expr) {
	switch x := x.(type) {
	case *ast.StringLit:
		c.themeName(x.Value, x.Pos.Offset, x.Pos.Offset+len(x.Value)+2)
	case *ast.Cond:
		c.themeValue(x.Then)
		if x.Else != nil {
			c.themeValue(x.Else)
		}
	default:
		c.errorExpr(diag.ST712, x, "data-theme takes theme names written out, but %s could be any string", c.src(x)).
			WithFix(`choose between names with an inline if, e.g. data-theme={if t.platform == "amiga", "amiga", ""}, so each one is checked`)
	}
}

func (c *checker) themeName(name string, start, end int) {
	names := append([]string{"light", "dark"}, c.cfg.Tokens.ThemeNames()...)
	if name == "" || slices.Contains(names, name) {
		return
	}
	e := c.errorAt(diag.ST712, start, end, "data-theme=%q names no theme", name)
	if m := closest(name, names); m != "" {
		e.WithFix("did you mean %q? the themes are %s", m, strings.Join(names, ", "))
	} else {
		e.WithFix("use one of %s, or declare it in tokens.toml under [theme.%s.color]", strings.Join(names, ", "), name)
	}
}
