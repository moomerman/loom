// Package lexer splits .hx source into tokens.
//
// Lexing happens in two layers. Lex splits the markup: text, tags,
// attributes, `{...}` expressions and component declarations. The contents
// of an expression (or a declaration) are kept as a byte range of the
// source, which LexExpr later splits into expression tokens. Keeping the
// layers apart keeps each simple, and every token carries its byte offset,
// so errors always point at the right place in the original file.
package lexer

import (
	"strings"

	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/compiler/source"
)

// Kind is the kind of a markup token.
type Kind int

const (
	EOF          Kind = iota
	Text              // literal text, including HTML comments and doctypes
	Interp            // `{expr}` in text; Inner is the expression range
	Decl              // `component ...`, `behavior ...` or `import ...` line; Val is the keyword, Inner everything after it
	TagOpen           // `<name`; Val is the name, e.g. "li", ".footer", ":left"
	AttrName          // attribute name; Val is the name
	AttrString        // quoted attribute value; Val is the unquoted text
	AttrExpr          // `{expr}` attribute value; Inner is the expression range
	TagEnd            // `>` ending an open tag
	TagSelfClose      // `/>` ending an open tag
	TagClose          // `</name>`; Val is the name
)

var kindNames = [...]string{"EOF", "Text", "Interp", "Decl", "TagOpen", "AttrName", "AttrString", "AttrExpr", "TagEnd", "TagSelfClose", "TagClose"}

func (k Kind) String() string { return kindNames[k] }

// Token is a markup token. Start and End are byte offsets of the whole
// token in the source; InnerStart and InnerEnd delimit an expression's or
// declaration's contents.
type Token struct {
	Kind       Kind
	Val        string
	Start, End int
	InnerStart int
	InnerEnd   int
}

// rawTextTags hold raw text: their contents are not markup, so `{` and `<`
// inside them are literal.
var rawTextTags = map[string]bool{"script": true, "style": true}

// Lex splits a file into markup tokens. The last token is always EOF.
func Lex(f *source.File) ([]Token, error) {
	l := &markupLexer{f: f, src: f.Text}
	if err := l.run(); err != nil {
		return nil, err
	}
	l.emit(Token{Kind: EOF, Start: len(l.src), End: len(l.src)})
	return l.toks, nil
}

type markupLexer struct {
	f    *source.File
	src  string
	pos  int
	toks []Token
}

func (l *markupLexer) emit(t Token) {
	// Merge adjacent text, e.g. a literal "<" followed by more text.
	if t.Kind == Text && len(l.toks) > 0 {
		last := &l.toks[len(l.toks)-1]
		if last.Kind == Text && last.End == t.Start {
			last.Val += t.Val
			last.End = t.End
			return
		}
	}
	l.toks = append(l.toks, t)
}

func (l *markupLexer) errorf(code diag.Code, start, end int, format string, args ...any) *diag.Error {
	return diag.Errorf(code, l.f, start, end, format, args...)
}

func (l *markupLexer) run() error {
	for l.pos < len(l.src) {
		var err error
		rest := l.src[l.pos:]
		switch {
		case l.atDecl():
			l.lexDecl()
		case strings.HasPrefix(rest, "<%"):
			end := strings.Index(rest, "%>")
			if end < 0 {
				end = 2
			} else {
				end += 2
			}
			err = l.errorf(diag.LX116, l.pos, l.pos+end, "EEx tags like <%%= ... %%> aren't supported").
				WithFix("write {expression} for a value; use :if and :for attributes for conditions and loops")
		case strings.HasPrefix(rest, "<!--"):
			err = l.lexComment()
		case strings.HasPrefix(rest, "<!"):
			err = l.lexDoctype()
		case strings.HasPrefix(rest, "</"):
			err = l.lexCloseTag()
		case rest[0] == '<' && len(rest) > 1 && isTagStart(rest[1]):
			err = l.lexOpenTag()
		case rest[0] == '{':
			var t Token
			t, err = l.lexBraces(Interp)
			if err == nil {
				l.emit(t)
			}
		case rest[0] == '}':
			err = l.errorf(diag.LX101, l.pos, l.pos+1, "unexpected '}' in text").
				WithFix(`there is no matching '{'; to show a literal brace write {"}"}`)
		default:
			l.lexText()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// atDecl reports whether a declaration starts here: `component` or
// `import` at the very start of a line, followed by a space.
func (l *markupLexer) atDecl() bool {
	return l.declKeyword() != ""
}

func (l *markupLexer) declKeyword() string {
	if l.pos > 0 && l.src[l.pos-1] != '\n' {
		return ""
	}
	for _, kw := range []string{"component", "behavior", "import"} {
		if strings.HasPrefix(l.src[l.pos:], kw+" ") {
			return kw
		}
	}
	return ""
}

func (l *markupLexer) lexDecl() {
	kw := l.declKeyword()
	start := l.pos
	end := strings.IndexByte(l.src[start:], '\n')
	if end < 0 {
		end = len(l.src)
	} else {
		end += start
	}
	l.emit(Token{Kind: Decl, Val: kw, Start: start, End: end, InnerStart: start + len(kw), InnerEnd: end})
	l.pos = end
}

func (l *markupLexer) lexText() {
	start := l.pos
	l.pos++ // the first character is never special here
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '{' || c == '}' || c == '<' || l.atDecl() {
			break
		}
		l.pos++
	}
	l.emit(Token{Kind: Text, Val: l.src[start:l.pos], Start: start, End: l.pos})
}

func (l *markupLexer) lexComment() error {
	start := l.pos
	end := strings.Index(l.src[start:], "-->")
	if end < 0 {
		return l.errorf(diag.LX102, start, start+4, "HTML comment is never closed").WithFix("end the comment with -->")
	}
	l.pos = start + end + len("-->")
	l.emit(Token{Kind: Text, Val: l.src[start:l.pos], Start: start, End: l.pos})
	return nil
}

func (l *markupLexer) lexDoctype() error {
	start := l.pos
	end := strings.IndexByte(l.src[start:], '>')
	if end < 0 {
		return l.errorf(diag.LX103, start, start+2, "<! declaration is never closed").WithFix("end it with '>', e.g. <!DOCTYPE html>")
	}
	l.pos = start + end + 1
	l.emit(Token{Kind: Text, Val: l.src[start:l.pos], Start: start, End: l.pos})
	return nil
}

func (l *markupLexer) lexCloseTag() error {
	start := l.pos
	l.pos += len("</")
	name := l.readName()
	if name == "" {
		return l.errorf(diag.LX104, start, start+2, "expected a tag name after '</'").
			WithFix("a closing tag looks like </div>; to show a literal '<' write {\"<\"}")
	}
	l.skipSpace()
	if l.pos >= len(l.src) || l.src[l.pos] != '>' {
		return l.errorf(diag.LX104, start, l.pos, "closing tag </%s is missing its '>'", name).
			WithFix("end it with '>', e.g. </%s>", name)
	}
	l.pos++
	l.emit(Token{Kind: TagClose, Val: name, Start: start, End: l.pos})
	return nil
}

func (l *markupLexer) lexOpenTag() error {
	start := l.pos
	l.pos++ // '<'
	name := l.readName()
	l.emit(Token{Kind: TagOpen, Val: name, Start: start, End: l.pos})
	for {
		l.skipSpace()
		if l.pos >= len(l.src) {
			return l.errorf(diag.LX105, start, start+1+len(name), "tag <%s is missing its closing '>'", name).
				WithFix("end the tag with > or />")
		}
		c := l.src[l.pos]
		switch {
		case c == '>':
			l.pos++
			l.emit(Token{Kind: TagEnd, Start: l.pos - 1, End: l.pos})
			if rawTextTags[name] {
				return l.lexRawText(name)
			}
			return nil
		case strings.HasPrefix(l.src[l.pos:], "/>"):
			l.pos += 2
			l.emit(Token{Kind: TagSelfClose, Start: l.pos - 2, End: l.pos})
			return nil
		case isAttrStart(c):
			if err := l.lexAttr(name); err != nil {
				return err
			}
		case c == '<':
			return l.errorf(diag.LX105, start, start+1+len(name), "tag <%s is missing its closing '>'", name).
				WithFix("end the tag with > or /> before starting another tag")
		default:
			return l.errorf(diag.LX106, l.pos, l.pos+1, "unexpected %q inside tag <%s>", c, name).
				WithFix("attributes look like name=\"text\" or name={expression}")
		}
	}
}

func (l *markupLexer) lexAttr(tag string) error {
	start := l.pos
	name := l.readName()
	l.emit(Token{Kind: AttrName, Val: name, Start: start, End: l.pos})
	save := l.pos
	l.skipSpace()
	if l.pos >= len(l.src) || l.src[l.pos] != '=' {
		l.pos = save // a bare attribute like `disabled`
		return nil
	}
	l.pos++
	l.skipSpace()
	if l.pos >= len(l.src) {
		return l.errorf(diag.LX107, start, l.pos, "attribute %s has '=' but no value", name).
			WithFix(`give it a value, e.g. %s="text" or %s={expression}, or remove the '='`, name, name)
	}
	switch q := l.src[l.pos]; q {
	case '>', '/', ' ', '\t', '\n', '\r':
		return l.errorf(diag.LX107, start, l.pos, "attribute %s has '=' but no value", name).
			WithFix(`give it a value, e.g. %s="text" or %s={expression}, or remove the '='`, name, name)
	case '"', '\'':
		vstart := l.pos
		end := strings.IndexByte(l.src[vstart+1:], q)
		if end < 0 {
			return l.errorf(diag.LX108, vstart, vstart+1, "the value of %s is never closed", name).
				WithFix("add a closing %c", q)
		}
		l.pos = vstart + 1 + end + 1
		l.emit(Token{Kind: AttrString, Val: l.src[vstart+1 : l.pos-1], Start: vstart, End: l.pos})
		return nil
	case '{':
		t, err := l.lexBraces(AttrExpr)
		if err != nil {
			return err
		}
		l.emit(t)
		return nil
	default:
		return l.errorf(diag.LX109, l.pos, l.pos+1, "the value of %s must be quoted or in braces", name).
			WithFix(`write %s="text" for a fixed value, or %s={expression} for a dynamic one`, name, name)
	}
}

// lexBraces lexes a `{...}` expression, matching nested braces and
// skipping over string literals.
func (l *markupLexer) lexBraces(kind Kind) (Token, error) {
	start := l.pos
	depth := 0
	for i := start; i < len(l.src); i++ {
		switch l.src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				l.pos = i + 1
				return Token{Kind: kind, Start: start, End: l.pos, InnerStart: start + 1, InnerEnd: i}, nil
			}
		case '"':
			// Skip a string literal so braces inside it don't count. Strings
			// can't span lines; the expression lexer reports unclosed ones.
			for i++; i < len(l.src) && l.src[i] != '"' && l.src[i] != '\n'; i++ {
				if l.src[i] == '\\' {
					i++
				}
			}
		}
	}
	return Token{}, l.errorf(diag.LX110, start, start+1, "this '{' is never closed").
		WithFix("every expression needs a closing '}'")
}

func (l *markupLexer) lexRawText(tag string) error {
	start := l.pos
	end := indexFold(l.src[start:], "</"+tag)
	if end < 0 {
		return l.errorf(diag.LX111, start, start, "<%s> is never closed", tag).WithFix("add </%s> after its contents", tag)
	}
	l.pos = start + end
	if end > 0 {
		l.emit(Token{Kind: Text, Val: l.src[start:l.pos], Start: start, End: l.pos})
	}
	return nil
}

func (l *markupLexer) readName() string {
	start := l.pos
	for l.pos < len(l.src) && isNameChar(l.src[l.pos]) {
		l.pos++
	}
	return l.src[start:l.pos]
}

func (l *markupLexer) skipSpace() {
	for l.pos < len(l.src) && isSpace(l.src[l.pos]) {
		l.pos++
	}
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

// isTagStart reports whether c can follow '<' to start a tag: a letter for
// HTML, '.' for a component call, ':' for a named slot.
func isTagStart(c byte) bool { return isLetter(c) || c == '.' || c == ':' }

func isAttrStart(c byte) bool { return isLetter(c) || c == ':' || c == '_' || c == '@' }

func isNameChar(c byte) bool {
	return isLetter(c) || isDigit(c) || c == '-' || c == '_' || c == ':' || c == '.' || c == '@'
}

// indexFold is strings.Index, ignoring ASCII case.
func indexFold(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(substr)], substr) {
			return i
		}
	}
	return -1
}
