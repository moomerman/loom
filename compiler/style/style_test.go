package style

import (
	"strings"
	"testing"

	"github.com/moomerman/loom/compiler/diag"
	"github.com/moomerman/loom/compiler/source"
)

const testTokens = `
[color]
accent    = { light = "#4f46e5", dark = "#a5b4fc" }
on-accent = { light = "#ffffff", dark = "#1e1b4b" }

[space]
2 = "0.5rem"

[radius]
md = "6px"
`

func tokens(t *testing.T, text string) *Tokens {
	t.Helper()
	tok, errs := ParseTokens(source.NewFile("tokens.toml", text))
	if errs.HasErrors() {
		t.Fatal(errs)
	}
	return tok
}

func compile(t *testing.T, css string, opt Options) (string, diag.List) {
	t.Helper()
	if opt.Class == "" {
		opt.Class = "l-abc123"
	}
	if opt.Tokens == nil {
		opt.Tokens = tokens(t, testTokens)
	}
	f := source.NewFile("a.hx", css)
	return Compile(f, 0, len(css), opt)
}

// TestScoping checks that every compound selector requires the scope
// class, at its end but before any pseudo-element, and that :global is kept as written.
func TestScoping(t *testing.T) {
	cases := []struct{ in, want string }{
		{`p { margin: 0 }`, `p.l-abc123{margin:0;}`},
		{`.a .b, h1 > span { margin: 0 }`, `.a.l-abc123 .b.l-abc123,h1.l-abc123>span.l-abc123{margin:0;}`},
		{`a:hover::before { content: "{ x }" }`, `a:hover.l-abc123::before{content:"{ x }";}`},
		{`/* a { } */ li + li { margin: 0 }`, `li.l-abc123+li.l-abc123{margin:0;}`},
		{`input[type="checkbox"]:checked { margin: 0 }`, `input[type="checkbox"]:checked.l-abc123{margin:0;}`},
		{`:not(.x) { margin: 0 }`, `:not(.x).l-abc123{margin:0;}`},
		{`@media (min-width: 40rem) { p { padding: token(space.2) } }`, "@media (min-width: 40rem){\np.l-abc123{padding:var(--space-2);}\n}"},
		{`@keyframes pop { from { opacity: 0 } } p { animation: pop 1s }`, "@keyframes pop-l-abc123{\nfrom{opacity:0;}\n}\np.l-abc123{animation:pop-l-abc123 1s;}"},
	}
	for _, c := range cases {
		got, errs := compile(t, c.in, Options{})
		if len(errs) > 0 {
			t.Errorf("%s: %v", c.in, errs)
			continue
		}
		if strings.TrimSpace(got) != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.in, strings.TrimSpace(got), c.want)
		}
	}
}

func TestGlobal(t *testing.T) {
	got, errs := compile(t, `:global(body) { margin: 0 } :global(a) span { margin: 0 }`, Options{Global: true})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	want := "body{margin:0;}\na span.l-abc123{margin:0;}"
	if strings.TrimSpace(got) != want {
		t.Errorf("got %q, want %q", strings.TrimSpace(got), want)
	}
	if _, errs := compile(t, `:global(body) { margin: 0 }`, Options{}); len(errs) != 1 || errs[0].Code != diag.ST709 {
		t.Errorf(":global outside a root layout: got %v, want ST709", errs)
	}
}

func TestValues(t *testing.T) {
	cases := []struct {
		css  string
		code diag.Code // "" if it compiles
	}{
		{`p { color: token(color.accent) }`, ""},
		{`p { color: #fff }`, diag.ST704},
		{`p { background: red }`, diag.ST704},
		{`p { border: 1px solid rgb(0 0 0) }`, diag.ST704},
		{`p { border: 1px solid token(color.accent) }`, ""},
		{`p { padding: 1rem }`, diag.ST704},
		{`p { padding: 0 auto; width: 50%; display: grid }`, ""},
		{`p { border-radius: 4px }`, diag.ST704},
		{`p { box-shadow: 0 1px 2px #0003 }`, diag.ST704},
		{`p { color: var(--color-accent) }`, diag.ST704},
		{`p { color: token(color.acent) }`, diag.ST703},
		{`p { padding: token(color.accent) }`, diag.ST710},
		{`p { color: inherit; background: none; border-color: transparent }`, ""},
		{`p { a { color: red } }`, diag.ST708},
		{`@import "x.css";`, diag.ST708},
	}
	for _, c := range cases {
		_, errs := compile(t, c.css, Options{})
		switch {
		case c.code == "" && len(errs) > 0:
			t.Errorf("%s: unexpected %v", c.css, errs)
		case c.code != "" && (len(errs) == 0 || errs[0].Code != c.code):
			t.Errorf("%s: got %v, want %s", c.css, errs, c.code)
		}
	}
	if _, errs := compile(t, `p { color: #fff; padding: 3px }`, Options{Unsafe: true}); len(errs) > 0 {
		t.Errorf("unsafe: unexpected %v", errs)
	}
}

func TestTokensCSS(t *testing.T) {
	css := tokens(t, testTokens).CSS()
	for _, want := range []string{
		`:root{color-scheme:light dark;`,
		`--color-accent:#4f46e5;`,
		`--space-2:0.5rem;`,
		`@media (prefers-color-scheme:dark){:root:not([data-theme=light]){`,
		`--color-accent:#a5b4fc;`,
		`:root[data-theme=dark]{color-scheme:dark;`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("tokens CSS lacks %q:\n%s", want, css)
		}
	}
}

// A reusable package's tokens are defaults: :where() gives them no
// specificity, so the host app's :root tokens win on a shared page.
func TestTokensDefaultsCSS(t *testing.T) {
	css := tokens(t, testTokens).DefaultsCSS()
	for _, want := range []string{
		`:where(:root){color-scheme:light dark;`,
		`@media (prefers-color-scheme:dark){:where(:root:not([data-theme=light])){`,
		`:where(:root[data-theme=dark]){color-scheme:dark;`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("defaults CSS lacks %q:\n%s", want, css)
		}
	}
}

// A theme's tokens apply under its data-theme, its dark values with the
// document's mode.
func TestThemesCSS(t *testing.T) {
	toks, errs := ParseTokens(source.NewFile("tokens.toml", `
[color]
page = { light = "#ffffff", dark = "#000000" }
on-page = { light = "#000000", dark = "#ffffff" }
[font]
body = "serif"
[theme.amiga.color]
page = { light = "#0055aa", dark = "#002255" }
on-page = "#ffffff"
[theme.amiga.font]
body = "Topaz, monospace"
`))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if got := toks.ThemeNames(); len(got) != 1 || got[0] != "amiga" {
		t.Errorf("themes = %v", got)
	}
	css := toks.CSS()
	for _, want := range []string{
		`[data-theme=amiga]{--color-page:#0055aa;--color-on-page:#ffffff;--font-body:Topaz, monospace;}`,
		`@media (prefers-color-scheme:dark){:root:not([data-theme=light]) [data-theme=amiga]{--color-page:#002255;}}`,
		`:root[data-theme=dark] [data-theme=amiga]{--color-page:#002255;}`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("tokens CSS lacks %q:\n%s", want, css)
		}
	}
}

func TestContrast(t *testing.T) {
	_, errs := ParseTokens(source.NewFile("tokens.toml", `
[color]
accent    = "#777777"
on-accent = "#888888"
`))
	if len(errs) != 1 || errs[0].Code != diag.ST706 || !errs[0].Warning || !strings.Contains(errs[0].Msg, "1.") {
		t.Fatalf("got %v, want one ST706 warning with the ratio", errs)
	}
	if r := ContrastRatio("#000", "#fff"); r < 20.9 || r > 21.1 {
		t.Errorf("black on white: %v, want 21", r)
	}
}
