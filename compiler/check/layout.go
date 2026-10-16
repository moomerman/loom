package check

import (
	"strings"

	"github.com/moomerman/loom/compiler/ast"
	"github.com/moomerman/loom/compiler/diag"
)

// Root layouts and the builtin components only they may use.
//
// A root layout is a component whose markup has an <html> element: the
// document a live page is served in (see docs/layouts.md). It must be a
// complete document, render its inner_block (the live page) and include
// <.live_meta />, <.loom_styles /> and <.loom_scripts />. <.live_title>,
// <.live_meta />, <.loom_styles /> and <.loom_scripts /> are builtin
// components, valid only in a root layout.

// Builtin component names, as called.
const (
	LiveTitle   = "live_title"
	LiveMeta    = "live_meta"
	LoomStyles  = "loom_styles"
	LoomScripts = "loom_scripts"
)

var builtinComponents = map[string]bool{LiveTitle: true, LiveMeta: true, LoomStyles: true, LoomScripts: true}

// liveTitleAttrs are the attributes <.live_title> takes, as in HEEx.
var liveTitleAttrs = []string{"default", "prefix", "suffix"}

// builtinCall checks a call to <.live_title> or <.loom_scripts />.
func (c *checker) builtinCall(n *ast.ComponentCall, sc *scope) {
	start, end := n.Pos.Offset, n.Pos.Offset+2+len(n.Name)
	tag := "<." + n.Name + ">"
	if n.Name != LiveTitle {
		tag = "<." + n.Name + " />"
	}
	if !c.comp.RootLayout {
		c.errorAt(diag.TC333, start, end, "%s can only be used in a root layout", tag).
			WithFix("a root layout is a component with a whole document (<!DOCTYPE html><html><head>...</head><body>...</body></html>); move %s into yours, see docs/layouts.md", tag)
	}
	if c.builtinsSeen[n.Name] {
		c.errorAt(diag.TC332, start, end, "%s is used twice", tag).
			WithFix("a document has one; remove the second %s", tag)
	}
	c.builtinsSeen[n.Name] = true
	if n.For != nil || n.If != nil || n.Key != nil {
		c.errorAt(diag.TC332, start, end, "%s can't take :for, :if or :key", tag).
			WithFix("remove the directive; %s always renders exactly once", tag)
	}
	if len(n.Slots) > 0 {
		s := n.Slots[0]
		c.errorAt(diag.TC332, s.Pos.Offset, s.Pos.Offset+2+len(s.Name), "%s has no slot %s", tag, s.Name).
			WithFix("remove <:%s>", s.Name)
	}

	switch n.Name {
	case LoomScripts, LoomStyles, LiveMeta:
		if len(n.Attrs) > 0 {
			a := n.Attrs[0]
			c.errorAt(diag.TC332, a.Pos.Offset, a.Pos.Offset+len(a.Name), "%s takes no attributes", tag).
				WithFix("write %s", tag)
		}
		if len(n.Children) > 0 {
			c.errorAt(diag.TC332, start, end, "%s takes no content", tag).
				WithFix("write %s with nothing inside", tag)
		}
	case LiveTitle:
		for _, a := range n.Attrs {
			astart, aend := a.Pos.Offset, a.Pos.Offset+len(a.Name)
			known := false
			for _, name := range liveTitleAttrs {
				known = known || a.Name == name
			}
			switch {
			case !known:
				e := c.errorAt(diag.TC332, astart, aend, "<.live_title> has no attribute %s", a.Name)
				if s := closest(a.Name, liveTitleAttrs); s != "" {
					e.WithFix("did you mean %s? Its attributes are default, prefix and suffix", s)
				} else {
					e.WithFix("its attributes are default, prefix and suffix")
				}
				if a.Kind == ast.AttrExpr {
					c.expr(a.X, sc)
				}
			case a.Kind == ast.AttrBare:
				c.errorAt(diag.TC332, astart, aend, "%s of <.live_title> needs a value", a.Name).
					WithFix(`give it a string, e.g. %s="My app"`, a.Name)
			case a.Kind == ast.AttrExpr:
				if t := c.expr(a.X, sc); t.Kind != String && t.Kind != Invalid {
					c.errorExpr(diag.TC332, a.X, "%s of <.live_title> must be a string, but %s is %s", a.Name, c.src(a.X), t).
						WithFix("pass a string")
				}
			}
		}
		for _, child := range n.Children {
			switch child.(type) {
			case *ast.Text, *ast.Interp:
			default:
				p := child.Position().Offset
				c.errorAt(diag.TC332, p, p+1, "the content of <.live_title> must be text").
					WithFix(`write the title as text and {expressions}, e.g. <.live_title default="Todos">{title}</.live_title>`)
			}
		}
		c.nodes(n.Children, sc)
	}
}

// isRootLayout reports whether a component body has an <html> element.
func isRootLayout(body []ast.Node) bool { return findElement(body, "html") != nil }

// rootLayout checks that a root layout is a complete document that
// renders its page and the client runtime. It runs after the body has
// been checked, so builtinsSeen is complete.
func (c *checker) rootLayout(comp *Component) {
	html := findElement(comp.Decl.Body, "html")
	start, end := html.Pos.Offset, html.Pos.Offset+len("<html")
	nameStart, nameEnd := comp.Decl.Pos.Offset, comp.Decl.Pos.Offset+len(comp.Name)
	if !hasDoctype(comp.Decl.Body, html) {
		c.errorAt(diag.TC331, start, end, "root layout %s has no <!DOCTYPE html> before <html>", comp.Name).
			WithFix("start the document with <!DOCTYPE html>, so browsers use standards mode")
	}
	var head, body *ast.Element
	for _, n := range html.Children {
		if e, ok := n.(*ast.Element); ok {
			switch {
			case e.Tag == "head" && head == nil:
				head = e
			case e.Tag == "body" && body == nil:
				body = e
			}
		}
	}
	if head == nil {
		c.errorAt(diag.TC331, start, end, "root layout %s has no <head> inside <html>", comp.Name).
			WithFix(`add <head> with <meta charset="utf-8" /> and a <.live_title>, before <body>`)
	}
	if body == nil {
		c.errorAt(diag.TC331, start, end, "root layout %s has no <body> inside <html>", comp.Name).
			WithFix("add <body>{render_slot(inner_block)} <.loom_scripts /></body> after </head>")
	}
	if !c.builtinsSeen[LoomScripts] {
		c.errorAt(diag.TC329, start, end, "root layout %s has no <.loom_scripts />", comp.Name).
			WithFix("add <.loom_scripts /> at the end of <body>; without it the page is never live")
	}
	if !c.builtinsSeen[LiveMeta] {
		c.errorAt(diag.TC354, start, end, "root layout %s has no <.live_meta />", comp.Name).
			WithFix("add <.live_meta /> inside <head>, after <.live_title>; it renders the description and link preview tags pages set with c.Meta")
	}
	if !c.builtinsSeen[LoomStyles] {
		c.errorAt(diag.TC337, start, end, "root layout %s has no <.loom_styles />", comp.Name).
			WithFix("add <.loom_styles /> inside <head>; it links the stylesheet of the components each page renders")
	}
	if p := comp.Param("inner_block"); p == nil || p.Type.Kind != Slot {
		c.errorAt(diag.TC330, nameStart, nameEnd, "root layout %s has no inner_block slot for the page", comp.Name).
			WithFix("add inner_block slot to the signature and render {render_slot(inner_block)} inside <body>")
	} else if !rendersSlot(comp.Decl.Body, "inner_block") {
		c.errorAt(diag.TC330, start, end, "root layout %s never renders inner_block, so the page doesn't appear", comp.Name).
			WithFix("add {render_slot(inner_block)} inside <body>")
	}
}

// hasDoctype reports whether a <!DOCTYPE> comes before html at the top
// level of body.
func hasDoctype(body []ast.Node, html *ast.Element) bool {
	for _, n := range body {
		if n == ast.Node(html) {
			return false
		}
		if t, ok := n.(*ast.Text); ok && strings.Contains(strings.ToLower(t.Value), "<!doctype") {
			return true
		}
	}
	return false
}

// findElement returns the first element with the given tag, at any depth.
func findElement(nodes []ast.Node, tag string) *ast.Element {
	var found *ast.Element
	walk(nodes, func(n ast.Node) {
		if e, ok := n.(*ast.Element); ok && e.Tag == tag && found == nil {
			found = e
		}
	})
	return found
}

// rendersSlot reports whether nodes contain {render_slot(name)}.
func rendersSlot(nodes []ast.Node, name string) bool {
	found := false
	walk(nodes, func(n ast.Node) {
		if in, ok := n.(*ast.Interp); ok {
			if call, ok := in.X.(*ast.Call); ok && call.Func.Name == "render_slot" && len(call.Args) == 1 {
				if id, ok := call.Args[0].(*ast.Ident); ok && id.Name == name {
					found = true
				}
			}
		}
	})
	return found
}

// walk calls f for every node, depth first.
func walk(nodes []ast.Node, f func(ast.Node)) {
	for _, n := range nodes {
		f(n)
		switch n := n.(type) {
		case *ast.Element:
			walk(n.Children, f)
		case *ast.ComponentCall:
			walk(n.Children, f)
			for _, s := range n.Slots {
				walk(s.Children, f)
			}
		}
	}
}
