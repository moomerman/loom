// Package parser turns .hx source into an ast.File.
//
// Parsing stops at the first error: template errors tend to cascade (one
// unclosed tag confuses everything after it), so the first one is the one
// worth reading.
package parser

import (
	"strings"

	"github.com/moomerman/loom/compiler/ast"
	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/compiler/lexer"
	"github.com/moomerman/loom/compiler/source"
)

// voidElements never have children or a closing tag.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true,
	"input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true,
}

// Parse parses a whole .hx file.
func Parse(f *source.File) (*ast.File, error) {
	toks, err := lexer.Lex(f)
	if err != nil {
		return nil, err
	}
	p := &parser{f: f, toks: toks, file: &ast.File{Source: f}}
	if err := p.parse(); err != nil {
		return nil, err
	}
	return p.file, nil
}

// ParseString is a convenience for tests and tools.
func ParseString(name, text string) (*ast.File, error) {
	return Parse(source.NewFile(name, text))
}

type parser struct {
	f     *source.File
	toks  []lexer.Token
	i     int
	file  *ast.File
	comp  *ast.Component // the component being parsed, if any
	stack []*frame       // open tags, innermost last
}

// frame is a tag that has been opened but not yet closed.
type frame struct {
	name       string // as written: "li", ".footer", ":left"
	start, end int    // span of `<name`, for errors
	node       ast.Node
	children   []ast.Node
}

func (p *parser) errorf(code diag.Code, start, end int, format string, args ...any) *diag.Error {
	return diag.Errorf(code, p.f, start, end, format, args...)
}

func (p *parser) next() lexer.Token {
	t := p.toks[p.i]
	if t.Kind != lexer.EOF {
		p.i++
	}
	return t
}

func (p *parser) parse() error {
	for {
		t := p.next()
		if p.comp == nil && t.Kind != lexer.Decl && t.Kind != lexer.EOF {
			if t.Kind == lexer.Text && strings.TrimSpace(t.Val) == "" {
				continue
			}
			if n := len(p.file.Behaviors); n > 0 {
				b := p.file.Behaviors[n-1]
				return p.errorf(diag.PA240, t.Start, t.End, "behavior %s has no markup; its code is in %s.js", b.Name, b.Name).
					WithFix("put markup in a component, after a component declaration")
			}
			return p.errorf(diag.PA201, t.Start, t.End, "markup must come after a component declaration").
				WithFix("start the file with a signature, e.g. component TodoList(todos []Todo)")
		}
		var err error
		switch t.Kind {
		case lexer.EOF:
			if len(p.stack) > 0 {
				return p.unclosed(p.stack[len(p.stack)-1], "close it before the end of the file")
			}
			if p.comp == nil && len(p.file.Behaviors) > 0 {
				return nil // a file of behaviors only
			}
			if p.comp == nil {
				return p.errorf(diag.PA202, 0, 0, "no component declaration found").
					WithFix("start the file with a signature, e.g. component TodoList(todos []Todo)")
			}
			p.finishComponent()
			return nil
		case lexer.Decl:
			err = p.decl(t)
		case lexer.Text:
			p.add(&ast.Text{Pos: p.f.Pos(t.Start), Value: t.Val})
		case lexer.Interp:
			var x ast.Expr
			x, err = ParseExpr(p.f, t.InnerStart, t.InnerEnd)
			if err == nil {
				p.add(&ast.Interp{Pos: p.f.Pos(t.Start), X: x})
			}
		case lexer.TagOpen:
			err = p.openTag(t)
		case lexer.TagClose:
			err = p.closeTag(t)
		default:
			err = p.errorf(diag.PA221, t.Start, t.End, "unexpected %s", t.Kind). // the lexer never produces this
												WithFix("this is a bug in Loom; please report it with the template that caused it")
		}
		if err != nil {
			return err
		}
	}
}

func (p *parser) decl(t lexer.Token) error {
	if t.Val == "import" {
		return p.importLine(t)
	}
	if len(p.stack) > 0 {
		if t.Val == "behavior" {
			line := p.f.Pos(t.Start).Line
			return p.unclosed(p.stack[len(p.stack)-1], "close it before the behavior on line %d", line)
		}
		line := p.f.Pos(t.Start).Line
		return p.unclosed(p.stack[len(p.stack)-1], "close it before the next component, which starts on line %d", line)
	}
	if t.Val == "behavior" {
		b, err := parseBehavior(p.f, t.InnerStart, t.InnerEnd)
		if err != nil {
			return err
		}
		for _, other := range p.file.Behaviors {
			if other.Name == b.Name {
				return p.errorf(diag.PA240, b.Pos.Offset, b.Pos.Offset+len(b.Name), "behavior %s is declared twice", b.Name).
					WithFix("the first declaration is at line %d", other.Pos.Line)
			}
		}
		if p.comp != nil {
			p.finishComponent()
			p.comp = nil
		}
		p.file.Behaviors = append(p.file.Behaviors, b)
		return nil
	}
	c, err := parseDecl(p.f, t.InnerStart, t.InnerEnd)
	if err != nil {
		return err
	}
	for _, other := range p.file.Components {
		if other.Name == c.Name {
			return p.errorf(diag.PA204, c.Pos.Offset, c.Pos.Offset+len(c.Name), "component %s is declared twice", c.Name).
				WithFix("the first declaration is at line %d", other.Pos.Line)
		}
	}
	if p.comp != nil {
		p.finishComponent()
	}
	p.comp = c
	p.file.Components = append(p.file.Components, c)
	return nil
}

// importLine parses `import "path"`, which must come before the first
// component.
func (p *parser) importLine(t lexer.Token) error {
	const fix = `write import "module/path/components" before the first component`
	if p.comp != nil {
		return p.errorf(diag.PA239, t.Start, t.InnerStart, "imports must come before the first component").
			WithFix("move this line to the top of the file")
	}
	toks, err := lexer.LexExpr(p.f, t.InnerStart, t.InnerEnd)
	if err != nil {
		return err
	}
	if len(toks) != 2 || toks[0].Kind != lexer.XString || toks[0].Val == "" {
		return p.errorf(diag.PA239, t.Start, t.End, "an import is a quoted package path").WithFix(fix)
	}
	path := toks[0].Val
	for _, imp := range p.file.Imports {
		if imp.Path == path {
			return p.errorf(diag.PA239, t.Start, t.End, "%q is imported twice", path).
				WithFix("remove one of the imports")
		}
	}
	p.file.Imports = append(p.file.Imports, &ast.Import{Pos: p.f.Pos(toks[0].Start), Path: path})
	return nil
}

// finishComponent takes the component's <style> blocks out of its body
// and trims the whitespace around the body, which separates it from the
// declaration and from the next component.
func (p *parser) finishComponent() {
	var body []ast.Node
	for _, n := range p.comp.Body {
		if e, ok := n.(*ast.Element); ok && strings.EqualFold(e.Tag, "style") {
			p.comp.Styles = append(p.comp.Styles, e)
			continue
		}
		body = append(body, n)
	}
	// Trim whitespace around the body. Removing a style block can leave
	// two text nodes in a row, so trim until text that isn't blank.
	for _, n := range body {
		t, ok := n.(*ast.Text)
		if !ok {
			break
		}
		trimmed := strings.TrimLeft(t.Value, " \t\r\n")
		t.Pos = p.f.Pos(t.Pos.Offset + len(t.Value) - len(trimmed))
		t.Value = trimmed
		if trimmed != "" {
			break
		}
	}
	for i := len(body) - 1; i >= 0; i-- {
		t, ok := body[i].(*ast.Text)
		if !ok {
			break
		}
		t.Value = strings.TrimRight(t.Value, " \t\r\n")
		if t.Value != "" {
			break
		}
	}
	p.comp.Body = body[:0]
	for _, n := range body {
		if t, ok := n.(*ast.Text); ok && t.Value == "" {
			continue
		}
		p.comp.Body = append(p.comp.Body, n)
	}
}

// add appends n to the innermost open tag, or to the component body.
func (p *parser) add(n ast.Node) {
	if len(p.stack) > 0 {
		top := p.stack[len(p.stack)-1]
		top.children = append(top.children, n)
		return
	}
	p.comp.Body = append(p.comp.Body, n)
}

func (p *parser) unclosed(fr *frame, hint string, args ...any) error {
	return p.errorf(diag.PA203, fr.start, fr.end, "<%s> is never closed", fr.name).
		WithFix("add </%s>; "+hint, append([]any{fr.name}, args...)...)
}

// rawAttr is an attribute as lexed, before directives are separated out.
type rawAttr struct {
	name       string
	start, end int
	value      lexer.Token // AttrString, AttrExpr, or EOF for a bare attribute
}

func (p *parser) openTag(t lexer.Token) error {
	var raw []rawAttr
	var closer lexer.Token
	for closer.Kind == lexer.EOF {
		a := p.next()
		switch a.Kind {
		case lexer.AttrName:
			r := rawAttr{name: a.Val, start: a.Start, end: a.End}
			if k := p.toks[p.i].Kind; k == lexer.AttrString || k == lexer.AttrExpr {
				r.value = p.next()
				r.end = r.value.End
			}
			raw = append(raw, r)
		case lexer.TagEnd, lexer.TagSelfClose:
			closer = a
		default:
			return p.errorf(diag.PA221, a.Start, a.End, "unexpected %s in tag <%s>", a.Kind, t.Val). // the lexer never produces this
															WithFix("this is a bug in Loom; please report it with the template that caused it")
		}
	}
	selfClosing := closer.Kind == lexer.TagSelfClose
	fr := &frame{name: t.Val, start: t.Start, end: t.End}
	use, raw, err := p.use(t.Val, raw)
	if err != nil {
		return err
	}
	pos := p.f.Pos(t.Start)

	switch {
	case strings.HasPrefix(t.Val, "."):
		name := t.Val[1:]
		if !isSnakeCase(name) {
			return p.errorf(diag.PA205, t.Start, t.End, "invalid component name <%s>", t.Val).
				WithFix("call components by their snake_case name: <.%s> calls component %s", snakeCase(name), pascalCase(snakeCase(name)))
		}
		attrs, dir, err := p.attrs(t.Val, raw, true)
		if err != nil {
			return err
		}
		fr.node = &ast.ComponentCall{Pos: pos, Name: name, Attrs: attrs, Directives: dir}

	case strings.HasPrefix(t.Val, ":"):
		name := t.Val[1:]
		if !isIdent(name) {
			return p.errorf(diag.PA206, t.Start, t.End, "invalid slot name <%s>", t.Val).
				WithFix("a named slot looks like <:left>")
		}
		if len(p.stack) == 0 || !isCall(p.stack[len(p.stack)-1].node) {
			return p.errorf(diag.PA207, t.Start, t.End, "slot <%s> must be directly inside a component call", t.Val).
				WithFix("named slots pass markup to a component, e.g. <.footer><:left>Items</:left></.footer>")
		}
		attrs, _, err := p.attrs(t.Val, raw, false)
		if err != nil {
			return err
		}
		fr.node = &ast.Slot{Pos: pos, Name: name, Attrs: attrs}

	default:
		if !isTagName(t.Val) {
			return p.errorf(diag.PA208, t.Start, t.End, "invalid tag name <%s>", t.Val).
				WithFix("HTML tag names use letters, digits and '-'")
		}
		attrs, dir, err := p.attrs(t.Val, raw, true)
		if err != nil {
			return err
		}
		el := &ast.Element{Pos: pos, Tag: t.Val, Attrs: attrs, Directives: dir, Use: use, Void: voidElements[strings.ToLower(t.Val)]}
		fr.node = el
		if el.Void {
			selfClosing = true
		}
	}

	p.add(fr.node)
	if !selfClosing {
		p.stack = append(p.stack, fr)
	}
	return nil
}

// attrs validates a tag's attributes and separates out the directives.
func (p *parser) attrs(tag string, raw []rawAttr, directivesAllowed bool) ([]*ast.Attr, ast.Directives, error) {
	var attrs []*ast.Attr
	var dir ast.Directives
	var keyAttr *rawAttr
	seen := map[string]int{}

	for i := range raw {
		r := &raw[i]
		if first, ok := seen[r.name]; ok {
			return nil, dir, p.errorf(diag.PA209, r.start, r.start+len(r.name), "attribute %s is set twice on <%s>", r.name, tag).
				WithFix("the first one is at %s", p.f.Pos(first))
		}
		seen[r.name] = r.start

		isDirective := strings.HasPrefix(r.name, ":")
		if isDirective && !directivesAllowed {
			return nil, dir, p.errorf(diag.PA210, r.start, r.start+len(r.name), "%s can't be used on slot <%s>", r.name, tag).
				WithFix("put %s on the component call or on an element inside the slot instead", r.name)
		}

		switch {
		case r.name == ":for":
			if err := p.needExpr(r, ":for={item <- list}"); err != nil {
				return nil, dir, err
			}
			f, err := parseFor(p.f, r.value.InnerStart, r.value.InnerEnd)
			if err != nil {
				return nil, dir, err
			}
			dir.For = f
		case r.name == ":if":
			if err := p.needExpr(r, ":if={condition}"); err != nil {
				return nil, dir, err
			}
			x, err := ParseExpr(p.f, r.value.InnerStart, r.value.InnerEnd)
			if err != nil {
				return nil, dir, err
			}
			dir.If = x
		case r.name == ":key":
			if err := p.needExpr(r, ":key={item.id}"); err != nil {
				return nil, dir, err
			}
			x, err := ParseExpr(p.f, r.value.InnerStart, r.value.InnerEnd)
			if err != nil {
				return nil, dir, err
			}
			dir.Key = x
			keyAttr = r
		case r.name == ":else" || r.name == ":unless":
			return nil, dir, p.errorf(diag.PA212, r.start, r.start+len(r.name), "unknown directive %s", r.name).
				WithFix("Loom has no %s; put :if={!cond} on the other element, or pick between values with an inline if: {if cond, \"yes\", \"no\"}", r.name)
		case strings.HasPrefix(r.name, ":"):
			return nil, dir, p.errorf(diag.PA212, r.start, r.start+len(r.name), "unknown directive %s", r.name).
				WithFix("the supported directives are :for, :if and :key")
		case r.name == "key":
			return nil, dir, p.errorf(diag.PA236, r.start, r.start+len(r.name), "list keys are written :key, not key").
				WithFix("rename key to :key")
		case strings.HasPrefix(r.name, "phx-"):
			return nil, dir, p.phxAttr(r)
		case strings.HasPrefix(r.name, "on:"):
			if len(r.name) == len("on:") {
				return nil, dir, p.errorf(diag.PA213, r.start, r.end, "on: needs an event name").
					WithFix(`write the DOM event after the colon, e.g. on:click="save"`)
			}
			if r.value.Kind == lexer.AttrExpr {
				// Forwards an event parameter, on:click={on_click}
				// (checked by the type checker).
				x, err := ParseExpr(p.f, r.value.InnerStart, r.value.InnerEnd)
				if err != nil {
					return nil, dir, err
				}
				attrs = append(attrs, &ast.Attr{Pos: p.f.Pos(r.start), Name: r.name, Kind: ast.AttrExpr, X: x})
				continue
			}
			if r.value.Kind != lexer.AttrString || r.value.Val == "" {
				return nil, dir, p.errorf(diag.PA213, r.start, r.end, "%s takes the name of a server event as a quoted string", r.name).
					WithFix(`e.g. %s="toggle"; the payload goes in val-* attributes. In a component, %s={on_click} forwards its on:click event parameter`, r.name, r.name)
			}
			attrs = append(attrs, &ast.Attr{Pos: p.f.Pos(r.start), Name: r.name, Kind: ast.AttrString, Value: r.value.Val})
		default:
			a := &ast.Attr{Pos: p.f.Pos(r.start), Name: r.name}
			switch r.value.Kind {
			case lexer.AttrString:
				a.Kind, a.Value = ast.AttrString, r.value.Val
			case lexer.AttrExpr:
				parse := ParseExpr
				if r.name == "class" {
					parse = ParseClass
				}
				x, err := parse(p.f, r.value.InnerStart, r.value.InnerEnd)
				if err != nil {
					return nil, dir, err
				}
				a.Kind, a.X = ast.AttrExpr, x
			default:
				a.Kind = ast.AttrBare
			}
			attrs = append(attrs, a)
		}
	}

	if keyAttr != nil && dir.For == nil {
		return nil, dir, p.errorf(diag.PA214, keyAttr.start, keyAttr.end, ":key only applies to a tag with :for").
			WithFix(":key identifies each item of a list; add :for={item <- list} or remove :key")
	}
	return attrs, dir, nil
}

// use takes an element's use:name={...} out of its raw attributes.
func (p *parser) use(tag string, raw []rawAttr) (*ast.Use, []rawAttr, error) {
	var use *ast.Use
	rest := raw[:0:0]
	for _, r := range raw {
		if !strings.HasPrefix(r.name, "use:") {
			rest = append(rest, r)
			continue
		}
		name := r.name[len("use:"):]
		switch {
		case strings.HasPrefix(tag, ".") || strings.HasPrefix(tag, ":"):
			return nil, nil, p.errorf(diag.PA241, r.start, r.end, "use: goes on an HTML element, not <%s>", tag).
				WithFix("attach the behavior to an element inside the component's own markup")
		case use != nil:
			return nil, nil, p.errorf(diag.PA241, r.start, r.end, "<%s> already uses %s; an element takes one behavior", tag, use.Name).
				WithFix("put the second behavior on a wrapping or inner element")
		case !isSnake(name):
			return nil, nil, p.errorf(diag.PA241, r.start, r.end, "%s needs a snake_case behavior name", r.name).
				WithFix("write use:name={...}, e.g. use:dropdown={open: open}")
		case r.value.Kind == lexer.AttrString:
			return nil, nil, p.errorf(diag.PA241, r.start, r.end, "%s takes props in braces, not a string", r.name).
				WithFix("write %s={prop: value, ...}, or %s alone if it takes none", r.name, r.name)
		}
		use = &ast.Use{Pos: p.f.Pos(r.start), Name: name}
		if r.value.Kind == lexer.AttrExpr {
			props, err := ParseUse(p.f, r.value.InnerStart, r.value.InnerEnd)
			if err != nil {
				return nil, nil, err
			}
			use.Props = props
		}
	}
	return use, rest, nil
}

// phxAttr explains the Loom spelling of a Phoenix LiveView phx-* binding.
func (p *parser) phxAttr(r *rawAttr) error {
	err := p.errorf(diag.PA235, r.start, r.start+len(r.name), "%s is Phoenix LiveView syntax", r.name)
	switch name := strings.TrimPrefix(r.name, "phx-"); {
	case strings.HasPrefix(name, "value-"):
		return err.WithFix("event payload values are written val-%s={...}", strings.TrimPrefix(name, "value-"))
	case name == "click" || name == "change" || name == "submit" || name == "input" || name == "keydown" || name == "keyup" || name == "blur" || name == "focus":
		return err.WithFix(`events are bound with on:<dom event>, e.g. on:%s="event_name"`, name)
	default:
		return err.WithFix(`Loom binds events with on:<dom event>="name" and sends val-* attributes as the payload`)
	}
}

func (p *parser) needExpr(r *rawAttr, example string) error {
	if r.value.Kind == lexer.AttrExpr {
		return nil
	}
	return p.errorf(diag.PA211, r.start, r.end, "%s takes an expression in braces", r.name).
		WithFix("write %s", example)
}

func (p *parser) closeTag(t lexer.Token) error {
	if voidElements[strings.ToLower(t.Val)] {
		return p.errorf(diag.PA215, t.Start, t.End, "<%s> is a void element and has no closing tag", t.Val).
			WithFix("remove </%s>; void elements can also be written <%s />", t.Val, t.Val)
	}
	if len(p.stack) == 0 {
		return p.errorf(diag.PA216, t.Start, t.End, "</%s> has no matching opening tag", t.Val).
			WithFix("remove it, or add <%s> before it", t.Val)
	}
	top := p.stack[len(p.stack)-1]
	if top.name != t.Val {
		for _, fr := range p.stack {
			if fr.name == t.Val {
				return p.errorf(diag.PA217, t.Start, t.End, "</%s> closes <%s>, but <%s> inside it is still open", t.Val, t.Val, top.name).
					WithFix("add </%s> first; <%s> was opened at %s", top.name, top.name, p.f.Pos(top.start))
			}
		}
		return p.errorf(diag.PA218, t.Start, t.End, "</%s> doesn't match any open tag", t.Val).
			WithFix("the innermost open tag is <%s>, opened at %s", top.name, p.f.Pos(top.start))
	}
	p.stack = p.stack[:len(p.stack)-1]

	switch n := top.node.(type) {
	case *ast.Element:
		n.Children = top.children
	case *ast.Slot:
		n.Children = top.children
	case *ast.ComponentCall:
		// Named slots go to Slots; everything else is the default slot.
		var rest []ast.Node
		blank := true
		for _, c := range top.children {
			if s, ok := c.(*ast.Slot); ok {
				n.Slots = append(n.Slots, s)
				continue
			}
			if t, ok := c.(*ast.Text); !ok || strings.TrimSpace(t.Value) != "" {
				blank = false
			}
			rest = append(rest, c)
		}
		// Whitespace between named slots is formatting, not content.
		if !blank || len(n.Slots) == 0 {
			n.Children = rest
		}
	}
	return nil
}

func isCall(n ast.Node) bool {
	_, ok := n.(*ast.ComponentCall)
	return ok
}

func isSnakeCase(s string) bool {
	if s == "" || !('a' <= s[0] && s[0] <= 'z') {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !('a' <= s[i] && s[i] <= 'z' || isDigit(s[i]) || s[i] == '_') {
			return false
		}
	}
	return true
}

// snakeCase converts a name like TodoList or todoList to todo_list.
func snakeCase(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z':
			if i > 0 && s[i-1] != '_' {
				b.WriteByte('_')
			}
			b.WriteByte(c + 'a' - 'A')
		case c == '-' || c == '.':
			b.WriteByte('_')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// pascalCase converts a snake_case name like todo_list to TodoList.
func pascalCase(s string) string {
	var b strings.Builder
	upper := true
	for i := 0; i < len(s); i++ {
		if s[i] == '_' {
			upper = true
			continue
		}
		c := s[i]
		if upper && 'a' <= c && c <= 'z' {
			c -= 'a' - 'A'
		}
		upper = false
		b.WriteByte(c)
	}
	return b.String()
}

func isIdent(s string) bool {
	if s == "" || isDigit(s[0]) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !(isLetter(s[i]) || isDigit(s[i]) || s[i] == '_') {
			return false
		}
	}
	return true
}

func isTagName(s string) bool {
	if s == "" || !isLetter(s[0]) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !(isLetter(s[i]) || isDigit(s[i]) || s[i] == '-') {
			return false
		}
	}
	return true
}

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }
func isDigit(c byte) bool  { return '0' <= c && c <= '9' }
