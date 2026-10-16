package loomtest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/moomerman/loom/render"
)

// Accessibility checks run on rendered HTML, without a browser. They
// catch what can be seen in the markup: names for images, buttons, links,
// form controls and dialogs; duplicate and dangling IDs; and valid ARIA
// roles, attributes and values. Each rule has a stable ID, like axe's,
// and a fix (see docs/components.md):
//
//	loomtest.AssertAccessible(t, loomtest.Render(t, Menu, MenuAssigns{...}))
//	loomtest.AssertPreviewsAccessible(t, components.Button) // every variant
//	p.AssertAccessible()                                    // a mounted page
//
// Components add their own rules, scoped by a selector:
//
//	rule := loomtest.A11yRule{ID: "menu-items", Selector: "[role=menu]",
//		Fix: "give the menu at least one [role=menuitem]",
//		Check: func(e *loomtest.Element) string {
//			if len(loomtest.Select(e, "[role=menuitem]")) == 0 { return "menu has no items" }
//			return ""
//		}}
//	loomtest.AssertAccessible(t, html, rule)

// A11yRule is an accessibility rule: Check runs on every element that
// matches Selector and returns a problem, or "" if there is none.
type A11yRule struct {
	ID       string // stable, e.g. "button-name"
	Selector string // which elements it checks (see Count for the syntax)
	Fix      string // how to fix a problem, shown with it
	Check    func(e *Element) string
}

// A11yProblem is a rule broken by an element.
type A11yProblem struct {
	Rule    string
	Element string // the element's start tag
	Message string
	Fix     string
}

func (p A11yProblem) String() string {
	return fmt.Sprintf("%s: %s: %s\n    fix: %s", p.Rule, p.Element, p.Message, p.Fix)
}

// A11y checks html against the built-in rules and any extra ones, and
// returns the problems found, in document order per rule.
func A11y(html string, extra ...A11yRule) ([]A11yProblem, error) {
	doc := parseHTML(html)
	var out []A11yProblem
	for _, r := range append(append([]A11yRule(nil), A11yRules...), extra...) {
		sel, err := parseSelector(r.Selector)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %v", r.ID, err)
		}
		for _, e := range find(doc, sel) {
			if msg := r.Check(e); msg != "" {
				out = append(out, A11yProblem{Rule: r.ID, Element: e.String(), Message: msg, Fix: r.Fix})
			}
		}
	}
	out = append(out, duplicateIDs(doc)...)
	return out, nil
}

// AssertAccessible fails the test with every accessibility problem in
// html.
func AssertAccessible(t testing.TB, html string, extra ...A11yRule) {
	t.Helper()
	problems, err := A11y(html, extra...)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// AssertAccessible fails the test with every accessibility problem in
// the page's current HTML.
func (p *Page) AssertAccessible(extra ...A11yRule) {
	p.t.Helper()
	AssertAccessible(p.t, p.HTML(), extra...)
}

// AssertPreviewsAccessible checks every preview variant of a component
// (see docs/components.md), as a subtest per variant. It fails if the
// component has no previews.
func AssertPreviewsAccessible(t *testing.T, tmpl render.Template, extra ...A11yRule) {
	t.Helper()
	samples, why := tmpl.ComponentPreviews()
	if len(samples) == 0 {
		t.Fatalf("%s.%s has no previews: %s", tmpl.PackageName(), tmpl.ComponentName(), why)
	}
	for _, s := range samples {
		t.Run(s.Name, func(t *testing.T) {
			AssertAccessible(t, s.Render(nil).HTML(), extra...)
		})
	}
}

// Select returns the elements under e (not e itself) that match a
// selector, for rules that look inside an element. A bad selector panics.
func Select(e *Element, selector string) []*Element {
	sel, err := parseSelector(selector)
	if err != nil {
		panic(err)
	}
	var out []*Element
	for _, c := range e.children {
		out = append(out, find(c, sel)...)
	}
	return out
}

// Name returns an element's accessible name, simplified from the ARIA
// algorithm: aria-labelledby, then aria-label, then for a form control
// its <label>, then for a button, link or similar its text and images'
// alt text, then title.
func Name(e *Element) string {
	if ids, ok := e.attrs["aria-labelledby"]; ok {
		var parts []string
		for _, id := range strings.Fields(ids) {
			if l := e.ByID(id); l != nil {
				parts = append(parts, l.Text())
			}
		}
		if n := strings.TrimSpace(strings.Join(parts, " ")); n != "" {
			return n
		}
	}
	if l := strings.TrimSpace(e.attrs["aria-label"]); l != "" {
		return l
	}
	if isControl(e) {
		if id := e.attrs["id"]; id != "" {
			for _, l := range Select(root(e), "label") {
				if l.attrs["for"] == id {
					if n := l.Text(); n != "" {
						return n
					}
				}
			}
		}
		for a := e.parent; a != nil; a = a.parent {
			if a.tag == "label" {
				if n := a.Text(); n != "" {
					return n
				}
			}
		}
	} else if nameFromContent(e) {
		if n := textAndAlt(e); n != "" {
			return n
		}
	}
	return strings.TrimSpace(e.attrs["title"])
}

// nameFromContent reports whether e's text names it: true for buttons,
// links and the like, false for containers such as dialogs, which need
// a label of their own.
func nameFromContent(e *Element) bool {
	switch e.attrs["role"] {
	case "button", "link", "tab", "option", "menuitem", "menuitemcheckbox", "menuitemradio",
		"checkbox", "radio", "switch", "treeitem", "cell", "columnheader", "rowheader", "heading", "tooltip":
		return true
	case "":
		switch e.tag {
		case "button", "a", "summary", "label", "legend", "caption", "th", "td", "h1", "h2", "h3", "h4", "h5", "h6", "option":
			return true
		}
	}
	return false
}

func root(e *Element) *Element {
	for e.parent != nil {
		e = e.parent
	}
	return e
}

// textAndAlt is an element's text with its images' alt text, skipping
// what is hidden with aria-hidden.
func textAndAlt(e *Element) string {
	var parts []string
	var walk func(n *Element)
	walk = func(n *Element) {
		switch {
		case n.tag == "#text":
			parts = append(parts, n.text)
			return
		case n.attrs["aria-hidden"] == "true":
			return
		case n.tag == "img":
			parts = append(parts, n.attrs["alt"])
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, c := range e.children {
		walk(c)
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

// isControl reports whether e is a form control that needs a label.
func isControl(e *Element) bool {
	switch e.tag {
	case "select", "textarea":
		return true
	case "input":
		switch e.attrs["type"] {
		case "hidden", "submit", "reset", "button", "image":
			return false
		}
		return true
	}
	return false
}

func duplicateIDs(doc *Element) []A11yProblem {
	var out []A11yProblem
	seen := map[string]bool{}
	reported := map[string]bool{}
	for _, e := range Select(doc, "[id]") {
		id := e.attrs["id"]
		if seen[id] && !reported[id] {
			reported[id] = true
			out = append(out, A11yProblem{Rule: "duplicate-id", Element: e.String(), Message: fmt.Sprintf("id %q is used more than once", id),
				Fix: "make each id unique, e.g. by adding the item's key: id={\"todo-\" + todo.id}"})
		}
		seen[id] = true
	}
	return out
}
