package loomtest

import (
	"fmt"
	"html"
	"sort"
	"strings"
)

// A small HTML parser and CSS selector matcher, enough to count elements
// and check accessibility in Loom's own output. It is not a general HTML
// parser: it expects well-formed markup, which generated templates
// always produce.

// Element is an element of parsed HTML, as accessibility rules see it.
type Element struct {
	tag      string // "" for the document, "#text" for text
	text     string // a text node's text, unescaped
	attrs    map[string]string
	children []*Element
	parent   *Element
}

// Tag returns the element's tag name, lower case.
func (e *Element) Tag() string { return e.tag }

// Attr returns an attribute's value, unescaped, and whether it is set.
func (e *Element) Attr(name string) (string, bool) {
	v, ok := e.attrs[name]
	return v, ok
}

// Parent returns the element's parent element, or nil at the top.
func (e *Element) Parent() *Element {
	if e.parent == nil || e.parent.tag == "" {
		return nil
	}
	return e.parent
}

// Children returns the element's child elements (not text).
func (e *Element) Children() []*Element {
	var out []*Element
	for _, c := range e.children {
		if c.tag != "#text" {
			out = append(out, c)
		}
	}
	return out
}

// Text returns the element's text content, with whitespace collapsed.
func (e *Element) Text() string {
	var b strings.Builder
	var walk func(n *Element)
	walk = func(n *Element) {
		if n.tag == "#text" {
			b.WriteString(n.text)
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(e)
	return strings.Join(strings.Fields(b.String()), " ")
}

// ByID returns the element in the same document with the given id, or
// nil.
func (e *Element) ByID(id string) *Element {
	root := e
	for root.parent != nil {
		root = root.parent
	}
	var found *Element
	var walk func(n *Element)
	walk = func(n *Element) {
		if found != nil {
			return
		}
		if n.tag != "#text" && n.tag != "" && n.attrs["id"] == id {
			found = n
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(root)
	return found
}

// String returns the element's start tag, to point at it in messages.
func (e *Element) String() string {
	var b strings.Builder
	b.WriteString("<" + e.tag)
	var names []string
	for k := range e.attrs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if v := e.attrs[k]; v != "" {
			fmt.Fprintf(&b, " %s=%q", k, v)
		} else {
			b.WriteString(" " + k)
		}
	}
	return b.String() + ">"
}

var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "source": true,
	"track": true, "wbr": true,
}

// parseHTML returns the document's element tree.
func parseHTML(s string) *Element {
	doc := &Element{}
	stack := []*Element{doc}
	addText := func(t string) {
		if strings.TrimSpace(t) != "" {
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, &Element{tag: "#text", text: html.UnescapeString(t), parent: parent})
		}
	}
	for len(s) > 0 {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			addText(s)
			break
		}
		addText(s[:i])
		s = s[i:]
		switch {
		case strings.HasPrefix(s, "<!--"):
			s = after(s, "-->")
		case strings.HasPrefix(s, "<!"):
			s = after(s, ">")
		case strings.HasPrefix(s, "</"):
			end := strings.IndexByte(s, '>')
			if end < 0 {
				return doc
			}
			tag := strings.ToLower(strings.TrimSpace(s[2:end]))
			s = s[end+1:]
			for j := len(stack) - 1; j > 0; j-- {
				if stack[j].tag == tag {
					stack = stack[:j]
					break
				}
			}
		default:
			n, rest, selfClose := parseTag(s[1:])
			s = rest
			parent := stack[len(stack)-1]
			n.parent = parent
			parent.children = append(parent.children, n)
			if n.tag == "script" || n.tag == "style" {
				s = after(s, "</"+n.tag+">")
			} else if !selfClose && !voidTags[n.tag] {
				stack = append(stack, n)
			}
		}
	}
	return doc
}

func after(s, sep string) string {
	if i := strings.Index(s, sep); i >= 0 {
		return s[i+len(sep):]
	}
	return ""
}

// parseTag parses an open tag after its '<'.
func parseTag(s string) (n *Element, rest string, selfClose bool) {
	n = &Element{attrs: map[string]string{}}
	i := 0
	for i < len(s) && !isSpace(s[i]) && s[i] != '>' && s[i] != '/' {
		i++
	}
	n.tag = strings.ToLower(s[:i])
	for i < len(s) {
		switch c := s[i]; {
		case c == '>':
			return n, s[i+1:], selfClose
		case c == '/':
			selfClose = true
			i++
		case isSpace(c):
			i++
		default:
			start := i
			for i < len(s) && !isSpace(s[i]) && s[i] != '=' && s[i] != '>' && s[i] != '/' {
				i++
			}
			name := strings.ToLower(s[start:i])
			value := ""
			if i < len(s) && s[i] == '=' {
				i++
				if i < len(s) && (s[i] == '"' || s[i] == '\'') {
					q := s[i]
					end := strings.IndexByte(s[i+1:], q)
					if end < 0 {
						end = len(s) - i - 1
					}
					value = s[i+1 : i+1+end]
					i += end + 2
				} else {
					start := i
					for i < len(s) && !isSpace(s[i]) && s[i] != '>' {
						i++
					}
					value = s[start:i]
				}
			}
			n.attrs[name] = html.UnescapeString(value)
		}
	}
	return n, "", selfClose
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

// A selector is a list of compound selectors separated by descendant
// combinators (spaces), e.g. "ul.todos li.done input[checked]".
type compound struct {
	tag     string
	id      string
	classes []string
	attrs   []attrSel
}

type attrSel struct {
	name, value string
	hasValue    bool
}

// parseSelector supports *, tag, #id, .class, [attr] and [attr=value]
// (value optionally quoted, and then it may hold spaces), combined with
// descendant combinators.
func parseSelector(sel string) ([]compound, error) {
	var out []compound
	for _, part := range splitCompounds(sel) {
		var c compound
		s := part
		for len(s) > 0 {
			switch s[0] {
			case '#', '.':
				j := 1
				for j < len(s) && !strings.ContainsRune("#.[", rune(s[j])) {
					j++
				}
				if j == 1 {
					return nil, fmt.Errorf("selector %q: empty name after %q", sel, s[0])
				}
				if s[0] == '#' {
					c.id = s[1:j]
				} else {
					c.classes = append(c.classes, s[1:j])
				}
				s = s[j:]
			case '[':
				end := strings.IndexByte(s, ']')
				if end < 0 {
					return nil, fmt.Errorf("selector %q: '[' without ']'", sel)
				}
				a := attrSel{name: strings.ToLower(s[1:end])}
				if name, value, ok := strings.Cut(s[1:end], "="); ok {
					a = attrSel{name: strings.ToLower(name), value: strings.Trim(value, `"'`), hasValue: true}
				}
				c.attrs = append(c.attrs, a)
				s = s[end+1:]
			default:
				j := 0
				for j < len(s) && !strings.ContainsRune("#.[", rune(s[j])) {
					j++
				}
				if c.tag != "" {
					return nil, fmt.Errorf("selector %q: unsupported syntax %q (supported: tag, #id, .class, [attr], [attr=value], descendant spaces)", sel, part)
				}
				c.tag = strings.ToLower(s[:j])
				if c.tag == "*" { // any element
					c.tag = ""
					s = s[j:]
					continue
				}
				if strings.ContainsAny(c.tag, ">+~:*,()") {
					return nil, fmt.Errorf("selector %q: unsupported syntax %q (supported: tag, #id, .class, [attr], [attr=value], descendant spaces)", sel, part)
				}
				s = s[j:]
			}
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty selector")
	}
	return out, nil
}

// splitCompounds splits a selector at the spaces between compounds,
// leaving spaces inside brackets alone: [title="a b"].
func splitCompounds(sel string) []string {
	var parts []string
	var b strings.Builder
	depth := 0
	for _, r := range sel {
		switch {
		case r == '[':
			depth++
		case r == ']' && depth > 0:
			depth--
		case depth == 0 && (r == ' ' || r == '\t' || r == '\n'):
			if b.Len() > 0 {
				parts = append(parts, b.String())
				b.Reset()
			}
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

func (c compound) matches(n *Element) bool {
	if n.tag == "#text" || n.tag == "" {
		return false
	}
	if c.tag != "" && c.tag != n.tag {
		return false
	}
	if c.id != "" && n.attrs["id"] != c.id {
		return false
	}
	for _, cl := range c.classes {
		if !hasClass(n.attrs["class"], cl) {
			return false
		}
	}
	for _, a := range c.attrs {
		v, ok := n.attrs[a.name]
		if !ok || a.hasValue && v != a.value {
			return false
		}
	}
	return true
}

func hasClass(list, class string) bool {
	for _, c := range strings.Fields(list) {
		if c == class {
			return true
		}
	}
	return false
}

// count returns how many elements under root match the selector.
func count(root *Element, sel []compound) int { return len(find(root, sel)) }

// find returns the elements under root that match the selector.
func find(root *Element, sel []compound) []*Element {
	var found []*Element
	var walk func(nd *Element, ancestors []*Element)
	walk = func(nd *Element, ancestors []*Element) {
		if nd.tag != "" && sel[len(sel)-1].matches(nd) && matchAncestors(sel[:len(sel)-1], ancestors) {
			found = append(found, nd)
		}
		if nd.tag != "" {
			ancestors = append(ancestors, nd)
		}
		for _, c := range nd.children {
			walk(c, ancestors)
		}
	}
	walk(root, nil)
	return found
}

// matchAncestors reports whether the compounds match ancestors in order,
// innermost last.
func matchAncestors(sel []compound, ancestors []*Element) bool {
	i := len(ancestors) - 1
	for j := len(sel) - 1; j >= 0; j-- {
		for i >= 0 && !sel[j].matches(ancestors[i]) {
			i--
		}
		if i < 0 {
			return false
		}
		i--
	}
	return true
}

// Count returns the number of elements in html matching a CSS selector.
// Supported: *, tag, #id, .class, [attr], [attr=value], and descendant
// combinators (spaces). Other syntax is an error.
func Count(html, selector string) (int, error) {
	sel, err := parseSelector(selector)
	if err != nil {
		return 0, err
	}
	return count(parseHTML(html), sel), nil
}
