package loom

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestAssetDirectories serves a directory of assets under one hash, so
// the files in it load each other by relative URL.
func TestAssetDirectories(t *testing.T) {
	files := fstest.MapFS{
		"app.css":                {Data: []byte("body{}")},
		"player/player.js":       {Data: []byte(`import "./lib/engine.js";`)},
		"player/lib/engine.js":   {Data: []byte("export {};")},
		"player/lib/engine.wasm": {Data: []byte("\x00asm")},
	}
	app := New(Config{})
	g := app.Group(Assets(files))
	a := g.assets

	dir, ok := a.url("player/")
	if !ok || !strings.HasPrefix(dir, "/_assets/player.") || !strings.HasSuffix(dir, "/") {
		t.Fatalf(`url("player/") = %q, %v`, dir, ok)
	}
	lib, ok := a.url("player/lib/")
	if !ok || !strings.HasPrefix(lib, "/_assets/player/lib.") {
		t.Fatalf(`url("player/lib/") = %q, %v`, lib, ok)
	}
	if _, ok := a.url("nope/"); ok {
		t.Error(`url("nope/") found`)
	}
	if css, _ := a.url("app.css"); !strings.HasPrefix(css, "/_assets/app.") || !strings.HasSuffix(css, ".css") {
		t.Errorf(`url("app.css") = %q`, css)
	}

	srv := httptest.NewServer(app)
	defer srv.Close()
	fetch := func(p string) (int, string, http.Header) {
		res, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res.Header
	}
	for p, want := range map[string]string{
		dir + "player.js":       `import "./lib/engine.js";`,
		dir + "lib/engine.js":   "export {};",
		lib + "engine.js":       "export {};",
		dir + "lib/engine.wasm": "\x00asm",
	} {
		code, body, h := fetch(p)
		if code != 200 || body != want || !strings.Contains(h.Get("Cache-Control"), "immutable") {
			t.Errorf("GET %s = %d %q %v", p, code, body, h)
		}
	}
	if _, _, h := fetch(dir + "lib/engine.wasm"); h.Get("Content-Type") != "application/wasm" {
		t.Errorf("wasm Content-Type = %q", h.Get("Content-Type"))
	}
	for _, p := range []string{dir + "missing.js", "/_assets/player/player.js", "/_assets/player.00000000/player.js", dir + "../app.css"} {
		if code, _, _ := fetch(p); code != 404 {
			t.Errorf("GET %s = %d, want 404", p, code)
		}
	}

	// A change to any file under a directory gives it a new URL; a
	// directory beside it keeps its own.
	files["player/lib/engine.js"] = &fstest.MapFile{Data: []byte("export const v = 2;")}
	b := newAssets(files)
	if d, _ := b.url("player/"); d == dir {
		t.Errorf("changed directory kept its URL %q", d)
	}
	if l, _ := b.url("player/lib/"); l == lib {
		t.Errorf("changed subdirectory kept its URL %q", l)
	}
	files["other/x.js"] = &fstest.MapFile{Data: []byte("1")}
	c := newAssets(files)
	if d1, _ := b.url("player/"); func() string { d2, _ := c.url("player/"); return d2 }() != d1 {
		t.Error("adding another directory changed player/'s URL")
	}
}
