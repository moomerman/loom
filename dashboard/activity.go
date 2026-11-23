package dashboard

import (
	"net/url"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/trace"
)

// feedSize is how many events the activity feed shows.
const feedSize = 100

// ActivityPage is the app's event stream as it happens, newest first:
// pages loaded and clicked, jobs run, transactions committed, errors.
// It reads the same ring of recent events as `loom events`, filtered by
// a tab whose kind lives in the URL (/activity?kind=job), so the
// overview's cards can link straight to one. The dashboard's own
// events are left out.
type ActivityPage struct {
	app  *App
	kind string // "" for all, or page, job, data, error, other
}

// Tab is one of the feed's filters.
type Tab struct {
	Kind    string
	Label   string
	Href    string
	Count   int
	Current bool
}

// tabs are the feed's filters, in order.
var tabs = []struct{ kind, label string }{
	{"", "All"}, {"page", "Pages"}, {"job", "Jobs"}, {"data", "Data"}, {"error", "Errors"}, {"other", "Other"},
}

// Mount starts the refresh: twice as often as the other pages, since
// the feed is what moves.
func (p *ActivityPage) Mount(c *loom.Ctx) {
	section(c, "activity")
	c.Title("Activity")
	p.refresh(c)
}

func (p *ActivityPage) refresh(c *loom.Ctx) { c.SendAfter(p.app.cfg.Refresh/2, "tick", nil) }

// HandleParams reads the tab from the URL.
func (p *ActivityPage) HandleParams(c *loom.Ctx, query url.Values) { p.kind = query.Get("kind") }

// Events has the refresh's timer.
func (p *ActivityPage) Events() loom.Events {
	return loom.Events{"tick": func(c *loom.Ctx, _ loom.Payload) { p.refresh(c) }}
}

// Assigns reads the kept events, counts them by kind for the tabs, and
// keeps the newest of the current kind.
func (p *ActivityPage) Assigns(c *loom.Ctx) ActivityAssigns {
	now := time.Now()
	counts := map[string]int{}
	a := ActivityAssigns{Kind: p.kind}
	mine := p.app.ownSessions()
	events := p.app.app.Stream().Since(0, trace.Filter{})
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if p.app.own(e.String("route")) || mine[e.String("sid")] {
			continue
		}
		k := kind(e)
		counts[k]++
		counts[""]++
		if (p.kind == "" || p.kind == k) && len(a.Events) < feedSize {
			a.Events = append(a.Events, eventRow(e, now))
		}
	}
	for _, t := range tabs {
		href := c.Path("/activity")
		if t.kind != "" {
			href += "?kind=" + t.kind
		}
		a.Tabs = append(a.Tabs, Tab{Kind: t.kind, Label: t.label, Href: href, Count: counts[t.kind], Current: t.kind == p.kind})
	}
	a.Kept = counts[""]
	return a
}
