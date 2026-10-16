package diag

import (
	"encoding/json"
	"testing"

	"github.com/moomerman/loom/compiler/source"
)

func TestErrorFormat(t *testing.T) {
	f := source.NewFile("a.hx", "component A()\n\t<p>{x</p>\n")
	e := Errorf(LX110, f, 18, 19, "this '{' is never closed").WithFix("add '}'")
	want := "a.hx:2:5: error LX110: this '{' is never closed\n" +
		"  |\n" +
		"2 | \t<p>{x</p>\n" +
		"  | \t   ^\n" +
		"  = fix: add '}'\n"
	if got := e.Error(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestErrorSpanStopsAtLineEnd(t *testing.T) {
	f := source.NewFile("a.hx", "ab\ncd")
	e := Errorf(PA203, f, 0, 5, "m").WithFix("f")
	want := "a.hx:1:1: error PA203: m\n  |\n1 | ab\n  | ^^\n  = fix: f\n"
	if got := e.Error(); got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestDiagnosticJSON(t *testing.T) {
	f := source.NewFile("todo.hx", "component A()\n<p>é{}</p>")
	e := Errorf(PA219, f, 18, 21, "empty expression").WithFix("remove them")
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"code":"PA219","severity":"error","file":"todo.hx","line":2,"col":5,"message":"empty expression","fix":"remove them"}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}
