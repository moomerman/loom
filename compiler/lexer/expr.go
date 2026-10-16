package lexer

import (
	"strconv"
	"strings"

	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/compiler/source"
)

// ExprKind is the kind of an expression token.
type ExprKind int

const (
	XEOF    ExprKind = iota
	XIdent           // name
	XInt             // 42
	XFloat           // 4.2
	XString          // "text"; Val is the decoded value
	XOp              // operator or punctuation; Val is the operator
)

// ExprToken is an expression token. Start and End are byte offsets in the
// whole source file.
type ExprToken struct {
	Kind       ExprKind
	Val        string
	Start, End int
}

// operators, longest first so that "<=" wins over "<".
var operators = []string{
	"<-", "==", "!=", "<=", ">=", "&&", "||",
	"+", "-", "*", "/", "%", "!", "<", ">", "(", ")", "[", "]", ",", ".", "=",
}

// LexExpr splits the source range [start, end) into expression tokens. The
// last token is always XEOF, positioned at end.
//
// The lexer accepts "=" so that the parser can explain that assignment is
// not allowed, rather than reporting an unknown character.
func LexExpr(f *source.File, start, end int) ([]ExprToken, error) {
	return lexExpr(f, start, end, false)
}

// LexSignature is LexExpr for a component signature, which also uses ':',
// '|' and '@': `variant: :primary | :secondary = :primary`,
// `on:click event`, `label string @preview("Save")`.
func LexSignature(f *source.File, start, end int) ([]ExprToken, error) {
	return lexExpr(f, start, end, true)
}

func lexExpr(f *source.File, start, end int, signature bool) ([]ExprToken, error) {
	var toks []ExprToken
	src := f.Text[:end]
	i := start
	for i < end {
		c := src[i]
		switch {
		case isSpace(c):
			i++
		case isLetter(c) || c == '_':
			j := i
			for j < end && (isLetter(src[j]) || isDigit(src[j]) || src[j] == '_') {
				j++
			}
			toks = append(toks, ExprToken{Kind: XIdent, Val: src[i:j], Start: i, End: j})
			i = j
		case isDigit(c):
			j := i
			for j < end && isDigit(src[j]) {
				j++
			}
			kind := XInt
			if j+1 < end && src[j] == '.' && isDigit(src[j+1]) {
				kind = XFloat
				for j++; j < end && isDigit(src[j]); j++ {
				}
			}
			toks = append(toks, ExprToken{Kind: kind, Val: src[i:j], Start: i, End: j})
			i = j
		case c == '"':
			t, err := lexString(f, src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, t)
			i = t.End
		case c == '\'':
			return nil, diag.Errorf(diag.LX112, f, i, i+1, "strings use double quotes").
				WithFix(`write "text" instead of 'text'`)
		case signature && (c == ':' || c == '@' || c == '|' && !strings.HasPrefix(src[i:], "||")):
			toks = append(toks, ExprToken{Kind: XOp, Val: string(c), Start: i, End: i + 1})
			i++
		default:
			op := matchOperator(src[i:])
			if op == "" {
				return nil, unexpectedChar(f, src, i)
			}
			toks = append(toks, ExprToken{Kind: XOp, Val: op, Start: i, End: i + len(op)})
			i += len(op)
		}
	}
	toks = append(toks, ExprToken{Kind: XEOF, Start: end, End: end})
	return toks, nil
}

func matchOperator(s string) string {
	for _, op := range operators {
		if strings.HasPrefix(s, op) {
			return op
		}
	}
	return ""
}

func unexpectedChar(f *source.File, src string, i int) error {
	if src[i] == '@' {
		j := i + 1
		for j < len(src) && (isLetter(src[j]) || isDigit(src[j]) || src[j] == '_') {
			j++
		}
		return diag.Errorf(diag.LX117, f, i, j, "assigns are referenced without '@'").
			WithFix("write %s instead of %s", src[i+1:j], src[i:j])
	}
	err := diag.Errorf(diag.LX113, f, i, i+1, "unexpected %q in expression", src[i])
	switch src[i] {
	case '&':
		err.WithFix("use && for logical and")
	case '|':
		err.WithFix("use || for logical or")
	case '{':
		err.WithFix("expressions can't contain braces; you are already inside {...}")
	case ';':
		err.WithFix("an expression is a single value; statements aren't allowed in templates")
	case ':':
		err.WithFix(`inline if is written: if cond, "yes", "no" (no do: or else:)`)
	default:
		err.WithFix("remove it; expressions use names, numbers, \"strings\", ( ), [ ], . , and the operators ! && || == != < <= > >= + - * / %%")
	}
	return err
}

func lexString(f *source.File, src string, start int) (ExprToken, error) {
	var b strings.Builder
	for i := start + 1; i < len(src); i++ {
		switch c := src[i]; c {
		case '"':
			return ExprToken{Kind: XString, Val: b.String(), Start: start, End: i + 1}, nil
		case '\n':
			return ExprToken{}, diag.Errorf(diag.LX114, f, start, i, "string is never closed").
				WithFix(`add a closing " before the end of the line`)
		case '\\':
			if i+1 >= len(src) {
				break
			}
			i++
			switch e := src[i]; e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '"', '\\':
				b.WriteByte(e)
			default:
				return ExprToken{}, diag.Errorf(diag.LX115, f, i-1, i+1, "unknown escape sequence %s", strconv.Quote(src[i-1:i+1])).
					WithFix(`supported escapes are \n, \t, \" and \\`)
			}
		default:
			b.WriteByte(c)
		}
	}
	return ExprToken{}, diag.Errorf(diag.LX114, f, start, start+1, "string is never closed").
		WithFix(`add a closing "`)
}
