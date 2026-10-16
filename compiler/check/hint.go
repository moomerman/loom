package check

import (
	"slices"
	"strings"

	"github.com/moomerman/loom/compiler/ast"
	"github.com/moomerman/loom/compiler/diag"
)

// hints are the optimistic:* hints (docs/live.md): while an event is
// pending, optimistic:class="a b" toggles classes on the element that
// sent it, and optimistic:disabled and optimistic:hidden set those. They
// are static, so they can't predict data: the server's render decides.
var hints = []string{"class", "disabled", "hidden"}

// isHint reports whether an attribute is an optimistic:* hint.
func isHint(a *ast.Attr) bool { return strings.HasPrefix(a.Name, "optimistic:") }

// bound reports whether attrs bind an on:* event, for hints to apply to.
func bound(attrs []*ast.Attr) bool {
	return slices.ContainsFunc(attrs, func(a *ast.Attr) bool { return strings.HasPrefix(a.Name, "on:") })
}

// hint checks an optimistic:* attribute of a tag whose attributes are
// attrs: on an HTML element or a component call, beside an on:* event.
func (c *checker) hint(a *ast.Attr, attrs []*ast.Attr) {
	start, end := a.Pos.Offset, a.Pos.Offset+len(a.Name)
	name := strings.TrimPrefix(a.Name, "optimistic:")
	switch {
	case !slices.Contains(hints, name):
		e := c.errorAt(diag.TC349, start, end, "unknown hint %s", a.Name)
		if s := closest(name, hints); s != "" {
			e.WithFix("did you mean optimistic:%s? The hints are optimistic:class, optimistic:disabled and optimistic:hidden", s)
		} else {
			e.WithFix(`the hints are optimistic:class="names", optimistic:disabled and optimistic:hidden`)
		}
		return
	case name == "class" && (a.Kind != ast.AttrString || strings.TrimSpace(a.Value) == ""):
		c.errorAt(diag.TC350, start, end, "optimistic:class takes class names as a quoted string").
			WithFix(`write e.g. optimistic:class="done"; hints are static, so the server's render, not an expression, decides what shows once the event is handled`)
	case name != "class" && a.Kind != ast.AttrBare:
		c.errorAt(diag.TC350, start, end, "%s takes no value", a.Name).
			WithFix("write %s alone; it applies while the event is pending", a.Name)
	}
	if !bound(attrs) {
		c.errorAt(diag.TC351, start, end, "%s is on a tag with no on:* event, so it never applies", a.Name).
			WithFix("put it on the element or component call whose on:* event it anticipates, e.g. <button on:click=\"delete\" %s>", a.Name)
	}
}
