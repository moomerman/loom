package loom

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/moomerman/loom/preview"
	"github.com/moomerman/loom/render"
)

// The preview gallery, /_loom/preview in dev mode, shows every variant
// of every component the app links, in light and dark, each in its own
// frame so fixed-position content (a dialog) stays inside it. Variants
// come from the parameters' enums and bools and @preview sample values
// (see docs/components.md). `loom preview` reads the same list as JSON from
// /_loom/dev/previews.

// PreviewInfo describes a component's previews.
type PreviewInfo struct {
	Component string   `json:"component"` // e.g. kit.Button
	Variants  []string `json:"variants"`  // its preview samples' names; the first is "default"
	Themes    []string `json:"themes"`    // light, dark, then its tokens' named themes
	NoPreview string   `json:"no_preview,omitempty"`
}

// Previews lists the preview variants of every registered component
// except root layouts, sorted by name.
func Previews() []PreviewInfo {
	out := []PreviewInfo{}
	for _, t := range previewable() {
		samples, why := t.ComponentPreviews()
		info := PreviewInfo{Component: preview.Name(t), Variants: []string{}, Themes: preview.Themes(t), NoPreview: why}
		for _, s := range samples {
			info.Variants = append(info.Variants, s.Name)
		}
		out = append(out, info)
	}
	return out
}

func previewable() []render.Template {
	var ts []render.Template
	for _, t := range render.Components() {
		if !t.IsRootLayout() {
			ts = append(ts, t)
		}
	}
	sort.Slice(ts, func(i, j int) bool { return preview.Name(ts[i]) < preview.Name(ts[j]) })
	return ts
}

// serveGallery serves the gallery's index, or with ?component= one
// component's variants.
func (a *App) serveGallery(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString(galleryHead)
	name := r.URL.Query().Get("component")
	if name == "" {
		b.WriteString("<h1>Previews</h1>\n<ul class=\"index\">\n")
		for _, info := range Previews() {
			note := fmt.Sprintf("%d variants", len(info.Variants))
			if info.NoPreview != "" {
				note = "no previews: " + info.NoPreview
			}
			fmt.Fprintf(&b, "<li><a href=\"?component=%s\">%s</a> <span>%s</span></li>\n",
				url.QueryEscape(info.Component), html.EscapeString(info.Component), html.EscapeString(note))
		}
		b.WriteString("</ul>\n")
	} else {
		t, err := render.Lookup(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		samples, why := t.ComponentPreviews()
		fmt.Fprintf(&b, "<p><a href=\"?\">All components</a></p>\n<h1>%s</h1>\n", html.EscapeString(preview.Name(t)))
		if len(samples) == 0 {
			fmt.Fprintf(&b, "<p>No previews: %s.</p>\n", html.EscapeString(why))
		}
		for _, s := range samples {
			fmt.Fprintf(&b, "<section>\n<h2>%s</h2>\n<div class=\"themes\">\n", html.EscapeString(s.Name))
			for _, theme := range preview.Themes(t) {
				q := url.Values{"variant": {s.Name}, "theme": {theme}}
				fmt.Fprintf(&b, "<iframe title=\"%s, %s\" src=\"/_loom/preview/%s?%s\" loading=\"lazy\" onload=\"this.style.height=this.contentDocument.documentElement.scrollHeight+'px'\"></iframe>\n",
					html.EscapeString(s.Name), theme, url.PathEscape(preview.Name(t)), html.EscapeString(q.Encode()))
			}
			b.WriteString("</div>\n</section>\n")
		}
	}
	b.WriteString("</body>\n</html>\n")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(b.String()))
}

// servePreview serves one component's preview variants as a standalone
// page: ?variant= picks one (default: all, with their names) and ?theme=
// the theme: light (the default), dark or one of its tokens' themes.
func (a *App) servePreview(w http.ResponseWriter, r *http.Request) {
	t, err := render.Lookup(r.PathValue("component"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	theme := r.URL.Query().Get("theme")
	if theme == "" {
		theme = "light"
	}
	if themes := preview.Themes(t); !slices.Contains(themes, theme) {
		http.Error(w, "theme is one of "+strings.Join(themes, ", "), http.StatusBadRequest)
		return
	}
	samples, why := t.ComponentPreviews()
	names := true
	if name := r.URL.Query().Get("variant"); name != "" {
		s, err := preview.Find(t, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		samples, names = []render.Sample{s}, false
	} else if len(samples) == 0 {
		http.Error(w, preview.Name(t)+" has no previews: "+why, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(preview.Page(t, samples, theme, preview.Options{Names: names})))
}

const galleryHead = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Loom previews</title>
<style>
:root{color-scheme:light dark}
body{margin:0 auto;padding:24px;max-width:1200px;font:15px/1.5 system-ui,sans-serif;background:Canvas;color:CanvasText}
a{color:LinkText}
h1{font-size:22px}
h2{font:13px ui-monospace,monospace;margin:24px 0 8px}
.index{list-style:none;padding:0}
.index li{padding:4px 0}
.index span{opacity:.7;font-size:13px;margin-left:8px}
.themes{display:grid;grid-template-columns:1fr 1fr;gap:12px}
iframe{width:100%;height:80px;border:1px solid GrayText;border-radius:6px}
</style>
</head>
<body>
`
