package style

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/moomerman/loom/compiler/diag"
)

// category is what a property's values are checked for.
type category struct {
	what    string   // e.g. "a color", for messages
	colors  bool     // raw colors are errors
	lengths bool     // raw lengths are errors
	groups  []string // token groups it takes; nil means any
}

var (
	colorCat    = category{what: "color", colors: true, groups: []string{"color"}}
	spaceCat    = category{what: "spacing", lengths: true, groups: []string{"space"}}
	radiusCat   = category{what: "radius", lengths: true, groups: []string{"radius"}}
	textCat     = category{what: "font size", lengths: true, groups: []string{"text"}}
	fontCat     = category{what: "font", lengths: true, groups: []string{"text", "font"}}
	familyCat   = category{what: "font family", groups: []string{"font"}}
	shadowCat   = category{what: "shadow", colors: true, lengths: true, groups: []string{"shadow"}}
	durationCat = category{what: "timing", groups: []string{"duration"}}
	otherCat    = category{}
)

// propCategory returns the category of a CSS property. Properties that
// aren't listed are layout and free to use raw values, except colors
// written as #hex or rgb(), which are always errors.
func propCategory(prop string) category {
	switch {
	case prop == "color", prop == "background", prop == "background-color", prop == "background-image",
		prop == "outline", prop == "outline-color", prop == "fill", prop == "stroke", prop == "caret-color",
		prop == "accent-color", prop == "text-decoration", prop == "text-decoration-color", prop == "column-rule",
		prop == "column-rule-color", prop == "scrollbar-color", prop == "text-emphasis-color",
		strings.HasPrefix(prop, "border") && !strings.HasSuffix(prop, "radius") && !strings.HasSuffix(prop, "width") &&
			!strings.HasSuffix(prop, "style") && !strings.HasPrefix(prop, "border-image") && prop != "border-collapse" && prop != "border-spacing":
		return colorCat
	case strings.HasPrefix(prop, "margin"), strings.HasPrefix(prop, "padding"), prop == "gap", prop == "row-gap", prop == "column-gap":
		return spaceCat
	case strings.HasSuffix(prop, "radius"):
		return radiusCat
	case prop == "font-size":
		return textCat
	case prop == "font":
		return fontCat
	case prop == "font-family":
		return familyCat
	case prop == "box-shadow", prop == "text-shadow":
		return shadowCat
	case strings.HasPrefix(prop, "transition"), strings.HasPrefix(prop, "animation"):
		return durationCat
	}
	return otherCat
}

// keywords that are never raw values.
var keywords = map[string]bool{
	"transparent": true, "currentcolor": true, "inherit": true, "initial": true, "unset": true,
	"revert": true, "revert-layer": true, "none": true, "auto": true,
}

var colorFuncs = map[string]bool{
	"rgb": true, "rgba": true, "hsl": true, "hsla": true, "hwb": true, "lab": true, "lch": true,
	"oklab": true, "oklch": true, "color": true,
}

// value compiles a declaration's value: it resolves token() calls and,
// when check is set and the block isn't unsafe, reports raw values. at is
// the value's byte offset in the file.
func (c *cssCompiler) value(prop, val string, at int, check bool) string {
	cat := propCategory(prop)
	if strings.HasPrefix(prop, "--") {
		cat = category{what: "custom property", colors: true}
	}
	raw := check && !c.opt.Unsafe
	if raw && cat.what == "shadow" && hasRawShadow(val) {
		// A shadow is one value: report it once, not each length in it.
		trimmed := strings.TrimSpace(val)
		start := at + strings.Index(val, trimmed)
		e := c.errorf(diag.ST704, start, start+len(trimmed), "raw shadow %s in %s", trimmed, prop)
		if g := c.opt.Tokens.Group("shadow"); g != nil && len(g.Tokens) > 0 {
			e.WithFix("use a shadow token: %s; or add one to tokens.toml", hints(g.Tokens))
		} else {
			e.WithFix("define it in tokens.toml (e.g. [shadow] card = %q) and write token(shadow.card); for a one-off, use <style unsafe>", trimmed)
		}
		raw = false
	}
	var b strings.Builder
	space := false
	i := 0
	emit := func(s string) {
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteString(s)
	}
	for i < len(val) {
		ch := val[i]
		switch {
		case isSpace(ch):
			space = true
			i++
		case ch == '"' || ch == '\'':
			j := i + 1
			for j < len(val) && val[j] != ch {
				if val[j] == '\\' {
					j++
				}
				j++
			}
			emit(val[i:min(j+1, len(val))])
			i = j + 1
		case ch == '#':
			j := i + 1
			for j < len(val) && isIdentChar(val[j]) {
				j++
			}
			if raw {
				c.rawColor(prop, cat, val[i:j], at+i)
			}
			emit(val[i:j])
			i = j
		case isDigit(ch) || ch == '.' && i+1 < len(val) && isDigit(val[i+1]) ||
			(ch == '-' || ch == '+') && i+1 < len(val) && (isDigit(val[i+1]) || val[i+1] == '.') && (i == 0 || !isIdentChar(val[i-1])):
			j := i + 1
			for j < len(val) && (isDigit(val[j]) || val[j] == '.') {
				j++
			}
			k := j
			for k < len(val) && (isIdentChar(val[k]) && !isDigit(val[k]) || val[k] == '%') {
				k++
			}
			num, unit := val[i:j], strings.ToLower(val[j:k])
			if raw && cat.lengths && lengthUnits[unit] && !isZero(num) {
				c.rawLength(prop, cat, val[i:k], at+i)
			}
			emit(val[i:k])
			i = k
		case isIdentStart(ch):
			j := i
			for j < len(val) && isIdentChar(val[j]) {
				j++
			}
			name := val[i:j]
			lower := strings.ToLower(name)
			if j < len(val) && val[j] == '(' {
				end := matchParen(val, j)
				args := val[j+1 : max(j+1, end)]
				switch {
				case lower == "token":
					emit(c.token(prop, cat, args, at+i, at+end+1))
				case lower == "url":
					emit(val[i:min(end+1, len(val))])
				case lower == "var" && c.tokenVar(args, at+i, at+end+1, raw):
					emit(val[i:min(end+1, len(val))])
				case colorFuncs[lower]:
					if raw {
						c.rawColor(prop, cat, val[i:min(end+1, len(val))], at+i)
					}
					emit(name + "(" + c.value(prop, args, at+j+1, false) + ")")
				default:
					emit(name + "(" + c.value(prop, args, at+j+1, check) + ")")
				}
				i = end + 1
				continue
			}
			switch {
			case raw && cat.colors && cat.groups != nil && namedColors[lower] != "" && !keywords[lower]:
				c.rawColor(prop, cat, name, at+i)
			case (prop == "animation" || prop == "animation-name") && c.keyframes[name] != "":
				name = c.keyframes[name]
			}
			emit(name)
			i = j
		default:
			if ch == ',' || ch == '/' || ch == ')' {
				space = false
			}
			emit(string(ch))
			if ch == ',' {
				space = true
			}
			i++
		}
	}
	return b.String()
}

// hasRawShadow reports whether a shadow value has a length or color
// outside token() calls.
func hasRawShadow(val string) bool {
	v := tokenCall.ReplaceAllString(strings.ToLower(val), "")
	return rawShadowPart.MatchString(v)
}

var (
	tokenCall     = regexp.MustCompile(`token\([^)]*\)`)
	rawShadowPart = regexp.MustCompile(`#|\d*\.?\d+[a-z]+|rgb|hsl|oklch|\b(black|white|gray|grey)\b`)
)

func isDigit(b byte) bool { return '0' <= b && b <= '9' }
func isIdentStart(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || b == '-' || b == '_'
}

// matchParen returns the index of the ')' matching the '(' at open, or
// len(s)-1 if there is none.
func matchParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		case '"', '\'':
			if j := strings.IndexByte(s[i+1:], s[i]); j >= 0 {
				i += j + 1
			}
		}
	}
	return len(s) - 1
}

// token resolves token(group.name) to its custom property. start and end
// span the whole call.
func (c *cssCompiler) token(prop string, cat category, args string, start, end int) string {
	ref := strings.TrimSpace(args)
	group, name, ok := strings.Cut(ref, ".")
	if !ok || group == "" || name == "" {
		c.errorf(diag.ST703, start, end, "token(%s) needs a group and a name", ref).
			WithFix("write token(group.name), e.g. token(color.accent) or token(space.2)")
		return "unset"
	}
	if c.opt.Tokens == nil {
		c.errorf(diag.ST703, start, end, "token(%s) is used, but there is no tokens.toml", ref).
			WithFix("create tokens.toml in the app (next to go.mod, or in this directory or a parent) with a [%s] group defining %s", group, name)
		return "unset"
	}
	tok := c.opt.Tokens.Lookup(group, name)
	if tok == nil {
		e := c.errorf(diag.ST703, start, end, "unknown token %s", ref)
		g := c.opt.Tokens.Group(group)
		switch s := closestName(ref, c.opt.Tokens.Refs()); {
		case s != "" && len(name) > 2:
			e.WithFix("did you mean token(%s)?", s)
		case g != nil && len(g.Tokens) > 0:
			e.WithFix("the %s tokens are %s; add %s to tokens.toml if it's missing", group, hints(g.Tokens), ref)
		case g != nil:
			e.WithFix("tokens.toml has no %s tokens yet; add %s to its [%s] group", group, name, group)
		default:
			e.WithFix("tokens.toml has no [%s] group; its groups are: %s", group, strings.Join(groupNames(c.opt.Tokens), ", "))
		}
		return "unset"
	}
	if !c.opt.Unsafe && cat.groups != nil && !contains(cat.groups, group) {
		e := c.errorf(diag.ST710, start, end, "%s takes %s tokens, not %s", prop, strings.Join(cat.groups, " or "), ref)
		if g := c.opt.Tokens.Group(cat.groups[0]); g != nil && len(g.Tokens) > 0 {
			e.WithFix("use a %s token, e.g. token(%s)", cat.groups[0], g.Tokens[0].Ref())
		} else {
			e.WithFix("add a [%s] group to tokens.toml and use one of its tokens", cat.groups[0])
		}
	}
	return "var(" + tok.Var() + ")"
}

// tokenVar reports a var() that names a token's custom property, which
// should be written token(...). It returns true when var() is fine.
func (c *cssCompiler) tokenVar(args string, start, end int, raw bool) bool {
	v := strings.TrimSpace(strings.SplitN(args, ",", 2)[0])
	for _, g := range c.opt.Tokens.groups() {
		for _, tok := range g.Tokens {
			if tok.Var() == v && raw {
				c.errorf(diag.ST704, start, end, "var(%s) refers to a token directly", v).
					WithFix("write token(%s), so the compiler can check it", tok.Ref())
			}
		}
	}
	return true
}

func (t *Tokens) groups() []*Group {
	if t == nil {
		return nil
	}
	return t.Groups
}

func (c *cssCompiler) rawColor(prop string, cat category, raw string, at int) {
	e := c.errorf(diag.ST704, at, at+len(raw), "raw color %s in %s", raw, prop)
	if toks := c.opt.Tokens.closestTokens("color", raw); len(toks) > 0 {
		e.WithFix("use a color token: %s; or add one to tokens.toml", hints(toks))
	} else {
		e.WithFix("define it in tokens.toml (e.g. [color] accent = %q) and write token(color.accent); for a one-off, use <style unsafe>", raw)
	}
}

func (c *cssCompiler) rawLength(prop string, cat category, raw string, at int) {
	group := cat.groups[0]
	e := c.errorf(diag.ST704, at, at+len(raw), "raw %s %s in %s", cat.what, raw, prop)
	if toks := c.opt.Tokens.closestTokens(group, raw); len(toks) > 0 {
		e.WithFix("use a %s token: %s; or add one to tokens.toml", group, hints(toks))
	} else {
		e.WithFix("define it in tokens.toml (e.g. [%s] md = %q) and write token(%s.md); for a one-off, use <style unsafe>", group, raw, group)
	}
}

// hints formats tokens with their values: token(space.4) (1rem), ...
func hints(toks []*Token) string {
	var parts []string
	for _, t := range toks {
		parts = append(parts, fmt.Sprintf("token(%s) (%s)", t.Ref(), t.Light))
	}
	return strings.Join(parts, ", ")
}

func groupNames(t *Tokens) []string {
	var out []string
	for _, g := range t.Groups {
		out = append(out, g.Name)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// closestName returns the candidate most similar to name, if any is close.
func closestName(name string, candidates []string) string {
	best, bestDist := "", max(2, len(name)/3)+1
	for _, c := range candidates {
		if d := distance(name, c); d > 0 && d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
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
