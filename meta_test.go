package loom

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/render"
)

// metaPage sets its metadata in Mount; "same" sets it again unchanged
// and "late" changes it, which is too late.
type metaPage struct{}

func (p *metaPage) Mount(c *Ctx) {
	c.Title("Monty")
	c.Meta(metaOf("Monty on the Run"))
}

func metaOf(desc string) Meta {
	return Meta{Description: desc, Image: "/_assets/c64.png", Canonical: "/c64/monty",
		StructuredData: map[string]any{"@type": "MusicRecording", "name": "Monty </script> on the Run", "genre": []string{"Chiptune"}}}
}

func (p *metaPage) Assigns(c *Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "meta", Events: bound(p.Events())}
}

func (p *metaPage) Events() Events {
	return Events{
		"same": func(c *Ctx, _ Payload) { c.Meta(metaOf("Monty on the Run")) },
		"late": func(c *Ctx, _ Payload) { c.Meta(metaOf("changed")) },
		"ld": func(c *Ctx, _ Payload) {
			m := metaOf("Monty on the Run")
			m.StructuredData = map[string]any{"name": "x"}
			c.Meta(m)
		},
	}
}

func newMetaApp(t *testing.T, cfg Config) (*App, *httptest.Server) {
	cfg.Logger = quiet()
	app := New(cfg)
	app.Live("/meta", &metaPage{}, hxtest.Counter)
	g := app.Group(RootLayout(hxtest.Root, rootAssigns))
	g.Live("/plain", &titlePage{}, hxtest.Counter)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return app, srv
}

// The root layout's <.live_meta /> renders what Mount set, with paths
// made absolute: on the request's origin, or Config.BaseURL.
func TestMeta(t *testing.T) {
	_, srv := newMetaApp(t, Config{})
	body := get(t, srv, "/meta")
	for _, w := range []string{
		`<meta name="description" content="Monty on the Run" data-live-meta>`,
		`<meta property="og:title" content="Monty" data-live-meta>`,
		`<meta property="og:image" content="` + srv.URL + `/_assets/c64.png" data-live-meta>`,
		`<meta name="twitter:card" content="summary_large_image" data-live-meta>`,
		`<link rel="canonical" href="` + srv.URL + `/c64/monty" data-live-meta>`,
		// JSON-LD, escaped so a value can't close the script.
		`<script type="application/ld+json" data-live-meta>{"@type":"MusicRecording","genre":["Chiptune"],"name":"Monty \u003c/script\u003e on the Run"}</script>`,
	} {
		if !strings.Contains(body, w) {
			t.Errorf("lacks %s:\n%s", w, body)
		}
	}

	// Behind a proxy that ends TLS, the scheme is the browser's.
	app, _ := newMetaApp(t, Config{})
	r := httptest.NewRequest("GET", "http://example.com/meta", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	v, err := app.NewView(r, "test", true)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if got := v.headMeta().Canonical; got != "https://example.com/c64/monty" {
		t.Errorf("canonical behind a TLS proxy = %q", got)
	}

	_, srv = newMetaApp(t, Config{BaseURL: "https://example.com/"})
	if body := get(t, srv, "/meta"); !strings.Contains(body, `<link rel="canonical" href="https://example.com/c64/monty" data-live-meta>`) {
		t.Errorf("canonical not on BaseURL:\n%s", body)
	}
	// A page that sets only a title gets og:title and nothing else.
	if body := get(t, srv, "/plain"); !strings.Contains(body, `og:title`) || strings.Contains(body, "description") || strings.Contains(body, "canonical") {
		t.Errorf("want only og:title:\n%s", body)
	}
}

// Changing the metadata after the page loaded is RT449; setting the
// same again is not.
func TestMetaAfterLoad(t *testing.T) {
	app, _ := newMetaApp(t, Config{})
	v, err := app.NewView(httptest.NewRequest("GET", "/meta", nil), "test", true)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if _, err := v.Event("same", nil); err != nil {
		t.Fatal(err)
	}
	if f := v.Failures(); len(f) != 0 {
		t.Errorf("failures = %v, want none", f)
	}
	if _, err := v.Event("late", nil); err != nil {
		t.Fatal(err)
	}
	if f := v.Failures(); len(f) != 1 || !strings.Contains(f[0].Error(), RT449) {
		t.Errorf("failures = %v, want RT449", f)
	}
	if got := v.Meta().Description; got != "Monty on the Run" {
		t.Errorf("description = %q, want it unchanged", got)
	}
}

// A map in StructuredData doesn't break the comparison that finds a
// late change: setting the same again is fine, a different one is
// RT449.
func TestMetaStructuredDataAfterLoad(t *testing.T) {
	app, _ := newMetaApp(t, Config{})
	v, err := app.NewView(httptest.NewRequest("GET", "/meta", nil), "test", true)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if _, err := v.Event("same", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Event("ld", nil); err != nil {
		t.Fatal(err)
	}
	if f := v.Failures(); len(f) != 1 || !strings.Contains(f[0].Error(), RT449) {
		t.Errorf("failures = %v, want RT449", f)
	}
}

// StructuredData encoding/json can't marshal is RT450, and the page
// renders without it.
func TestMetaStructuredDataInvalid(t *testing.T) {
	c := &Ctx{}
	c.Meta(Meta{Description: "d", StructuredData: map[string]any{"f": func() {}}})
	if len(c.failed) != 1 || !strings.Contains(c.failed[0].Error(), RT450) {
		t.Errorf("failures = %v, want RT450", c.failed)
	}
	if c.ld != "" || c.meta.Description != "d" {
		t.Errorf("meta = %+v, ld = %q; want the rest kept and no JSON-LD", c.meta, c.ld)
	}
}

// NoIndex renders robots noindex.
func TestMetaNoIndex(t *testing.T) {
	s := &render.Scope{Meta: render.Meta{NoIndex: true}}
	if got := string(render.LiveMeta(s)); got != `<meta name="robots" content="noindex" data-live-meta>` {
		t.Errorf("LiveMeta = %s", got)
	}
}

// c.URL is an absolute URL within the page's mount.
func TestCtxURL(t *testing.T) {
	app, _ := newMetaApp(t, Config{BaseURL: "https://example.com"})
	c := &Ctx{app: app, Request: httptest.NewRequest("GET", "/", nil)}
	if got := c.URL("/composers/rob-hubbard"); got != "https://example.com/composers/rob-hubbard" {
		t.Errorf("c.URL = %q", got)
	}
}
