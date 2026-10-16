package parser

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/moomerman/loom/compiler/ast"
	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/compiler/lexer"
	"github.com/moomerman/loom/compiler/source"
)

// precedence of binary operators; higher binds tighter.
var precedence = map[string]int{
	"||": 1,
	"&&": 2,
	"==": 3, "!=": 3, "<": 3, "<=": 3, ">": 3, ">=": 3,
	"+": 4, "-": 4,
	"*": 5, "/": 5, "%": 5,
}

// exprParser is a precedence-climbing parser over expression tokens.
type exprParser struct {
	f    *source.File
	toks []lexer.ExprToken
	i    int
	// classList is set in a class attribute, where an inline if may leave
	// out its else part.
	classList bool
}

func newExprParser(f *source.File, start, end int) (*exprParser, error) {
	toks, err := lexer.LexExpr(f, start, end)
	if err != nil {
		return nil, err
	}
	return &exprParser{f: f, toks: toks}, nil
}

// ParseExpr parses the expression in the source range [start, end).
func ParseExpr(f *source.File, start, end int) (ast.Expr, error) {
	p, err := newExprParser(f, start, end)
	if err != nil {
		return nil, err
	}
	if p.peek().Kind == lexer.XEOF {
		return nil, diag.Errorf(diag.PA219, f, start-1, end+1, "empty expression").
			WithFix("put an expression between the braces, or remove them")
	}
	x, err := p.expr()
	if err != nil {
		return nil, err
	}
	return x, p.expectEOF()
}

func (p *exprParser) peek() lexer.ExprToken { return p.toks[p.i] }

func (p *exprParser) next() lexer.ExprToken {
	t := p.toks[p.i]
	if t.Kind != lexer.XEOF {
		p.i++
	}
	return t
}

func (p *exprParser) isOp(op string) bool {
	t := p.peek()
	return t.Kind == lexer.XOp && t.Val == op
}

func (p *exprParser) pos(t lexer.ExprToken) source.Pos { return p.f.Pos(t.Start) }

func (p *exprParser) errorAt(code diag.Code, t lexer.ExprToken, format string, args ...any) *diag.Error {
	return diag.Errorf(code, p.f, t.Start, t.End, format, args...)
}

// expect consumes the operator op, or fails with code and msg.
func (p *exprParser) expect(code diag.Code, op, msg, fix string) error {
	if !p.isOp(op) {
		return p.errorAt(code, p.peek(), "%s, found %s", msg, describe(p.peek())).WithFix("%s", fix)
	}
	p.next()
	return nil
}

func (p *exprParser) expectEOF() error {
	t := p.peek()
	switch {
	case t.Kind == lexer.XEOF:
		return nil
	case t.Kind == lexer.XOp && t.Val == "<-":
		return p.errorAt(diag.PA224, t, "'<-' can only be used in :for").
			WithFix("to repeat markup, put :for={item <- list} on the element to repeat")
	case t.Kind == lexer.XOp && t.Val == "=":
		return p.errorAt(diag.PA220, t, "assignment isn't allowed in templates").
			WithFix("templates only read values; to compare, use ==")
	default:
		return p.errorAt(diag.PA221, t, "unexpected %s after the end of the expression", describe(t)).
			WithFix("an expression is a single value; did you miss an operator?")
	}
}

func (p *exprParser) expr() (ast.Expr, error) { return p.binary(1) }

func (p *exprParser) binary(minPrec int) (ast.Expr, error) {
	x, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		prec, ok := precedence[t.Val]
		if t.Kind != lexer.XOp || !ok || prec < minPrec {
			return x, nil
		}
		p.next()
		if n := p.peek(); t.Val == "<" && n.Kind == lexer.XOp && n.Val == ">" && n.Start == t.End {
			return nil, diag.Errorf(diag.PA237, p.f, t.Start, n.End, "<> isn't an operator in Loom templates").
				WithFix("join strings with +, e.g. \"/jobs/\" + job.id")
		}
		y, err := p.binary(prec + 1)
		if err != nil {
			return nil, err
		}
		x = &ast.Binary{Pos: p.pos(t), Op: t.Val, X: x, Y: y}
	}
}

func (p *exprParser) unary() (ast.Expr, error) {
	if p.isOp("!") || p.isOp("-") {
		t := p.next()
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &ast.Unary{Pos: p.pos(t), Op: t.Val, X: x}, nil
	}
	x, err := p.primary()
	if err != nil {
		return nil, err
	}
	return p.postfix(x)
}

func (p *exprParser) primary() (ast.Expr, error) {
	t := p.next()
	pos := p.pos(t)
	switch t.Kind {
	case lexer.XIdent:
		switch t.Val {
		case "true", "false":
			return &ast.BoolLit{Pos: pos, Value: t.Val == "true"}, nil
		case "if":
			return p.cond(t)
		}
		return &ast.Ident{Pos: pos, Name: t.Val}, nil
	case lexer.XInt:
		v, err := strconv.ParseInt(t.Val, 10, 64)
		if err != nil {
			return nil, p.errorAt(diag.PA234, t, "number %s is too large", t.Val).
				WithFix("integers must fit in 64 bits")
		}
		return &ast.IntLit{Pos: pos, Value: v}, nil
	case lexer.XFloat:
		v, err := strconv.ParseFloat(t.Val, 64)
		if err != nil {
			return nil, p.errorAt(diag.PA234, t, "invalid number %s", t.Val).
				WithFix("write numbers like 42 or 4.2")
		}
		return &ast.FloatLit{Pos: pos, Value: v}, nil
	case lexer.XString:
		return &ast.StringLit{Pos: pos, Value: t.Val}, nil
	case lexer.XEOF:
		return nil, p.errorAt(diag.PA222, t, "expression ends too early").
			WithFix("something is missing at the end, such as the right-hand side of an operator")
	}
	switch t.Val {
	case "(":
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		return x, p.expect(diag.PA226, ")", "expected ')'", "every '(' needs a matching ')'")
	case "<-":
		return nil, p.errorAt(diag.PA224, t, "'<-' can only be used in :for").
			WithFix("write :for={item <- list} on the element to repeat")
	case "[":
		return nil, p.errorAt(diag.PA223, t, "unexpected '[': templates have no list literals").
			WithFix(`for classes, list the values separated by spaces, with an inline if for optional ones: class={"btn" if busy, "busy"}`)
	}
	return nil, p.errorAt(diag.PA223, t, "unexpected %s", describe(t)).
		WithFix("expected a value: a name, a number, a string, or '('")
}

func (p *exprParser) postfix(x ast.Expr) (ast.Expr, error) {
	for {
		t := p.peek()
		if t.Kind != lexer.XOp {
			return x, nil
		}
		switch t.Val {
		case ".":
			p.next()
			name := p.next()
			if name.Kind != lexer.XIdent {
				return nil, p.errorAt(diag.PA226, name, "expected a field name after '.', found %s", describe(name)).
					WithFix("write the field name after the dot, e.g. t.title")
			}
			x = &ast.Field{Pos: p.pos(t), X: x, Name: name.Val}
		case "[":
			p.next()
			i, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expect(diag.PA226, "]", "expected ']'", "every '[' needs a matching ']'"); err != nil {
				return nil, err
			}
			x = &ast.Index{Pos: p.pos(t), X: x, Index: i}
		case "(":
			fn, ok := x.(*ast.Ident)
			if !ok {
				return nil, diag.Errorf(diag.PA225, p.f, x.Position().Offset, t.End, "only helpers can be called").
					WithFix("templates can't call methods; compute the value in Go and pass it in, or register a helper like len(list)")
			}
			p.next()
			args, err := p.args()
			if err != nil {
				return nil, err
			}
			x = &ast.Call{Pos: fn.Pos, Func: fn, Args: args}
		default:
			return x, nil
		}
	}
}

func (p *exprParser) args() ([]ast.Expr, error) {
	var args []ast.Expr
	if p.isOp(")") {
		p.next()
		return args, nil
	}
	for {
		a, err := p.expr()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		if p.isOp(",") {
			p.next()
			continue
		}
		return args, p.expect(diag.PA226, ")", "expected ',' or ')' in the argument list", "separate arguments with commas and close the call with ')'")
	}
}

// ParseClass parses a class attribute's value: one expression, or a list
// of them separated by spaces, in which an inline if may leave out its
// else part.
func ParseClass(f *source.File, start, end int) (ast.Expr, error) {
	p, err := newExprParser(f, start, end)
	if err != nil {
		return nil, err
	}
	p.classList = true
	if p.peek().Kind == lexer.XEOF {
		return nil, diag.Errorf(diag.PA219, f, start-1, end+1, "empty expression").
			WithFix("put an expression between the braces, or remove them")
	}
	first := p.peek()
	var items []ast.Expr
	for p.peek().Kind != lexer.XEOF {
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		items = append(items, x)
		if t := p.peek(); t.Kind == lexer.XOp && t.Val != "(" && t.Val != "!" && t.Val != "-" {
			return nil, p.expectEOF()
		}
	}
	if len(items) == 1 {
		if c, ok := items[0].(*ast.Cond); !ok || c.Else != nil {
			return items[0], nil
		}
	}
	return &ast.ClassList{Pos: p.pos(first), Items: items}, nil
}

// cond parses the inline `if cond, then, else`.
func (p *exprParser) cond(ifTok lexer.ExprToken) (ast.Expr, error) {
	const hint = "inline if has three parts: if condition, value_if_true, value_if_false"
	c, err := p.expr()
	if err != nil {
		return nil, err
	}
	if err := p.expect(diag.PA227, ",", "expected ',' after the if condition", hint); err != nil {
		return nil, err
	}
	then, err := p.expr()
	if err != nil {
		return nil, err
	}
	if p.classList && !p.isOp(",") {
		return &ast.Cond{Pos: p.pos(ifTok), Cond: c, Then: then}, nil
	}
	if err := p.expect(diag.PA227, ",", "expected ',' and an else value", hint); err != nil {
		return nil, err
	}
	els, err := p.expr()
	if err != nil {
		return nil, err
	}
	return &ast.Cond{Pos: p.pos(ifTok), Cond: c, Then: then, Else: els}, nil
}

// parseFor parses the contents of `:for={x <- list}`.
func parseFor(f *source.File, start, end int) (*ast.For, error) {
	const hint = "write :for={item <- list}"
	p, err := newExprParser(f, start, end)
	if err != nil {
		return nil, err
	}
	v := p.next()
	if v.Kind != lexer.XIdent || isKeyword(v.Val) {
		return nil, p.errorAt(diag.PA228, v, "expected a variable name in :for, found %s", describe(v)).WithFix(hint)
	}
	if err := p.expect(diag.PA228, "<-", "expected '<-' after the :for variable", hint); err != nil {
		return nil, err
	}
	list, err := p.expr()
	if err != nil {
		return nil, err
	}
	if err := p.expectEOF(); err != nil {
		return nil, err
	}
	return &ast.For{Pos: p.pos(v), Var: v.Val, VarPos: p.pos(v), List: list}, nil
}

// parseDecl parses a component signature: `Name(param Type, ...)`.
func parseDecl(f *source.File, start, end int) (*ast.Component, error) {
	const hint = "a declaration looks like: component TodoList(todos []Todo, filter string)"
	toks, err := lexer.LexSignature(f, start, end)
	if err != nil {
		return nil, err
	}
	p := &exprParser{f: f, toks: toks}
	name := p.next()
	if name.Kind != lexer.XIdent {
		return nil, p.errorAt(diag.PA229, name, "expected a component name, found %s", describe(name)).WithFix(hint)
	}
	if !unicode.IsUpper(rune(name.Val[0])) {
		return nil, p.errorAt(diag.PA230, name, "component names start with an uppercase letter").
			WithFix("rename %s to %s", name.Val, pascalCase(name.Val))
	}
	c := &ast.Component{Pos: p.pos(name), Name: name.Val}
	if err := p.expect(diag.PA229, "(", "expected '(' after the component name", hint); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for !p.isOp(")") {
		prm, err := p.param(hint)
		if err != nil {
			return nil, err
		}
		if seen[prm.Name] {
			return nil, diag.Errorf(diag.PA231, f, prm.Pos.Offset, prm.Pos.Offset+len(prm.Name), "parameter %s is declared twice", prm.Name).
				WithFix("rename or remove one of them")
		}
		seen[prm.Name] = true
		c.Params = append(c.Params, prm)
		if p.isOp(",") {
			p.next()
			continue
		}
		if !p.isOp(")") {
			return nil, p.errorAt(diag.PA229, p.peek(), "expected ',' or ')' after parameter %s, found %s", prm.Name, describe(p.peek())).WithFix(hint)
		}
	}
	p.next() // ')'
	return c, p.expectEOF()
}

// param parses one parameter: `name Type`, `name Type = default`,
// `name: :a | :b = :a` or `on:event event`.
func (p *exprParser) param(hint string) (*ast.Param, error) {
	pn := p.next()
	if pn.Kind != lexer.XIdent || isKeyword(pn.Val) {
		return nil, p.errorAt(diag.PA229, pn, "expected a parameter name, found %s", describe(pn)).WithFix("%s", hint)
	}
	prm := &ast.Param{Pos: p.pos(pn), Name: pn.Val}
	if pn.Val == "on" && p.isOp(":") {
		return p.eventParam(pn)
	}
	if p.isOp(",") || p.isOp(")") {
		return nil, p.errorAt(diag.PA232, pn, "parameter %s needs a type", pn.Val).
			WithFix("write the type after the name, e.g. %s string or %s []Item", pn.Val, pn.Val)
	}
	if p.isOp(":") && !p.atAtom() {
		colon := p.next()
		if !p.atAtom() {
			return nil, p.errorAt(diag.PA238, colon, "':' after a parameter name starts an enum, like %s: :a | :b", pn.Val).
				WithFix("write %s %s without the colon; only enums are written name: :value | :value", pn.Val, p.peek().Val)
		}
		enum := &ast.EnumType{Pos: p.pos(p.peek())}
		for {
			v, err := p.atom()
			if err != nil {
				return nil, err
			}
			for _, prev := range enum.Values {
				if prev == v.Name {
					return nil, diag.Errorf(diag.PA238, p.f, v.Pos.Offset, v.Pos.Offset+1+len(v.Name), "enum value :%s is listed twice", v.Name).
						WithFix("remove one of them")
				}
			}
			enum.Values = append(enum.Values, v.Name)
			if !p.isOp("|") {
				break
			}
			p.next()
		}
		prm.Type = enum
	} else {
		if p.atAtom() {
			return nil, p.errorAt(diag.PA238, p.peek(), "an enum parameter is written %s: :a | :b", pn.Val).
				WithFix("add a colon after the name: %s: ...", pn.Val)
		}
		typ, err := p.typ()
		if err != nil {
			return nil, err
		}
		if n, ok := typ.(*ast.NamedType); ok && n.Name == "event" {
			return nil, diag.Errorf(diag.PA238, p.f, prm.Pos.Offset, prm.Pos.Offset+len(pn.Val), "an event parameter is named after the DOM event, like on:click").
				WithFix("write on:click event (or on:change, on:submit, ...)")
		}
		prm.Type = typ
	}
	if p.isOp("=") {
		p.next()
		def, err := p.defaultValue(prm)
		if err != nil {
			return nil, err
		}
		prm.Default = def
	}
	if p.isOp("@") {
		previews, err := p.previews(prm)
		if err != nil {
			return nil, err
		}
		prm.Previews = previews
	}
	return prm, nil
}

// previews parses `@preview("a", "b")`: sample values for the preview
// gallery, after a parameter's type and default.
func (p *exprParser) previews(prm *ast.Param) ([]ast.Expr, error) {
	const fix = `write sample values after the type (and default): %s %s @preview("Save", "Delete")`
	at := p.next()
	name := p.next()
	if name.Kind != lexer.XIdent || name.Val != "preview" || name.Start != at.End {
		return nil, p.errorAt(diag.PA242, at, "expected @preview after parameter %s", prm.Name).
			WithFix(fix, prm.Name, ast.TypeString(prm.Type))
	}
	if err := p.expect(diag.PA242, "(", "expected '(' after @preview", fmt.Sprintf(fix, prm.Name, ast.TypeString(prm.Type))); err != nil {
		return nil, err
	}
	var xs []ast.Expr
	for {
		t := p.peek()
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		if !isLiteral(x) {
			return nil, diag.Errorf(diag.PA242, p.f, t.Start, p.toks[p.i-1].End, "@preview values must be literals").
				WithFix(`write strings, numbers or bools, e.g. @preview("Save", "Delete")`)
		}
		xs = append(xs, x)
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	if err := p.expect(diag.PA242, ")", "expected ',' or ')' in @preview", fmt.Sprintf(fix, prm.Name, ast.TypeString(prm.Type))); err != nil {
		return nil, err
	}
	if p.isOp("@") {
		return nil, p.errorAt(diag.PA242, p.peek(), "parameter %s has two @preview lists", prm.Name).
			WithFix("put every sample value in one @preview(...)")
	}
	return xs, nil
}

// isLiteral reports whether x is a literal: a string, number or bool,
// or a negative number.
func isLiteral(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
		return true
	case *ast.Unary:
		switch x.X.(type) {
		case *ast.IntLit, *ast.FloatLit:
			return x.Op == "-"
		}
	}
	return false
}

// eventParam parses `on:click event`, after the `on`.
func (p *exprParser) eventParam(on lexer.ExprToken) (*ast.Param, error) {
	p.next() // ':'
	ev := p.next()
	if ev.Kind != lexer.XIdent || ev.Start != on.End+1 {
		return nil, p.errorAt(diag.PA238, ev, "expected a DOM event name after on:, found %s", describe(ev)).
			WithFix("write on:click event (or on:change, on:submit, ...)")
	}
	name := "on:" + ev.Val
	t := p.next()
	if t.Kind != lexer.XIdent || t.Val != "event" {
		return nil, p.errorAt(diag.PA238, t, "%s is an event parameter, so its type is event", name).
			WithFix("write %s event", name)
	}
	if p.isOp("=") {
		return nil, p.errorAt(diag.PA238, p.peek(), "event parameters have no default").
			WithFix("remove the default; a call that leaves %s out has no handler", name)
	}
	return &ast.Param{Pos: p.pos(on), Name: name, Type: &ast.NamedType{Pos: p.pos(t), Name: "event"}}, nil
}

// atAtom reports whether an enum value, `:name`, comes next.
func (p *exprParser) atAtom() bool {
	t, n := p.peek(), p.toks[min(p.i+1, len(p.toks)-1)]
	return t.Kind == lexer.XOp && t.Val == ":" && n.Kind == lexer.XIdent && n.Start == t.End
}

func (p *exprParser) atom() (*ast.Atom, error) {
	if !p.atAtom() {
		return nil, p.errorAt(diag.PA238, p.peek(), "expected an enum value like :primary, found %s", describe(p.peek())).
			WithFix("enum values are lower-case names after a colon: :primary | :secondary")
	}
	colon := p.next()
	name := p.next()
	return &ast.Atom{Pos: p.pos(colon), Name: name.Val}, nil
}

// defaultValue parses a parameter's default: a literal of its type, or an
// enum value.
func (p *exprParser) defaultValue(prm *ast.Param) (ast.Expr, error) {
	if _, ok := prm.Type.(*ast.EnumType); ok {
		return p.atom()
	}
	t := p.peek()
	if p.atAtom() {
		return nil, p.errorAt(diag.PA238, t, "%s isn't an enum, so its default can't be an enum value", prm.Name).
			WithFix(`write a value of its type, e.g. "text", 0 or false`)
	}
	x, err := p.unary()
	if err != nil {
		return nil, err
	}
	switch x := x.(type) {
	case *ast.StringLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
		return x, nil
	case *ast.Unary:
		switch x.X.(type) {
		case *ast.IntLit, *ast.FloatLit:
			if x.Op == "-" {
				return x, nil
			}
		}
	}
	return nil, diag.Errorf(diag.PA238, p.f, t.Start, p.toks[p.i-1].End, "a default must be a literal value").
		WithFix(`write a string, number or bool, e.g. %s %s = ""; compute other values in Go`, prm.Name, ast.TypeString(prm.Type))
}

func (p *exprParser) typ() (ast.Type, error) {
	t := p.next()
	switch {
	case t.Kind == lexer.XOp && t.Val == "[":
		if err := p.expect(diag.PA233, "]", "expected ']' in list type", "a list type looks like []Todo"); err != nil {
			return nil, err
		}
		elem, err := p.typ()
		if err != nil {
			return nil, err
		}
		return &ast.ListType{Pos: p.pos(t), Elem: elem}, nil
	case t.Kind == lexer.XIdent:
		named := &ast.NamedType{Pos: p.pos(t), Name: t.Val}
		if p.isOp("[") {
			p.next()
			arg, err := p.typ()
			if err != nil {
				return nil, err
			}
			if err := p.expect(diag.PA233, "]", "expected ']' after the type argument", "a form type looks like form[SettleInput]"); err != nil {
				return nil, err
			}
			named.Arg = arg
		}
		return named, nil
	}
	return nil, p.errorAt(diag.PA233, t, "expected a type, found %s", describe(t)).
		WithFix("types are names like string, int, bool, Todo, lists like []Todo, or forms like form[SettleInput]")
}

func isKeyword(s string) bool { return s == "if" || s == "true" || s == "false" }

func describe(t lexer.ExprToken) string {
	switch t.Kind {
	case lexer.XEOF:
		return "the end of the expression"
	case lexer.XString:
		return "a string"
	case lexer.XInt, lexer.XFloat:
		return "number " + t.Val
	case lexer.XIdent:
		return "'" + t.Val + "'"
	}
	return "'" + t.Val + "'"
}

// parseBehavior parses a behavior declaration after the keyword:
// `name(params) emits (event, event(params)) requires [attr=value]`.
// emits and requires are optional, in that order.
func parseBehavior(f *source.File, start, end int) (*ast.Behavior, error) {
	const hint = "a behavior looks like: behavior dropdown(open bool) emits (select(value string), close) requires [role=listbox]"
	toks, err := lexer.LexSignature(f, start, end)
	if err != nil {
		return nil, err
	}
	// The requires list is CSS attribute selectors, read from the source
	// rather than as expression tokens.
	reqStart := -1
	for i, t := range toks {
		if t.Kind == lexer.XIdent && t.Val == "requires" {
			reqStart = t.End
			toks = append(toks[:i:i], lexer.ExprToken{Kind: lexer.XEOF, Start: t.Start, End: t.Start})
			break
		}
	}
	p := &exprParser{f: f, toks: toks}
	name := p.next()
	if name.Kind != lexer.XIdent || isKeyword(name.Val) {
		return nil, p.errorAt(diag.PA240, name, "expected a behavior name, found %s", describe(name)).WithFix(hint)
	}
	if !isSnake(name.Val) {
		return nil, p.errorAt(diag.PA240, name, "behavior names are snake_case, as in use:%s", snakeCase(name.Val)).
			WithFix("rename %s to %s", name.Val, snakeCase(name.Val))
	}
	b := &ast.Behavior{Pos: p.pos(name), Name: name.Val}
	if b.Params, err = p.behaviorParams(hint); err != nil {
		return nil, err
	}
	if t := p.peek(); t.Kind == lexer.XIdent && t.Val == "emits" {
		p.next()
		if err := p.expect(diag.PA240, "(", "expected '(' after emits", hint); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for !p.isOp(")") {
			en := p.next()
			if en.Kind != lexer.XIdent || isKeyword(en.Val) {
				return nil, p.errorAt(diag.PA240, en, "expected an event name, found %s", describe(en)).WithFix(hint)
			}
			if seen[en.Val] {
				return nil, p.errorAt(diag.PA240, en, "event %s is listed twice", en.Val).WithFix("remove one of them")
			}
			seen[en.Val] = true
			e := &ast.Emit{Pos: p.pos(en), Name: en.Val}
			if p.isOp("(") {
				if e.Params, err = p.behaviorParams(hint); err != nil {
					return nil, err
				}
			}
			b.Emits = append(b.Emits, e)
			if p.isOp(",") {
				p.next()
			} else if !p.isOp(")") {
				return nil, p.errorAt(diag.PA240, p.peek(), "expected ',' or ')' after event %s, found %s", e.Name, describe(p.peek())).WithFix(hint)
			}
		}
		p.next() // ')'
	}
	if t := p.peek(); t.Kind != lexer.XEOF {
		return nil, p.errorAt(diag.PA240, t, "unexpected %s in a behavior declaration", describe(t)).
			WithFix("after the parameters come emits (...) and then requires [...]")
	}
	if reqStart >= 0 {
		if b.Requires, err = parseRequires(f, reqStart, end); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// behaviorParams parses `(name Type, ...)`: like a component's, but no
// events or slots, which the checker reports with a fix.
func (p *exprParser) behaviorParams(hint string) ([]*ast.Param, error) {
	if err := p.expect(diag.PA240, "(", "expected '('", hint); err != nil {
		return nil, err
	}
	var params []*ast.Param
	seen := map[string]bool{}
	for !p.isOp(")") {
		if t := p.peek(); t.Kind == lexer.XIdent && t.Val == "on" {
			return nil, p.errorAt(diag.PA240, t, "a behavior sends events with emits, not event parameters").
				WithFix("list the events after the parameters: emits (select(value string), close)")
		}
		prm, err := p.param(hint)
		if err != nil {
			return nil, err
		}
		if seen[prm.Name] {
			return nil, diag.Errorf(diag.PA231, p.f, prm.Pos.Offset, prm.Pos.Offset+len(prm.Name), "parameter %s is declared twice", prm.Name).
				WithFix("rename or remove one of them")
		}
		if len(prm.Previews) > 0 {
			x := prm.Previews[0].Position()
			return nil, diag.Errorf(diag.PA242, p.f, x.Offset, x.Offset+1, "behavior parameters take no @preview").
				WithFix("give sample values to the parameters of the component that uses the behavior")
		}
		seen[prm.Name] = true
		params = append(params, prm)
		if p.isOp(",") {
			p.next()
		} else if !p.isOp(")") {
			return nil, p.errorAt(diag.PA240, p.peek(), "expected ',' or ')' after parameter %s, found %s", prm.Name, describe(p.peek())).WithFix("%s", hint)
		}
	}
	p.next() // ')'
	return params, nil
}

// parseRequires parses a requires list: `[role=listbox][aria-label]`,
// each an attribute the element must have, optionally with its value
// (quoted or not).
func parseRequires(f *source.File, start, end int) ([]*ast.Require, error) {
	const fix = `write attribute selectors, e.g. requires [role=listbox] or requires [role="dialog"][aria-label]`
	src := f.Text[:end]
	i := start
	var out []*ast.Require
	for {
		for i < end && (src[i] == ' ' || src[i] == '\t' || src[i] == '\r') {
			i++
		}
		if i == end {
			break
		}
		if src[i] != '[' {
			return nil, diag.Errorf(diag.PA240, f, i, i+1, "expected '[' in the requires list").WithFix(fix)
		}
		close := strings.IndexByte(src[i:end], ']')
		if close < 0 {
			return nil, diag.Errorf(diag.PA240, f, i, end, "'[' without ']' in the requires list").WithFix(fix)
		}
		inner := src[i+1 : i+close]
		r := &ast.Require{Pos: f.Pos(i)}
		name, value, hasValue := strings.Cut(inner, "=")
		r.Attr = strings.TrimSpace(name)
		if hasValue {
			value = strings.TrimSpace(value)
			if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
				value = value[1 : len(value)-1]
			}
			r.Value, r.HasValue = value, true
		}
		if !isAttrName(r.Attr) || strings.ContainsAny(r.Value, `"'[]`) {
			return nil, diag.Errorf(diag.PA240, f, i, i+close+1, "%s isn't an attribute selector", src[i:i+close+1]).WithFix(fix)
		}
		out = append(out, r)
		i += close + 1
	}
	if len(out) == 0 {
		return nil, diag.Errorf(diag.PA240, f, start, end, "requires needs at least one attribute").WithFix(fix)
	}
	return out, nil
}

func isAttrName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == ':') {
			return false
		}
	}
	return true
}

func isSnake(s string) bool {
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return s != "" && s[0] != '_'
}

// ParseUse parses the props of use:name={a: x, b: y}.
func ParseUse(f *source.File, start, end int) ([]*ast.Prop, error) {
	const fix = "write use:name={prop: value, ...}, e.g. use:dropdown={open: open, value: selected}"
	toks, err := lexer.LexSignature(f, start, end)
	if err != nil {
		return nil, err
	}
	p := &exprParser{f: f, toks: toks}
	var props []*ast.Prop
	seen := map[string]bool{}
	for p.peek().Kind != lexer.XEOF {
		n := p.next()
		if n.Kind != lexer.XIdent || isKeyword(n.Val) {
			return nil, p.errorAt(diag.PA241, n, "expected a prop name, found %s", describe(n)).WithFix(fix)
		}
		if seen[n.Val] {
			return nil, p.errorAt(diag.PA241, n, "prop %s is given twice", n.Val).WithFix("remove one of them")
		}
		seen[n.Val] = true
		if err := p.expect(diag.PA241, ":", "expected ':' after prop "+n.Val, fix); err != nil {
			return nil, err
		}
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		props = append(props, &ast.Prop{Pos: p.pos(n), Name: n.Val, X: x})
		if p.isOp(",") {
			p.next()
		} else if p.peek().Kind != lexer.XEOF {
			return nil, p.errorAt(diag.PA241, p.peek(), "expected ',' between props, found %s", describe(p.peek())).WithFix(fix)
		}
	}
	return props, nil
}
