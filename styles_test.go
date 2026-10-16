package loom

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/moomerman/loom/internal/hxtest"
)

type panelsPage struct{}

func (*panelsPage) Mount(c *Ctx) {}

func (*panelsPage) Assigns(c *Ctx) hxtest.PanelListAssigns {
	return hxtest.PanelListAssigns{Titles: []string{"first", "second"}}
}

var stylesheetRE = regexp.MustCompile(`<link rel="stylesheet" href="(/_loom/css/[0-9a-f]+\.css)">`)

// stylesheet finds the stylesheet linked in body and returns its stylesheet's URL and CSS.
func stylesheet(t *testing.T, srvURL, body string) (string, string, *http.Response) {
	t.Helper()
	m := stylesheetRE.FindStringSubmatch(body)
	if m == nil {
		return "", "", nil
	}
	res, err := http.Get(srvURL + m[1])
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return m[1], string(b), res
}

// TestStylesheets checks that each route links one stylesheet with the
// tokens and the CSS of exactly the components it can render.
func TestStylesheets(t *testing.T) {
	app, srv := newTestApp(t) // "/": the default root layout, no styles
	g := app.Group(RootLayout(hxtest.Root, rootAssigns))
	g.Live("/panels", &panelsPage{}, hxtest.PanelList)
	g.Live("/badge", &titlePage{}, hxtest.Counter)

	if body := get(t, srv, "/"); stylesheetRE.MatchString(body) {
		t.Errorf("/ has no styles but links a stylesheet:\n%s", body)
	}

	panel, list, root := hxtest.Panel.Assets.CSS, hxtest.PanelList.Assets.CSS, hxtest.Root.Assets.CSS
	tokens := hxtest.Root.Assets.Tokens.CSS
	body := get(t, srv, "/panels")
	url, css, res := stylesheet(t, srv.URL, body)
	if url == "" {
		t.Fatalf("/panels links no stylesheet:\n%s", body)
	}
	if got := res.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := res.Header.Get("Content-Type"); got != "text/css; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if !strings.HasPrefix(css, tokens) || strings.Count(css, tokens) != 1 {
		t.Errorf("/panels stylesheet should start with the tokens, once:\n%s", css)
	}
	for _, want := range []string{root, list, panel} {
		if strings.Count(css, want) != 1 {
			t.Errorf("/panels stylesheet should have %q once:\n%s", want, css)
		}
	}
	if !strings.Contains(body, `<article class="panel featured l-`) {
		t.Errorf("/panels elements lack the scope class:\n%s", body)
	}

	badgeURL, css, _ := stylesheet(t, srv.URL, get(t, srv, "/badge"))
	if badgeURL == url || !strings.Contains(css, root) || strings.Contains(css, panel) || strings.Contains(css, list) {
		t.Errorf("/badge stylesheet should have the root layout's CSS only:\n%s", css)
	}

	res, err := http.Get(srv.URL + "/_loom/css/0000000000000000.css")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown stylesheet: %s, want 404", res.Status)
	}
}
