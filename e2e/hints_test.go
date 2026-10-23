package e2e

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/render"
)

// toolbarPage handles hxtest.Toolbar's events slowly, so a test can see
// them pending.
type toolbarPage struct{ saved, removed int }

func (p *toolbarPage) Mount(c *loom.Ctx) {}

func (p *toolbarPage) Events() loom.Events {
	slow := func(n *int) func(*loom.Ctx, loom.Payload) {
		return func(c *loom.Ctx, _ loom.Payload) {
			time.Sleep(400 * time.Millisecond)
			*n++
		}
	}
	return loom.Events{"save": slow(&p.saved), "remove": slow(&p.removed), "tag": func(*loom.Ctx, loom.Payload) {}, "dismiss": func(*loom.Ctx, loom.Payload) {}}
}

func (p *toolbarPage) Assigns(c *loom.Ctx) hxtest.ToolbarAssigns {
	return hxtest.ToolbarAssigns{ID: "1", OnClick: render.Event{Name: "remove"}}
}

// While an event is pending its element has class loom-pending and its
// optimistic:* hints applied, including hints given to a component call;
// once the event's diff is in, the server's render is all that shows.
func TestOptimisticHints(t *testing.T) {
	app := loom.New()
	app.Live("/", &toolbarPage{}, hxtest.Toolbar)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	ctx := browser(t)
	logConsole(t, ctx)
	save, remove := `button.primary`, `button.danger`
	var during, after string
	state := `[` + quote(save) + `, ` + quote(remove) + `].map(s => { const b = document.querySelector(s); return b.className + (b.disabled ? " disabled" : ""); }).join(" | ")`
	err := chromedp.Do(ctx,
		chromedp.Navigate(srv.URL),
		waitFor("#loom-root.loom-connected "+save),
		click(save),
		click(remove),
		eval(state, &during),
		waitJS(`!document.querySelector(".loom-pending")`),
		eval(state, &after),
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := "primary md loom-pending disabled | danger sm loom-pending leaving gone"; during != want {
		t.Errorf("while pending: %q; want %q", during, want)
	}
	if want := "primary md | danger sm"; after != want {
		t.Errorf("after the acks: %q; want %q", after, want)
	}
}

// An event a rate limit refuses (429, RT436) gives <html> class
// loom-limited, for the page to say so, instead of failing silently.
func TestRateLimitedEvent(t *testing.T) {
	app := loom.New()
	app.Live("/", &toolbarPage{}, hxtest.Toolbar, loom.RateLimit(loom.Limit{Key: loom.ByIP, Max: 1, Per: time.Hour, On: []string{"save"}}))
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	ctx := browser(t)
	var limited bool
	err := chromedp.Do(ctx,
		chromedp.Navigate(srv.URL),
		waitFor("#loom-root.loom-connected button.primary"),
		click("button.primary"),
		waitJS(`!document.querySelector(".loom-pending")`),
		eval(`document.documentElement.classList.contains("loom-limited")`, &limited),
	)
	if err != nil || limited {
		t.Fatalf("after the first save: limited %v, %v", limited, err)
	}
	err = chromedp.Do(ctx,
		click("button.primary"),
		waitJS(`document.documentElement.classList.contains("loom-limited")`),
	)
	if err != nil {
		t.Fatal(err)
	}
}
