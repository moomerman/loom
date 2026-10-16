// Package style compiles a component's <style> block and the app's design
// tokens.
//
// Design tokens live in tokens.toml: named values in groups (color,
// space, radius, text, font, shadow, duration, and any others an app
// adds). They compile to CSS custom properties, and component styles
// refer to them as token(group.name). A style block is scoped to its
// component: every selector is rewritten to need the component's scope
// class, so styles never leak. Raw values for colors, spacing, radii,
// font sizes and shadows are errors whose fix lists the closest tokens,
// so UI built by different hands (or agents) stays on one system.
package style

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/compiler/source"
)

// TokensFile is the name of the design tokens file.
const TokensFile = "tokens.toml"

// Tokens is a parsed tokens.toml.
type Tokens struct {
	File   *source.File
	Groups []*Group
	Themes []*Theme
}

// Theme is a named set of overrides of the tokens, declared under
// [theme.NAME.GROUP] headers and applied to an element and everything
// in it with data-theme="NAME".
type Theme struct {
	Name   string
	Offset int // byte offset of its first header's name
	Groups []*Group
}

// Group returns the theme's named group, or nil.
func (th *Theme) Group(name string) *Group {
	for _, g := range th.Groups {
		if g.Name == name {
			return g
		}
	}
	return nil
}

// Theme returns the named theme, or nil.
func (t *Tokens) Theme(name string) *Theme {
	if t == nil {
		return nil
	}
	for _, th := range t.Themes {
		if th.Name == name {
			return th
		}
	}
	return nil
}

// ThemeNames returns the themes' names, in the order declared.
func (t *Tokens) ThemeNames() []string {
	var names []string
	if t != nil {
		for _, th := range t.Themes {
			names = append(names, th.Name)
		}
	}
	return names
}

// Group is a [group] of tokens, e.g. [color].
type Group struct {
	Name   string
	Tokens []*Token
}

// Token is one design token. Dark is its value in dark mode, if it has
// one: only colors and shadows may.
type Token struct {
	Group  string
	Name   string
	Offset int // byte offset of the name in the tokens file
	Light  string
	Dark   string
}

// Ref returns how styles refer to the token: group.name.
func (t *Token) Ref() string { return t.Group + "." + t.Name }

// Var returns the CSS custom property the token compiles to.
func (t *Token) Var() string { return "--" + t.Group + "-" + t.Name }

// Kind is what kind of value a token group holds. Groups Loom knows are
// typed: their values are checked when tokens.toml is read.
type Kind int

const (
	Any      Kind = iota // a group the app added; any value
	Color                // color: #4f46e5, rgb(...), hsl(...), oklch(...)
	Length               // space, radius, text: 0.5rem, 4px
	Font                 // font: a font-family list
	Shadow               // shadow: a box-shadow value
	Duration             // duration: 150ms, 0.2s
)

// groupKinds are the groups Loom knows about.
var groupKinds = map[string]Kind{
	"color":    Color,
	"space":    Length,
	"radius":   Length,
	"text":     Length,
	"font":     Font,
	"shadow":   Shadow,
	"duration": Duration,
}

// GroupKind returns the kind of values in the named group.
func GroupKind(group string) Kind { return groupKinds[group] }

// Group returns the named group, or nil.
func (t *Tokens) Group(name string) *Group {
	if t == nil {
		return nil
	}
	for _, g := range t.Groups {
		if g.Name == name {
			return g
		}
	}
	return nil
}

// Lookup returns the token group.name, or nil.
func (t *Tokens) Lookup(group, name string) *Token {
	g := t.Group(group)
	if g == nil {
		return nil
	}
	for _, tok := range g.Tokens {
		if tok.Name == name {
			return tok
		}
	}
	return nil
}

// Refs returns every token's reference, for "did you mean" hints.
func (t *Tokens) Refs() []string {
	var refs []string
	if t == nil {
		return refs
	}
	for _, g := range t.Groups {
		for _, tok := range g.Tokens {
			refs = append(refs, tok.Ref())
		}
	}
	return refs
}

// Find returns the tokens file that applies to templates in dir: the
// nearest tokens.toml in dir or a parent, stopping at the directory with
// go.mod. It returns "" if there is none.
func Find(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		path := filepath.Join(dir, TokensFile)
		if _, err := os.Stat(path); err == nil {
			return path
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Load reads and parses a tokens file.
func Load(path string) (*Tokens, diag.List, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	t, errs := ParseTokens(source.NewFile(path, string(text)))
	return t, errs, nil
}

// ParseTokens parses tokens.toml. The format is a small subset of TOML:
//
//	# comments
//	[color]
//	accent    = { light = "#4f46e5", dark = "#818cf8" }
//	on-accent = "#ffffff"
//
//	[space]
//	2 = "0.5rem"
//
// Tables name groups; keys name tokens; values are strings, or inline
// tables with light and dark strings. It returns the tokens even when
// there are errors, leaving out the entries that failed.
func ParseTokens(f *source.File) (*Tokens, diag.List) {
	p := &tokenParser{f: f, t: &Tokens{File: f}}
	p.parse()
	p.themeTokens()
	p.t.contrast(&p.errs)
	return p.t, p.errs
}

type tokenParser struct {
	f     *source.File
	t     *Tokens
	errs  diag.List
	group *Group
	theme *Theme         // the theme group is in, or nil for the tokens'
	at    map[*Group]int // offset of each theme group's header
}

// lookup returns the token name in the current group, or nil.
func (p *tokenParser) lookup(name string) *Token {
	for _, tok := range p.group.Tokens {
		if tok.Name == name {
			return tok
		}
	}
	return nil
}

// themeHeader starts [theme.NAME.GROUP], whose header is at start:end.
func (p *tokenParser) themeHeader(head string, parts []string, start, end int) {
	p.group = &Group{} // tokens under a bad header are read and dropped
	if head != "theme" || len(parts) != 2 {
		p.errorf(start, end, "invalid group header").
			WithFix("a group header looks like [color], or [theme.NAME.color] for a theme's colors")
		return
	}
	name, group := parts[0], parts[1]
	if !validName(name) || name == "light" || name == "dark" {
		p.errs = append(p.errs, diag.Errorf(diag.ST711, p.f, start, end, "invalid theme name %q", name).
			WithFix("name a theme with lower-case letters, digits and '-', other than light and dark (the modes), e.g. [theme.amiga.color]"))
		return
	}
	th := p.t.Theme(name)
	if th == nil {
		th = &Theme{Name: name, Offset: start}
		p.t.Themes = append(p.t.Themes, th)
	}
	if th.Group(group) != nil {
		p.errorf(start, end, "group [theme.%s.%s] is declared twice", name, group).
			WithFix("put all of its tokens under one [theme.%s.%s] header", name, group)
		return
	}
	if p.at == nil {
		p.at = map[*Group]int{}
	}
	p.theme, p.group = th, &Group{Name: group}
	p.at[p.group] = start
	th.Groups = append(th.Groups, p.group)
}

// themeTokens checks that each theme overrides only tokens there are,
// once the whole file is read.
func (p *tokenParser) themeTokens() {
	for _, th := range p.t.Themes {
		for _, g := range th.Groups {
			base := p.t.Group(g.Name)
			if base == nil {
				at := p.at[g]
				p.errs = append(p.errs, diag.Errorf(diag.ST711, p.f, at, at+len("theme.."+th.Name+g.Name), "theme %s overrides group [%s], which has no tokens", th.Name, g.Name).
					WithFix("a theme overrides tokens declared outside it; declare the group's tokens first, or use one of: %s", strings.Join(p.groupNames(), ", ")))
				g.Tokens = nil
				continue
			}
			var kept []*Token
			for _, tok := range g.Tokens {
				if p.t.Lookup(g.Name, tok.Name) == nil {
					var names []string
					for _, b := range base.Tokens {
						names = append(names, b.Name)
					}
					e := diag.Errorf(diag.ST711, p.f, tok.Offset, tok.Offset+len(tok.Name), "theme %s overrides %s.%s, which isn't a token", th.Name, g.Name, tok.Name)
					if m := closestName(tok.Name, names); m != "" {
						e.WithFix("did you mean %s? a theme overrides tokens declared outside it", m)
					} else {
						e.WithFix("declare %s.%s outside the theme first: a theme only overrides tokens", g.Name, tok.Name)
					}
					p.errs = append(p.errs, e)
					continue
				}
				kept = append(kept, tok)
			}
			g.Tokens = kept
		}
	}
}

func (p *tokenParser) groupNames() []string {
	var names []string
	for _, g := range p.t.Groups {
		names = append(names, g.Name)
	}
	return names
}

const tokensExample = "a tokens file looks like:\n[color]\naccent = { light = \"#4f46e5\", dark = \"#818cf8\" }\n[space]\n2 = \"0.5rem\""

func (p *tokenParser) errorf(start, end int, format string, args ...any) *diag.Error {
	e := diag.Errorf(diag.ST701, p.f, start, end, format, args...)
	p.errs = append(p.errs, e)
	return e
}

func (p *tokenParser) parse() {
	start := 0
	for _, line := range strings.SplitAfter(p.f.Text, "\n") {
		p.line(strings.TrimRight(line, "\r\n"), start)
		start += len(line)
	}
}

// line parses one line, which starts at byte offset start.
func (p *tokenParser) line(line string, start int) {
	s := &scanner{src: line, base: start}
	s.space()
	if s.done() || s.peek() == '#' {
		return
	}
	if s.peek() == '[' {
		s.pos++
		s.space()
		at := s.pos
		name := s.key()
		var parts []string
		for s.peek() == '.' {
			s.pos++
			parts = append(parts, s.key())
		}
		s.space()
		if name == "" || s.peek() != ']' {
			p.errorf(start, start+len(line), "invalid group header").WithFix("a group header looks like [color], or [theme.NAME.color] in a theme")
			return
		}
		s.pos++
		if !p.rest(s) {
			return
		}
		if len(parts) > 0 {
			p.themeHeader(name, parts, s.base+at, start+len(line))
			return
		}
		if !validName(name) {
			p.errorf(s.base+at, s.base+at+len(name), "invalid group name %q", name).
				WithFix("group names use lower-case letters, digits and '-', e.g. [color]")
			return
		}
		if g := p.t.Group(name); g != nil {
			p.errorf(s.base+at, s.base+at+len(name), "group [%s] is declared twice", name).
				WithFix("put all of its tokens under one [%s] header", name)
			p.group = g
			return
		}
		p.group = &Group{Name: name}
		p.t.Groups = append(p.t.Groups, p.group)
		p.theme = nil
		return
	}

	at := s.pos
	name := s.key()
	if name == "" {
		p.errorf(start+at, start+at+1, "expected a token name").WithFix("%s", tokensExample)
		return
	}
	nameStart, nameEnd := start+at, start+at+len(name)
	s.space()
	if s.peek() != '=' {
		p.errorf(nameStart, nameEnd, "expected '=' after %s", name).WithFix(`write %s = "value"`, name)
		return
	}
	s.pos++
	s.space()
	if p.group == nil {
		p.errorf(nameStart, nameEnd, "token %s is not in a group", name).
			WithFix("put a group header such as [color] or [space] above it")
		return
	}
	if !validName(name) {
		p.errorf(nameStart, nameEnd, "invalid token name %q", name).
			WithFix("token names use lower-case letters, digits and '-', e.g. on-accent or 2")
		return
	}
	tok := &Token{Group: p.group.Name, Name: name, Offset: nameStart}
	switch s.peek() {
	case '"', '\'':
		v, ok := p.str(s)
		if !ok {
			return
		}
		tok.Light = v
	case '{':
		if !p.inline(s, tok) {
			return
		}
	default:
		p.errorf(s.base+s.pos, s.base+s.pos+1, "the value of %s must be a quoted string", name).
			WithFix(`write %s = "value", or { light = "...", dark = "..." } for a color with a dark value`, name)
		return
	}
	if !p.rest(s) {
		return
	}
	if p.lookup(name) != nil {
		p.errorf(nameStart, nameEnd, "token %s.%s is declared twice", tok.Group, name).WithFix("remove one of them")
		return
	}
	if !p.checkValue(tok, nameStart, nameEnd) {
		return
	}
	p.group.Tokens = append(p.group.Tokens, tok)
}

// inline parses { light = "...", dark = "..." }.
func (p *tokenParser) inline(s *scanner, tok *Token) bool {
	open := s.pos
	s.pos++
	seen := map[string]bool{}
	for {
		s.space()
		if s.peek() == '}' {
			s.pos++
			break
		}
		at := s.pos
		k := s.key()
		if k != "light" && k != "dark" {
			p.errorf(s.base+at, s.base+at+max(1, len(k)), "unexpected %q in %s's value", k, tok.Name).
				WithFix(`an inline value is { light = "...", dark = "..." }`)
			return false
		}
		if seen[k] {
			p.errorf(s.base+at, s.base+at+len(k), "%s is given twice", k).WithFix("give it once")
			return false
		}
		seen[k] = true
		s.space()
		if s.peek() != '=' {
			p.errorf(s.base+at, s.base+at+len(k), "expected '=' after %s", k).WithFix(`write %s = "value"`, k)
			return false
		}
		s.pos++
		s.space()
		v, ok := p.str(s)
		if !ok {
			return false
		}
		if k == "light" {
			tok.Light = v
		} else {
			tok.Dark = v
		}
		s.space()
		if s.peek() == ',' {
			s.pos++
			continue
		}
		if s.peek() != '}' {
			p.errorf(s.base+s.pos, s.base+s.pos+1, "expected ',' or '}'").WithFix(`an inline value is { light = "...", dark = "..." }`)
			return false
		}
	}
	if !seen["light"] {
		p.errorf(s.base+open, s.base+s.pos, "%s has no light value", tok.Name).
			WithFix(`give both: { light = "...", dark = "..." }, or write a plain string for a value that doesn't change`)
		return false
	}
	return true
}

// str parses a quoted string at s.
func (p *tokenParser) str(s *scanner) (string, bool) {
	q := s.peek()
	if q != '"' && q != '\'' {
		p.errorf(s.base+s.pos, s.base+s.pos+1, "expected a quoted string").WithFix(`quote the value, e.g. "0.5rem"`)
		return "", false
	}
	end := strings.IndexByte(s.src[s.pos+1:], q)
	if end < 0 {
		p.errorf(s.base+s.pos, s.base+len(s.src), "string is never closed").WithFix("add a closing %c", q)
		return "", false
	}
	v := s.src[s.pos+1 : s.pos+1+end]
	s.pos += end + 2
	if strings.ContainsAny(v, ";{}<>\\") {
		p.errorf(s.base+s.pos-len(v)-1, s.base+s.pos-1, "token values can't contain ; { } < > or \\").
			WithFix("a token is one CSS value, e.g. \"0.5rem\" or \"#4f46e5\"")
		return "", false
	}
	return strings.TrimSpace(v), true
}

// rest checks that only a comment follows.
func (p *tokenParser) rest(s *scanner) bool {
	s.space()
	if s.done() || s.peek() == '#' {
		return true
	}
	p.errorf(s.base+s.pos, s.base+len(s.src), "unexpected text after the value").
		WithFix("put one token per line; comments start with #")
	return false
}

// checkValue checks a token's values against its group's kind.
func (p *tokenParser) checkValue(tok *Token, start, end int) bool {
	kind := GroupKind(tok.Group)
	if tok.Dark != "" && kind != Color && kind != Shadow && kind != Any {
		p.errs = append(p.errs, diag.Errorf(diag.ST702, p.f, start, end, "%s has a dark value, but only colors and shadows can", tok.Ref()).
			WithFix("give %s one value", tok.Ref()))
		return false
	}
	values := []string{tok.Light}
	if tok.Dark != "" {
		values = append(values, tok.Dark)
	}
	for _, v := range values {
		if msg, fix := checkKind(kind, v); msg != "" {
			p.errs = append(p.errs, diag.Errorf(diag.ST702, p.f, start, end, "%s is %q, %s", tok.Ref(), v, msg).WithFix("%s", fix))
			return false
		}
	}
	return true
}

func checkKind(kind Kind, v string) (msg, fix string) {
	if v == "" {
		return "which is empty", "give it a value"
	}
	switch kind {
	case Color:
		if _, ok := ParseColor(v); !ok {
			return "which is not a color", `write a color as #rrggbb, rgb(...), hsl(...) or oklch(...), e.g. "#4f46e5"`
		}
	case Length:
		if _, ok := lengthPx(v); !ok && !isZero(v) {
			return "which is not a length", `write a length with a unit, e.g. "0.5rem" or "4px"`
		}
	case Duration:
		if !isDuration(v) {
			return "which is not a duration", `write a duration, e.g. "150ms" or "0.2s"`
		}
	}
	return "", ""
}

// contrast checks every color pair X / on-X against WCAG AA (4.5:1) in
// light and dark mode, adding a warning for each that fails: the
// tokens' pairs, then each theme's, where it overrides either color.
func (t *Tokens) contrast(errs *diag.List) {
	t.pairs(errs, "", func(name string) *Token { return t.Lookup("color", name) }, nil)
	for _, th := range t.Themes {
		g := th.Group("color")
		if g == nil {
			continue
		}
		own := func(name string) *Token {
			for _, tok := range g.Tokens {
				if tok.Name == name {
					return tok
				}
			}
			return nil
		}
		color := func(name string) *Token {
			if tok := own(name); tok != nil {
				return tok
			}
			return t.Lookup("color", name)
		}
		t.pairs(errs, " in theme "+th.Name, color, own)
	}
}

// pairs checks the pairs of color tokens that color looks up. With own,
// only pairs where own finds either token, and the warning is on that.
func (t *Tokens) pairs(errs *diag.List, where string, color, own func(string) *Token) {
	g := t.Group("color")
	if g == nil {
		return
	}
	for _, name := range g.Tokens {
		on, base := color(name.Name), color(strings.TrimPrefix(name.Name, "on-"))
		if !strings.HasPrefix(on.Name, "on-") || base == nil {
			continue
		}
		at := on
		if own != nil {
			if at = own(on.Name); at == nil {
				if at = own(base.Name); at == nil {
					continue
				}
			}
		}
		for _, mode := range []string{"light", "dark"} {
			bg, fg := base.Light, on.Light
			if mode == "dark" {
				if base.Dark == "" && on.Dark == "" {
					continue
				}
				bg, fg = or(base.Dark, base.Light), or(on.Dark, on.Light)
			}
			ratio := ContrastRatio(bg, fg)
			if ratio == 0 || ratio >= 4.5 {
				continue
			}
			*errs = append(*errs, diag.Warnf(diag.ST706, t.File, at.Offset, at.Offset+len(at.Name),
				"%s on %s has contrast %.2f:1 in %s mode%s, below the 4.5:1 WCAG AA needs for text", on.Ref(), base.Ref(), ratio, mode, where).
				WithFix("make %s or %s lighter or darker until the ratio is at least 4.5:1", on.Ref(), base.Ref()))
		}
	}
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// CSS returns the tokens as CSS custom properties on :root. Dark values
// apply under prefers-color-scheme: dark, unless the document has
// data-theme="light", and always under data-theme="dark".
func (t *Tokens) CSS() string { return t.css(func(sel string) string { return sel }) }

// DefaultsCSS is CSS for a package shipped to other apps, such as a
// sub-app: the same properties under :where(), which has no
// specificity, so the host app's own tokens on :root win wherever the
// two define the same name, in whichever order the sheets load. The
// package's pages take the host's brand, and its other tokens still
// apply.
func (t *Tokens) DefaultsCSS() string {
	return t.css(func(sel string) string { return ":where(" + sel + ")" })
}

func (t *Tokens) css(sel func(string) string) string {
	var light, dark []string
	for _, g := range t.Groups {
		for _, tok := range g.Tokens {
			light = append(light, tok.Var()+":"+tok.Light+";")
			if tok.Dark != "" {
				dark = append(dark, tok.Var()+":"+tok.Dark+";")
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s{color-scheme:light dark;%s}\n", sel(":root"), strings.Join(light, ""))
	fmt.Fprintf(&b, "%s{color-scheme:light}\n", sel(":root[data-theme=light]"))
	if len(dark) > 0 {
		fmt.Fprintf(&b, "@media (prefers-color-scheme:dark){%s{%s}}\n", sel(":root:not([data-theme=light])"), strings.Join(dark, ""))
		fmt.Fprintf(&b, "%s{color-scheme:dark;%s}\n", sel(":root[data-theme=dark]"), strings.Join(dark, ""))
	} else {
		fmt.Fprintf(&b, "%s{color-scheme:dark}\n", sel(":root[data-theme=dark]"))
	}
	// A theme's values apply to the element with data-theme="NAME" and
	// everything in it. Its dark values follow the document's mode, as
	// the tokens' do; a value with no dark one applies in both.
	for _, th := range t.Themes {
		var light, dark []string
		for _, g := range th.Groups {
			for _, tok := range g.Tokens {
				light = append(light, tok.Var()+":"+tok.Light+";")
				if tok.Dark != "" {
					dark = append(dark, tok.Var()+":"+tok.Dark+";")
				}
			}
		}
		at := "[data-theme=" + th.Name + "]"
		fmt.Fprintf(&b, "%s{%s}\n", sel(at), strings.Join(light, ""))
		if len(dark) > 0 {
			fmt.Fprintf(&b, "@media (prefers-color-scheme:dark){%s{%s}}\n", sel(":root:not([data-theme=light]) "+at), strings.Join(dark, ""))
			fmt.Fprintf(&b, "%s{%s}\n", sel(":root[data-theme=dark] "+at), strings.Join(dark, ""))
		}
	}
	return b.String()
}

// closestTokens returns up to three tokens of the given kinds nearest to a
// raw value, best first, for fix hints.
func (t *Tokens) closestTokens(group string, raw string) []*Token {
	g := t.Group(group)
	if g == nil {
		return nil
	}
	type scored struct {
		tok  *Token
		dist float64
	}
	var all []scored
	for _, tok := range g.Tokens {
		d, ok := valueDistance(GroupKind(group), raw, tok.Light)
		if ok {
			all = append(all, scored{tok, d})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].dist < all[j].dist })
	var out []*Token
	for i := 0; i < len(all) && i < 3; i++ {
		out = append(out, all[i].tok)
	}
	return out
}

func valueDistance(kind Kind, a, b string) (float64, bool) {
	switch kind {
	case Color:
		ca, ok1 := ParseColor(a)
		cb, ok2 := ParseColor(b)
		if !ok1 || !ok2 {
			return 0, false
		}
		return ca.distance(cb), true
	case Length:
		pa, ok1 := lengthPx(a)
		pb, ok2 := lengthPx(b)
		if !ok1 || !ok2 {
			return 0, false
		}
		if pa > pb {
			return pa - pb, true
		}
		return pb - pa, true
	}
	return 0, true
}

// validName reports whether s is a valid group or token name.
func validName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' && i > 0) {
			return false
		}
	}
	return true
}

// scanner reads one line of tokens.toml.
type scanner struct {
	src  string
	pos  int
	base int // byte offset of src in the file
}

func (s *scanner) done() bool { return s.pos >= len(s.src) }

func (s *scanner) peek() byte {
	if s.done() {
		return 0
	}
	return s.src[s.pos]
}

func (s *scanner) space() {
	for !s.done() && (s.src[s.pos] == ' ' || s.src[s.pos] == '\t' || s.src[s.pos] == '\r') {
		s.pos++
	}
}

// key reads a bare or quoted key.
func (s *scanner) key() string {
	if q := s.peek(); q == '"' {
		end := strings.IndexByte(s.src[s.pos+1:], q)
		if end < 0 {
			return ""
		}
		k := s.src[s.pos+1 : s.pos+1+end]
		s.pos += end + 2
		return k
	}
	start := s.pos
	for !s.done() {
		c := s.src[s.pos]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '_') {
			break
		}
		s.pos++
	}
	return s.src[start:s.pos]
}
