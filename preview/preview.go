// Package preview renders components' preview samples (declared with the
// parameters' enum and bool values and @preview sample values) as
// standalone HTML pages, and screenshots them in headless Chrome. The
// dev gallery at /_loom/preview and `loom preview screenshot` are built
// on it (see docs/components.md).
//
// Previews are static: the page has the component's tokens and styles
// but no loom.js and no behaviors, so it looks the same every time.
package preview

import (
	"fmt"
	"html"
	"strings"

	"github.com/moomerman/loom/render"
)

// Modes are the color modes previews are shown in.
var Modes = []string{"light", "dark"}

// Themes returns what t's previews are shown in: light and dark, then
// each named theme of its design tokens, in light mode.
func Themes(t render.Template) []string {
	return append(append([]string(nil), Modes...), render.Themes(t)...)
}

// Options change how Page renders.
type Options struct {
	// Names shows each sample's name above it.
	Names bool
}

// pageCSS lays out the samples. Each sample is a containing block for
// fixed positioning (the transform), so a dialog's fixed backdrop covers
// its own sample rather than the page.
const pageCSS = `body{margin:0;padding:16px;background:Canvas;color:CanvasText;font-family:system-ui,sans-serif}
.loom-sample{transform:translate(0);margin-bottom:16px;display:flow-root}
.loom-sample:last-child{margin-bottom:0}
.loom-sample-name{margin:0 0 8px;font:12px ui-monospace,monospace;opacity:.7}
`

// sizeScript gives a sample whose content is all fixed-position (a
// dialog) a height, since such content takes no space of its own.
const sizeScript = `<script>for(const s of document.querySelectorAll(".loom-sample"))for(const e of s.querySelectorAll("*"))if(getComputedStyle(e).position==="fixed"){s.style.minHeight="20rem";break}</script>`

// Page renders samples of t, one after another, as an HTML document in
// theme: "light" or "dark", or a named theme, shown in light mode.
func Page(t render.Template, samples []render.Sample, theme string, o Options) string {
	var b strings.Builder
	mode, named := theme, ""
	if mode != "light" && mode != "dark" {
		mode, named = "light", fmt.Sprintf(" data-theme=%q", theme)
	}
	fmt.Fprintf(&b, "<!DOCTYPE html>\n<html lang=\"en\" data-theme=%q>\n<head>\n<meta charset=\"utf-8\">\n", mode)
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(Name(t)))
	b.WriteString("<style>\n" + pageCSS)
	b.WriteString(render.Stylesheet(t) + "</style>\n</head>\n<body>\n")
	for _, s := range samples {
		b.WriteString(`<section class="loom-sample"` + named + `>`)
		if o.Names {
			fmt.Fprintf(&b, `<p class="loom-sample-name">%s</p>`, html.EscapeString(s.Name))
		}
		b.WriteString(s.Render(nil).HTML())
		b.WriteString("</section>\n")
	}
	b.WriteString(sizeScript + "\n</body>\n</html>\n")
	return b.String()
}

// Name is a component's qualified name, e.g. "kit.Button".
func Name(t render.Template) string { return t.PackageName() + "." + t.ComponentName() }

// Find returns the sample of t called name, or an error listing them.
func Find(t render.Template, name string) (render.Sample, error) {
	samples, why := t.ComponentPreviews()
	var names []string
	for _, s := range samples {
		if s.Name == name {
			return s, nil
		}
		names = append(names, fmt.Sprintf("%q", s.Name))
	}
	if len(samples) == 0 {
		return render.Sample{}, fmt.Errorf("%s has no previews: %s", Name(t), why)
	}
	return render.Sample{}, fmt.Errorf("%s has no variant %q; its variants are %s", Name(t), name, strings.Join(names, ", "))
}
