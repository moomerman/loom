package e2e

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
)

// sortPage reorders cards when its sortable behavior emits moved, and
// drops the last card on cancel.
type sortPage struct{ cards []hxtest.Card }

func (p *sortPage) Mount(c *loom.Ctx) {
	p.cards = []hxtest.Card{{ID: 1, Title: "One"}, {ID: 2, Title: "Two"}, {ID: 3, Title: "Three"}}
}

func (p *sortPage) Assigns(c *loom.Ctx) hxtest.SortableAssigns {
	return hxtest.SortableAssigns{Cards: p.cards, Label: "Plan"}
}

func (p *sortPage) Events() loom.Events {
	return loom.Events{
		"move": func(c *loom.Ctx, payload loom.Payload) {
			id, _ := strconv.Atoi(payload["id"])
			to, _ := strconv.Atoi(payload["to"])
			i := slices.IndexFunc(p.cards, func(c hxtest.Card) bool { return c.ID == id })
			if i >= 0 && to >= 0 && to < len(p.cards) {
				card := p.cards[i]
				p.cards = slices.Insert(slices.Delete(p.cards, i, i+1), to, card)
			}
		},
		"cancel": func(c *loom.Ctx, _ loom.Payload) { p.cards = p.cards[:len(p.cards)-1] },
		"pick":   func(c *loom.Ctx, _ loom.Payload) {},
	}
}

// TestBehaviors checks behaviors' lifecycle in a browser: mount on load,
// emitted events reaching the page, keyed moves keeping each element's
// instance (no remount), update after every patch, and destroy when an
// element goes.
func TestBehaviors(t *testing.T) {
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Dev: loom.DevOn})
	app.Live("/", &sortPage{}, hxtest.Sortable)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	ctx := browser(t)

	key := func(k string) string {
		return `document.querySelector('li[data-loom-key="1"]').dispatchEvent(new KeyboardEvent("keydown", {key: "` + k + `", altKey: true, bubbles: true})); true`
	}
	const order = `[...document.querySelectorAll("li")].map(li => li.dataset.loomKey + ":" + li.dataset.mounted).join(" ")`
	var log, hint, live string
	err := chromedp.Do(ctx,
		chromedp.Navigate(srv.URL),
		waitFor(`#loom-root.loom-connected li[data-mounted="3"]`),
		eval(`document.querySelector("[role=note]").title`, &hint),
		eval(`document.querySelector("[role=status]").getAttribute("aria-live")`, &live),
		js(`window.one = document.querySelector('li[data-loom-key="1"]'); true`),
		js(key("ArrowDown")),
		waitJS(order+` === "2:2 1:1 3:3"`),
		js(key("ArrowDown")),
		waitJS(order+` === "2:2 3:3 1:1"`),
		waitJS(`document.querySelector('li[data-loom-key="1"]') === window.one`),
		js(key("Escape")),
		waitJS(order+` === "2:2 3:3"`),
		eval(`window.itemLog.join(", ")`, &log),
	)
	if err != nil {
		t.Fatal(err)
	}
	if hint != "Plan" || live != "polite" {
		t.Errorf("imported behaviors didn't mount: title %q, aria-live %q", hint, live)
	}
	if want := "mount 1, mount 2, mount 3, destroy 1"; log != want {
		t.Errorf("lifecycle: %s, want %s", log, want)
	}
}
