package loom

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/internal/hxtest/parts"
)

type sortablePage struct{}

func (*sortablePage) Mount(c *Ctx) {}

func (*sortablePage) Assigns(c *Ctx) hxtest.SortableAssigns {
	return hxtest.SortableAssigns{Cards: []hxtest.Card{{ID: 1, Title: "One"}}, Label: "Plan"}
}

func (*sortablePage) Events() Events {
	nothing := func(*Ctx, Payload) {}
	return Events{"move": nothing, "cancel": nothing, "pick": nothing}
}

var scriptRE = regexp.MustCompile(`<script type="module" src="(/_loom/js/[0-9a-f]+\.js)"></script>`)

func fetch(t *testing.T, url string) (string, *http.Response) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), res
}

// TestBehaviorScripts checks that a route whose components attach
// behaviors loads one module that imports the runtime and each behavior's
// module and registers them, and that routes without behaviors load none.
func TestBehaviorScripts(t *testing.T) {
	app, srv := newTestApp(t)
	app.dev = true
	app.Live("/sortable", &sortablePage{}, hxtest.Sortable)

	if body := get(t, srv, "/"); scriptRE.MatchString(body) {
		t.Errorf("/ has no behaviors but loads a module:\n%s", body)
	}
	body := get(t, srv, "/sortable")
	m := scriptRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("/sortable loads no module:\n%s", body)
	}
	entry, res := fetch(t, srv.URL+m[1])
	if got := res.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := res.Header.Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	for _, want := range []string{
		`import { define } from "/_loom/js/`,
		`"` + hxtest.SortableBehavior.ID + `": [b0, ["moved", "cancel"]],`,
		`"` + parts.AnnounceBehavior.ID + `": [b2, []],`,
		"}, true);",
	} {
		if !strings.Contains(entry, want) {
			t.Errorf("module lacks %s:\n%s", want, entry)
		}
	}
	imports := regexp.MustCompile(`from "(/_loom/js/[0-9a-f]+\.js)"`).FindAllStringSubmatch(entry, -1)
	if len(imports) != 5 {
		t.Fatalf("module imports %d modules, want the runtime and 4 behaviors:\n%s", len(imports), entry)
	}
	if js, _ := fetch(t, srv.URL+imports[1][1]); js != hxtest.SortableBehavior.JS {
		t.Errorf("sortable's module is served as:\n%s", js)
	}
	if js, _ := fetch(t, srv.URL+imports[0][1]); !strings.Contains(js, "export function define") {
		t.Errorf("runtime module is served as:\n%s", js)
	}
	if _, res := fetch(t, srv.URL+"/_loom/js/0000000000000000.js"); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown module: %s, want 404", res.Status)
	}
}
