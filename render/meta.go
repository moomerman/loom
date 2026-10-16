package render

import "strings"

// Meta is what a page says about itself to link previews and search
// engines: loom.Ctx.Meta sets it, and a root layout's <.live_meta />
// renders it. Image and Canonical are absolute URLs by the time they
// reach the render.
type Meta struct {
	Title       string // the page's title, without the root layout's prefix or suffix
	Description string
	Image       string
	Canonical   string
	// StructuredData is JSON-LD, already encoded by encoding/json, which
	// escapes <, > and &, so it is safe inside a <script>.
	StructuredData string
	NoIndex        bool // robots noindex
}

// LiveMeta renders a root layout's <.live_meta />: the description,
// Open Graph and Twitter card tags, the canonical link and JSON-LD for the page's
// Meta, each marked data-live-meta so live navigation can swap them for
// the next page's. A tag whose value is empty is left out.
func LiveMeta(s *Scope) Text {
	if s == nil {
		return Empty
	}
	m := s.Meta
	var b strings.Builder
	tag := func(attr, name, content string) {
		if content != "" {
			b.WriteString(`<meta ` + attr + `="` + name + `" content="` + string(Escape(content)) + `" data-live-meta>`)
		}
	}
	if m.NoIndex {
		tag("name", "robots", "noindex")
	}
	tag("name", "description", m.Description)
	tag("property", "og:title", m.Title)
	tag("property", "og:description", m.Description)
	tag("property", "og:image", m.Image)
	tag("property", "og:url", m.Canonical)
	if m.Title+m.Description+m.Image+m.Canonical != "" {
		card := "summary"
		if m.Image != "" {
			card = "summary_large_image"
		}
		tag("name", "twitter:card", card)
	}
	if m.Canonical != "" {
		b.WriteString(`<link rel="canonical" href="` + string(Escape(m.Canonical)) + `" data-live-meta>`)
	}
	if m.StructuredData != "" {
		b.WriteString(`<script type="application/ld+json" data-live-meta>` + m.StructuredData + `</script>`)
	}
	return Text(b.String())
}
