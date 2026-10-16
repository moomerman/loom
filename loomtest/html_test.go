package loomtest

import "testing"

func TestCount(t *testing.T) {
	const doc = `<!DOCTYPE html><ul class="todos"><li class="done big" data-loom-key="1"><input type="checkbox" checked><span>a &amp; b</span></li>` +
		`<li data-loom-key="2"><input type=checkbox><br/><span id="x">b</span></li></ul><script>if (a < b) {}</script><p class=done title="a b"></p>`
	for _, tc := range []struct {
		sel  string
		want int
	}{
		{"li", 2},
		{"li.done", 1},
		{".done", 2},
		{"li.done.big", 1},
		{"ul li span", 2},
		{"ul.todos .done input[checked]", 1},
		{"input[type=checkbox]", 2},
		{`input[type="checkbox"][checked]`, 1},
		{"[data-loom-key='2'] #x", 1},
		{"p li", 0},
		{"span", 2},
		{"script", 1},
		{`p[title="a b"]`, 1},
		{`ul  [data-loom-key="2"]   span`, 1},
	} {
		got, err := Count(doc, tc.sel)
		if err != nil || got != tc.want {
			t.Errorf("Count(%q) = %d, %v; want %d", tc.sel, got, err, tc.want)
		}
	}
	for _, sel := range []string{"", "ul > li", "li:first-child", "a,b", "[x"} {
		if _, err := Count(doc, sel); err == nil {
			t.Errorf("Count(%q): want an error", sel)
		}
	}
}
