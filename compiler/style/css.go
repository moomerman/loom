package style

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/compiler/source"
)

// Options say how to compile one style block.
type Options struct {
	Class  string  // the component's scope class, without the dot
	Tokens *Tokens // the app's tokens, or nil if it has no tokens.toml
	Unsafe bool    // <style unsafe>: raw values are allowed
	Global bool    // a root layout's block: :global(...) is allowed
}

// Compile compiles the CSS in f.Text[start:end], a component's style
// block. It scopes every selector to opt.Class, replaces token(group.name)
// with the token's custom property, renames @keyframes so they can't
// clash, and reports raw values and unknown tokens. The CSS it returns
// is meant to be served as is.
func Compile(f *source.File, start, end int, opt Options) (string, diag.List) {
	c := &cssCompiler{f: f, src: blankComments(f.Text[:end]), pos: start, end: end, opt: opt, keyframes: map[string]string{}}
	for _, m := range keyframesName.FindAllStringSubmatch(c.src[start:end], -1) {
		c.keyframes[m[1]] = m[1] + "-" + opt.Class
	}
	var out strings.Builder
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(syntaxError); !ok {
					panic(r)
				}
			}
		}()
		c.rules(&out, false)
	}()
	return out.String(), c.errs
}

// syntaxError stops compilation of a block: CSS errors cascade like
// template errors, so the first is the useful one.
type syntaxError struct{}

type cssCompiler struct {
	f         *source.File
	src       string // the file text up to end, with comments blanked out
	pos, end  int
	opt       Options
	errs      diag.List
	keyframes map[string]string // local @keyframes name -> scoped name
}

var keyframesName = regexp.MustCompile(`@(?:-webkit-)?keyframes\s+([A-Za-z_][-A-Za-z0-9_]*)`)

// blankComments replaces CSS comments with spaces, keeping every offset.
func blankComments(s string) string {
	b := []byte(s)
	for i := 0; i+1 < len(b); i++ {
		switch {
		case b[i] == '"' || b[i] == '\'':
			q := b[i]
			for i++; i < len(b) && b[i] != q && b[i] != '\n'; i++ {
				if b[i] == '\\' {
					i++
				}
			}
		case b[i] == '/' && b[i+1] == '*':
			j := i
			for ; j < len(b) && !(b[j] == '*' && j+1 < len(b) && b[j+1] == '/' && j > i+1); j++ {
				if b[j] != '\n' {
					b[j] = ' '
				}
			}
			for k := j; k < len(b) && k <= j+1; k++ {
				b[k] = ' '
			}
			i = j + 1
		}
	}
	return string(b)
}

func (c *cssCompiler) fail(start, end int, format string, args ...any) *diag.Error {
	e := diag.Errorf(diag.ST708, c.f, start, end, format, args...)
	c.errs = append(c.errs, e)
	return e
}

func (c *cssCompiler) errorf(code diag.Code, start, end int, format string, args ...any) *diag.Error {
	e := diag.Errorf(code, c.f, start, end, format, args...)
	c.errs = append(c.errs, e)
	return e
}

func (c *cssCompiler) space() {
	for c.pos < c.end && isSpace(c.src[c.pos]) {
		c.pos++
	}
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' }

// rules compiles rules until the end of the block or, when nested, the
// closing brace (which it consumes).
func (c *cssCompiler) rules(out *strings.Builder, nested bool) {
	for {
		c.space()
		if c.pos >= c.end {
			if nested {
				c.fail(c.end, c.end, "a block is never closed").WithFix("add the missing '}'")
				panic(syntaxError{})
			}
			return
		}
		switch c.src[c.pos] {
		case '}':
			if !nested {
				c.fail(c.pos, c.pos+1, "unexpected '}'").WithFix("remove it, or add the '{' it closes")
				panic(syntaxError{})
			}
			c.pos++
			return
		case '@':
			c.atRule(out)
		default:
			c.styleRule(out)
		}
	}
}

// prelude reads up to the next top-level '{', returning the text before
// it and its offset. It fails on ';' or '}', which mean a missing '{'.
func (c *cssCompiler) prelude(what string) (string, int) {
	start := c.pos
	depth := 0
	for c.pos < c.end {
		switch ch := c.src[c.pos]; ch {
		case '"', '\'':
			c.skipString()
			continue
		case '\\':
			c.pos += 2
			continue
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case '{':
			if depth <= 0 {
				p := c.src[start:c.pos]
				c.pos++
				return p, start
			}
		case ';', '}':
			if depth <= 0 {
				c.fail(start, c.pos, "expected '{' after %s", strings.TrimSpace(c.src[start:c.pos])).
					WithFix("a rule looks like %s { property: value; }", what)
				panic(syntaxError{})
			}
		}
		c.pos++
	}
	c.fail(start, c.end, "expected '{' after %s", strings.TrimSpace(c.src[start:c.end])).
		WithFix("a rule looks like %s { property: value; }", what)
	panic(syntaxError{})
}

func (c *cssCompiler) skipString() {
	q := c.src[c.pos]
	start := c.pos
	for c.pos++; c.pos < c.end && c.src[c.pos] != q; c.pos++ {
		if c.src[c.pos] == '\\' {
			c.pos++
		}
		if c.src[c.pos] == '\n' {
			break
		}
	}
	if c.pos >= c.end || c.src[c.pos] != q {
		c.fail(start, start+1, "string is never closed").WithFix("add a closing %c", q)
		panic(syntaxError{})
	}
	c.pos++
}

func (c *cssCompiler) styleRule(out *strings.Builder) {
	sel, at := c.prelude("selector")
	scoped := c.selectors(sel, at)
	var body strings.Builder
	c.declarations(&body, true)
	fmt.Fprintf(out, "%s{%s}\n", scoped, body.String())
}

func (c *cssCompiler) atRule(out *strings.Builder) {
	start := c.pos
	c.pos++
	nameStart := c.pos
	for c.pos < c.end && (isIdentChar(c.src[c.pos])) {
		c.pos++
	}
	name := strings.ToLower(c.src[nameStart:c.pos])
	switch name {
	case "media", "supports", "container", "layer":
		p, _ := c.prelude("@" + name + " (...)")
		var inner strings.Builder
		c.rules(&inner, true)
		fmt.Fprintf(out, "@%s %s{\n%s}\n", name, collapse(p), inner.String())
	case "keyframes", "-webkit-keyframes":
		p, _ := c.prelude("@keyframes name")
		kf := strings.TrimSpace(p)
		if scoped, ok := c.keyframes[kf]; ok {
			kf = scoped
		}
		fmt.Fprintf(out, "@%s %s{\n", name, kf)
		for {
			c.space()
			if c.pos >= c.end {
				c.fail(start, nameStart+len(name), "@%s is never closed", name).WithFix("add the missing '}'")
				panic(syntaxError{})
			}
			if c.src[c.pos] == '}' {
				c.pos++
				break
			}
			frame, _ := c.prelude("from, to or 50%")
			var body strings.Builder
			c.declarations(&body, true)
			fmt.Fprintf(out, "%s{%s}\n", collapse(frame), body.String())
		}
		out.WriteString("}\n")
	case "font-face":
		c.prelude("@font-face")
		var body strings.Builder
		c.declarations(&body, false)
		fmt.Fprintf(out, "@font-face{%s}\n", body.String())
	default:
		e := c.fail(start, c.pos, "@%s isn't supported in a component's style", name)
		if name == "import" {
			e.WithFix("styles are per component; put shared styles in the component that renders the elements, or in the root layout")
		} else {
			e.WithFix("the supported at-rules are @media, @supports, @container, @layer, @keyframes and @font-face")
		}
		panic(syntaxError{})
	}
}

func isIdentChar(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || b == '-' || b == '_'
}

// collapse trims s and collapses runs of whitespace to one space.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// declarations compiles `prop: value;` pairs up to and including the
// closing brace. check says whether values are checked for raw values.
func (c *cssCompiler) declarations(out *strings.Builder, check bool) {
	for {
		c.space()
		if c.pos >= c.end {
			c.fail(c.end, c.end, "a rule is never closed").WithFix("add the missing '}'")
			panic(syntaxError{})
		}
		switch c.src[c.pos] {
		case '}':
			c.pos++
			return
		case ';':
			c.pos++
			continue
		}
		propStart := c.pos
		for c.pos < c.end && isIdentChar(c.src[c.pos]) {
			c.pos++
		}
		prop := c.src[propStart:c.pos]
		if prop == "" {
			c.fail(c.pos, c.pos+1, "expected a property name, found %q", c.src[c.pos]).
				WithFix("write declarations as property: value; nested rules aren't supported")
			panic(syntaxError{})
		}
		c.space()
		if c.pos >= c.end || c.src[c.pos] != ':' {
			rest := c.src[c.pos:c.end]
			if i := strings.IndexAny(rest, "{;}"); i >= 0 && rest[i] == '{' {
				c.fail(propStart, c.pos+i+1, "nested rules aren't supported").
					WithFix("move the rule out to the top level of the style block, with its full selector")
			} else {
				c.fail(propStart, c.pos, "expected ':' after %s", prop).WithFix("write %s: value;", prop)
			}
			panic(syntaxError{})
		}
		c.pos++
		valStart := c.pos
		depth := 0
	value:
		for c.pos < c.end {
			switch c.src[c.pos] {
			case '"', '\'':
				c.skipString()
				continue
			case '(':
				depth++
			case ')':
				depth--
			case '{':
				c.fail(propStart, c.pos, "nested rules aren't supported").
					WithFix("end %s with ';' and write each rule at the top level of the style block", prop)
				panic(syntaxError{})
			case ';', '}':
				if depth <= 0 {
					break value
				}
			}
			c.pos++
		}
		val := c.src[valStart:c.pos]
		if strings.TrimSpace(val) == "" {
			c.fail(propStart, c.pos, "%s has no value", prop).WithFix("write %s: value;", prop)
			panic(syntaxError{})
		}
		v := c.value(strings.ToLower(prop), val, valStart, check)
		fmt.Fprintf(out, "%s:%s;", prop, v)
	}
}

// selectors scopes a selector list: every compound selector gets the
// scope class, so the rule only matches the component's own elements.
func (c *cssCompiler) selectors(list string, at int) string {
	var out []string
	for _, part := range splitTop(list, ',') {
		sel := list[part[0]:part[1]]
		if strings.TrimSpace(sel) == "" {
			c.fail(at, at+len(list), "empty selector in %q", collapse(list)).WithFix("remove the extra comma")
			panic(syntaxError{})
		}
		out = append(out, c.complex(sel, at+part[0]))
	}
	return strings.Join(out, ",")
}

// complex scopes one complex selector, like `.list > li a:hover`.
func (c *cssCompiler) complex(sel string, at int) string {
	var b strings.Builder
	i, depth := 0, 0
	compStart := -1
	flush := func(end int) {
		if compStart >= 0 {
			b.WriteString(c.compound(sel[compStart:end], at+compStart))
			compStart = -1
		}
	}
	pendingComb := ""
	for i < len(sel) {
		ch := sel[i]
		switch {
		case ch == '\\':
			if compStart < 0 {
				compStart = i
			}
			i += 2
			continue
		case ch == '"' || ch == '\'':
			j := strings.IndexByte(sel[i+1:], ch)
			if j < 0 {
				j = len(sel) - i - 1
			}
			i += j + 2
			continue
		case ch == '(' || ch == '[':
			depth++
		case ch == ')' || ch == ']':
			depth--
		case depth == 0 && (isSpace(ch) || ch == '>' || ch == '+' || ch == '~'):
			flush(i)
			if !isSpace(ch) {
				pendingComb = string(ch)
			} else if pendingComb == "" {
				pendingComb = " "
			}
			i++
			continue
		}
		if compStart < 0 {
			if b.Len() > 0 {
				if pendingComb == " " {
					b.WriteByte(' ')
				} else {
					b.WriteString(pendingComb)
				}
			} else if pendingComb != "" && pendingComb != " " {
				c.fail(at, at+len(sel), "selector %q starts with %s", collapse(sel), pendingComb).
					WithFix("start the selector with an element, class or attribute")
				panic(syntaxError{})
			}
			pendingComb = ""
			compStart = i
		}
		i++
	}
	flush(len(sel))
	if pendingComb != "" && pendingComb != " " {
		c.fail(at, at+len(sel), "selector %q ends with %s", collapse(sel), pendingComb).
			WithFix("add the element the combinator leads to, or remove it")
		panic(syntaxError{})
	}
	return b.String()
}

// legacyPseudoElements may be written with one colon.
var legacyPseudoElements = []string{":before", ":after", ":first-line", ":first-letter"}

// compound adds the scope class to one compound selector, before any
// pseudo-element. :global(x) is left unscoped, in a root layout only.
func (c *cssCompiler) compound(s string, at int) string {
	if strings.HasPrefix(s, ":global(") && strings.HasSuffix(s, ")") {
		if !c.opt.Global {
			c.errorf(diag.ST709, at, at+len(s), ":global(...) can only be used in a root layout").
				WithFix("style elements from the component that renders them; global styles for the document go in the root layout")
		}
		return s[len(":global(") : len(s)-1]
	}
	if strings.Contains(s, ":global(") {
		c.errorf(diag.ST709, at, at+len(s), ":global(...) must be a whole compound selector, as in :global(body) .x").
			WithFix("write :global(%s) on its own", strings.SplitN(strings.SplitN(s, ":global(", 2)[1], ")", 2)[0])
		return s
	}
	if strings.Contains(s, "&") {
		c.fail(at, at+len(s), "nested selectors with & aren't supported").
			WithFix("write the full selector; styles are already scoped to the component")
		panic(syntaxError{})
	}
	insert := len(s)
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ':':
			if depth != 0 {
				continue
			}
			if strings.HasPrefix(s[i:], "::") {
				insert = i
			} else {
				for _, p := range legacyPseudoElements {
					if strings.HasPrefix(strings.ToLower(s[i:]), p) && (i+len(p) == len(s) || !isIdentChar(s[i+len(p)])) {
						insert = i
					}
				}
			}
		}
		if insert != len(s) {
			break
		}
	}
	return s[:insert] + "." + c.opt.Class + s[insert:]
}

// splitTop splits s on sep outside parentheses, brackets and strings,
// returning [start, end) offsets.
func splitTop(s string, sep byte) [][2]int {
	var parts [][2]int
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; {
		case ch == '\\':
			i++
		case ch == '"' || ch == '\'':
			if j := strings.IndexByte(s[i+1:], ch); j >= 0 {
				i += j + 1
			}
		case ch == '(' || ch == '[':
			depth++
		case ch == ')' || ch == ']':
			depth--
		case ch == sep && depth == 0:
			parts = append(parts, [2]int{start, i})
			start = i + 1
		}
	}
	return append(parts, [2]int{start, len(s)})
}
