package lexer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/moomerman/loom/compiler/source"
)

func dumpTokens(f *source.File, toks []Token) string {
	var b strings.Builder
	for _, t := range toks {
		fmt.Fprintf(&b, "%s %q @%s", t.Kind, t.Val, f.Pos(t.Start))
		if t.Kind == Interp || t.Kind == AttrExpr || t.Kind == Decl {
			fmt.Fprintf(&b, " inner=%q", f.Text[t.InnerStart:t.InnerEnd])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestLex(t *testing.T) {
	f := source.NewFile("t.hx", "component A(x int)\n<p class=\"a\" :if={x > 1} hidden>{x}</p>\n<script>{</script>")
	toks, err := Lex(f)
	if err != nil {
		t.Fatal(err)
	}
	want := `Decl "component" @1:1 inner=" A(x int)"
Text "\n" @1:19
TagOpen "p" @2:1
AttrName "class" @2:4
AttrString "a" @2:10
AttrName ":if" @2:14
AttrExpr "" @2:18 inner="x > 1"
AttrName "hidden" @2:26
TagEnd "" @2:32
Interp "" @2:33 inner="x"
TagClose "p" @2:36
Text "\n" @2:40
TagOpen "script" @3:1
TagEnd "" @3:8
Text "{" @3:9
TagClose "script" @3:10
EOF "" @3:19
`
	if got := dumpTokens(f, toks); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestLexExpr(t *testing.T) {
	src := `a.b[0] <= 1.5 && !f("x\"y", -2) || c <- d`
	f := source.NewFile("t.hx", src)
	toks, err := LexExpr(f, 0, len(src))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, t := range toks {
		got = append(got, t.Val)
	}
	want := []string{"a", ".", "b", "[", "0", "]", "<=", "1.5", "&&", "!", "f", "(", `x"y`, ",", "-", "2", ")", "||", "c", "<-", "d", ""}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if toks[7].Kind != XFloat || toks[12].Kind != XString || toks[12].Start != 20 {
		t.Errorf("unexpected kinds or offsets: %+v %+v", toks[7], toks[12])
	}
}
