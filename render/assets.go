package render

import (
	"slices"
	"strings"
)

// Assets are what a component needs on the page besides its HTML: its
// scoped CSS (from its <style> block), its package's design tokens, the
// behaviors it attaches, and the components it calls, whose assets it
// needs too. Generated code sets them; the runtime gathers them per route
// into one stylesheet and one script.
type Assets struct {
	CSS       string      // the component's scoped CSS, or ""
	Tokens    *Sheet      // its package's design tokens, if its CSS uses them
	Uses      []Template  // the components it calls
	Behaviors []*Behavior // the behaviors its markup attaches with use:
}

// Sheet is CSS shared by many components: a package's design tokens,
// compiled from tokens.toml to custom properties.
type Sheet struct {
	CSS    string
	Themes []string // the named themes tokens.toml declares, for data-theme
}

// LoomScripts renders a root layout's <.loom_scripts />: loom.js,
// navigate.js and, if the page's components attach behaviors, the module
// that loads them.
func LoomScripts(s *Scope) Text {
	loom, nav := "/_loom/loom.js", "/_loom/navigate.js"
	if s != nil && s.LoomJS != "" {
		loom, nav = s.LoomJS, s.NavigateJS
	}
	t := Text(`<script src="` + string(Escape(loom)) + `" defer></script><script src="` + string(Escape(nav)) + `" defer></script>`)
	if s != nil && s.Script != "" {
		t += Text(`<script type="module" src="` + string(Escape(s.Script)) + `"></script>`)
	}
	return t
}

// LoomStyles renders a root layout's <.loom_styles />: a link to the
// page's stylesheet, or nothing if it has none.
func LoomStyles(s *Scope) Text {
	if s == nil || s.Stylesheet == "" {
		return Empty
	}
	return Text(`<link rel="stylesheet" href="` + string(Escape(s.Stylesheet)) + `">`)
}

// Stylesheet returns the CSS needed by pages that render the given
// components: the design tokens first, then each component's CSS once,
// in the order the components are reached, callers before callees. It
// returns "" if none of them has styles.
func Stylesheet(tmpls ...Template) string {
	seen := map[*Assets]bool{}
	sheets := map[string]bool{} // by content: packages sharing a tokens.toml have equal sheets
	var tokens, css []string
	var visit func(t Template)
	visit = func(t Template) {
		a := t.ComponentAssets()
		if a == nil || seen[a] {
			return
		}
		seen[a] = true
		if a.Tokens != nil && !sheets[a.Tokens.CSS] {
			sheets[a.Tokens.CSS] = true
			tokens = append(tokens, a.Tokens.CSS)
		}
		if a.CSS != "" {
			css = append(css, a.CSS)
		}
		for _, u := range a.Uses {
			visit(u)
		}
	}
	for _, t := range tmpls {
		if t != nil {
			visit(t)
		}
	}
	return strings.Join(append(tokens, css...), "")
}

// Themes returns the named themes (tokens.toml's [theme.NAME.GROUP])
// of the design tokens the given components use, in the order first
// reached.
func Themes(tmpls ...Template) []string {
	seen := map[*Assets]bool{}
	var out []string
	var visit func(t Template)
	visit = func(t Template) {
		a := t.ComponentAssets()
		if a == nil || seen[a] {
			return
		}
		seen[a] = true
		if a.Tokens != nil {
			for _, th := range a.Tokens.Themes {
				if !slices.Contains(out, th) {
					out = append(out, th)
				}
			}
		}
		for _, u := range a.Uses {
			visit(u)
		}
	}
	for _, t := range tmpls {
		if t != nil {
			visit(t)
		}
	}
	return out
}

// ScopedClass adds a component's scope class to a dynamic class attribute.
func ScopedClass(class Text, scope string) Text {
	if class == "" {
		return Text(scope)
	}
	return class + " " + Text(scope)
}
