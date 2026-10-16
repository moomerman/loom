package loom

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestGallery checks the preview gallery lists components with their
// samples, serves each sample alone in either theme, and is dev only.
func TestGallery(t *testing.T) {
	app, srv := newTestApp(t)
	if _, res := fetch(t, srv.URL+"/_loom/preview"); res.StatusCode != http.StatusNotFound {
		t.Errorf("gallery outside dev mode: %s, want 404", res.Status)
	}
	app.dev = true

	index, _ := fetch(t, srv.URL+"/_loom/preview")
	for _, want := range []string{`<a href="?component=hxtest.Button">hxtest.Button</a> <span>24 variants</span>`, `hxtest.Board</a> <span>no previews: columns is [][]Card`} {
		if !strings.Contains(index, want) {
			t.Errorf("index lacks %s:\n%s", want, index)
		}
	}
	page, _ := fetch(t, srv.URL+"/_loom/preview?component=hxtest.Button")
	if n := strings.Count(page, "<iframe"); n != 48 {
		t.Errorf("hxtest.Button page has %d frames, want 24 variants x 2 themes", n)
	}
	if !strings.Contains(page, `src="/_loom/preview/hxtest.Button?theme=dark&amp;variant=variant%3Ddanger"`) {
		t.Errorf("no dark frame for variant=danger:\n%s", page)
	}

	one, _ := fetch(t, srv.URL+"/_loom/preview/hxtest.Button?variant=variant%3Ddanger&theme=dark")
	for _, want := range []string{`data-theme="dark"`, `class="danger md"`, ">Save</button>"} {
		if !strings.Contains(one, want) {
			t.Errorf("sample page lacks %s:\n%s", want, one)
		}
	}
	// A component using tokens with a theme is shown in it too, in light
	// mode, the theme set around the samples. Button uses no tokens.
	panel, _ := fetch(t, srv.URL+"/_loom/preview?component=hxtest.Panel")
	if n := strings.Count(panel, "<iframe"); n != 6 {
		t.Errorf("hxtest.Panel page has %d frames, want 2 variants x light, dark and brand", n)
	}
	brand, _ := fetch(t, srv.URL+"/_loom/preview/hxtest.Panel?theme=brand")
	for _, want := range []string{`<html lang="en" data-theme="light">`, `<section class="loom-sample" data-theme="brand">`, `[data-theme=brand]{`} {
		if !strings.Contains(brand, want) {
			t.Errorf("brand page lacks %s:\n%s", want, brand)
		}
	}
	if _, res := fetch(t, srv.URL+"/_loom/preview/hxtest.Button?theme=brand"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown theme: %s, want 400", res.Status)
	}
	all, _ := fetch(t, srv.URL+"/_loom/preview/hxtest.Button")
	if n := strings.Count(all, `class="loom-sample"`); n != 24 {
		t.Errorf("hxtest.Button page has %d samples, want 24", n)
	}
	if _, res := fetch(t, srv.URL+"/_loom/preview/hxtest.Button?variant=nope"); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown sample: %s", res.Status)
	}

	body, _ := fetch(t, srv.URL+"/_loom/dev/previews")
	var infos []PreviewInfo
	if err := json.Unmarshal([]byte(body), &infos); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, info := range infos {
		if info.Component == "hxtest.Button" {
			found = len(info.Variants) == 24 && info.Variants[0] == "default" && info.Variants[1] == "busy" &&
				strings.Join(info.Themes, ",") == "light,dark"
		}
		if info.Component == "hxtest.Panel" && strings.Join(info.Themes, ",") != "light,dark,brand" {
			t.Errorf("hxtest.Panel's themes are %q, want light, dark and brand", info.Themes)
		}
	}
	if !found {
		t.Errorf("previews JSON lacks hxtest.Button's 24 samples:\n%s", body)
	}
}
