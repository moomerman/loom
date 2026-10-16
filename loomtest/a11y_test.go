package loomtest

import (
	"strings"
	"testing"
)

func TestA11y(t *testing.T) {
	for _, tc := range []struct {
		name, html string
		want       []string // rule IDs, in order
	}{
		{"fine", `<html lang="en"><body>
			<img src="a.png" alt="A cat"><img src="b.png" alt="">
			<button>Save</button><button aria-label="Close">×</button><button><img src="x.png" alt="Delete"></button>
			<a href="/">Home</a>
			<label>Name <input name="n"></label><label for="e">Email</label><input id="e" type="email">
			<input type="hidden" name="h"><input type="submit" value="Go">
			<div role="dialog" aria-labelledby="t"><h2 id="t">Title</h2></div>
			<ul role="listbox" aria-label="Fruit"><li role="option" aria-selected="true">Pear</li></ul>
			<div role="tablist" aria-label="Show"><button role="tab" aria-selected="false">All</button></div>
			<button hidden></button><span aria-hidden="true"><a href="/x"></a></span>
		</body></html>`, nil},
		{"img", `<img src="a.png">`, []string{"image-alt"}},
		{"button", `<button><span aria-hidden="true">×</span></button><div role="button"></div>`, []string{"button-name", "button-name"}},
		{"link", `<a href="/"><img src="x.png" alt=""></a>`, []string{"link-name"}},
		{"label", `<input name="q"><select name="s"></select><textarea></textarea>`, []string{"label", "label", "label"}},
		{"dialog", `<div role="dialog"><p>Sure?</p></div>`, []string{"dialog-name"}},
		{"role", `<div role="buton">x</div>`, []string{"aria-roles"}},
		{"attr", `<div aria-lable="x" aria-expanded="yes">x</div>`, []string{"aria-valid-attr", "aria-valid-attr-value"}},
		{"reference", `<div aria-labelledby="nope">x</div><label for="gone">L</label>`, []string{"aria-reference", "aria-reference"}},
		{"parent", `<div><span role="option">x</span><span role="tab">y</span></div><ul><li role="listitem">z</li></ul><span role="option">top of a fragment</span>`, []string{"aria-required-parent", "aria-required-parent"}},
		{"lang", `<html><body></body></html>`, []string{"html-lang"}},
		{"ids", `<p id="a">1</p><p id="a">2</p><p id="a">3</p>`, []string{"duplicate-id"}},
	} {
		got, err := A11y(tc.html)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, p := range got {
			ids = append(ids, p.Rule)
		}
		if strings.Join(ids, " ") != strings.Join(tc.want, " ") {
			t.Errorf("%s: got %q, want %q:\n%v", tc.name, ids, tc.want, got)
		}
	}
}

func TestA11yExtraRule(t *testing.T) {
	rule := A11yRule{ID: "menu-items", Selector: "[role=menu]", Fix: "add items",
		Check: func(e *Element) string {
			if len(Select(e, "[role=menuitem]")) == 0 {
				return "menu has no items"
			}
			return ""
		}}
	got, err := A11y(`<div role="menu" aria-label="File"></div>`, rule)
	if err != nil || len(got) != 1 || got[0].String() != "menu-items: <div aria-label=\"File\" role=\"menu\">: menu has no items\n    fix: add items" {
		t.Errorf("got %v, %v", got, err)
	}
}
