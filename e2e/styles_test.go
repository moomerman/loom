package e2e

import (
	"net/http/httptest"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// TestDarkMode checks that the todo app's tokens follow the system color
// scheme, and that data-theme on <html> overrides it either way.
func TestDarkMode(t *testing.T) {
	srv := httptest.NewServer(todoApp(t))
	t.Cleanup(srv.Close)
	ctx := browser(t)

	const bg = `getComputedStyle(document.body).backgroundColor`
	scheme := func(dark bool) chromedp.Action {
		v := "light"
		if dark {
			v = "dark"
		}
		return emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: v}})
	}
	var light, dark, forcedLight, forcedDark, nav string
	err := chromedp.Run(ctx,
		scheme(false),
		chromedp.Navigate(srv.URL),
		waitFor(".todoapp"),
		eval(bg, &light),
		eval(`getComputedStyle(document.querySelector(".topnav")).backgroundColor`, &nav),
		scheme(true),
		eval(bg, &dark),
		eval(`document.documentElement.dataset.theme = "light"; `+bg, &forcedLight),
		scheme(false),
		eval(`document.documentElement.dataset.theme = "dark"; `+bg, &forcedDark),
	)
	if err != nil {
		t.Fatal(err)
	}
	// color.page in examples/todo/templates/tokens.toml.
	if light != "rgb(245, 245, 245)" || dark != "rgb(18, 18, 18)" {
		t.Errorf("body background: light %s, dark %s; want rgb(245, 245, 245) and rgb(18, 18, 18)", light, dark)
	}
	if forcedLight != light || forcedDark != dark {
		t.Errorf("data-theme: light %s, dark %s; want %s and %s", forcedLight, forcedDark, light, dark)
	}
	if nav != "rgb(184, 63, 69)" { // color.accent: the app layout's scoped style
		t.Errorf("nav background %s, want rgb(184, 63, 69)", nav)
	}
}
