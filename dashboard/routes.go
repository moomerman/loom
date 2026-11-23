package dashboard

import (
	"fmt"
	"sort"
	"strings"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/trace"
)

// RoutesPage lists every URL the app answers: its own pages and plain
// handlers first, then each mounted sub-app's, with who may open each
// one and how often it was opened. A route without parameters is a
// link; one with parameters links to a page open on it now, if any, so
// "what was the URL of the domain report again?" has an answer. The
// routes themselves are read once, on Mount: they can't change while
// the app runs. The search box filters them as you type.
type RoutesPage struct {
	app      *App
	sections []RouteSection
	filter   string
}

// RouteSection is the app's own routes, or one sub-app's.
type RouteSection struct {
	Title  string // "Your app", or the sub-app, e.g. jobsweb
	Prefix string // e.g. /admin/jobs; "" for the app's own
	Type   string // the sub-app's Go type, e.g. *jobsweb.App
	Access string // who may open the sub-app: its auth checks
	Routes []RouteEntry
}

// RouteEntry is one route.
type RouteEntry struct {
	Method    string // LIVE for a live page, or the handler's method
	Path      string // the full path, e.g. /domains/{id}
	Href      string // a link to open it; "" if there is none
	Example   bool   // Href is a page open on the route now, filling its parameters
	Component string // the template it renders, for a live page
	Access    string // "public", "signed in" or "restricted"
	Checks    string // the auth checks, for the title
	Watch     string // tables that re-render it
	Limits    string // rate limits
	DevOnly   bool
	Views     int // page loads since the app started
	Search    string
}

// Mount reads the routes and starts the refresh, which keeps the view
// counts and the open pages' links current.
func (p *RoutesPage) Mount(c *loom.Ctx) {
	section(c, "routes")
	c.Title("Routes")
	p.sections = p.read()
	p.app.tick(c)
}

// Events filters the list, and has the refresh's timer.
func (p *RoutesPage) Events() loom.Events {
	return loom.Events{
		"filter": func(c *loom.Ctx, payload loom.Payload) {
			p.filter = strings.ToLower(strings.TrimSpace(payload["value"]))
		},
		"tick": func(c *loom.Ctx, _ loom.Payload) { p.app.tick(c) },
	}
}

// read groups the app's live routes and handlers by mount.
func (p *RoutesPage) read() []RouteSection {
	app := &RouteSection{Title: "Your app"}
	mounts := map[string]*RouteSection{}
	var order []*RouteSection
	m := p.app.app.Manifest()
	for _, mt := range m.Mounts {
		s := &RouteSection{Title: mountName(mt.Type), Prefix: mt.Prefix, Type: mt.Type, Access: access(mt.Auth)}
		mounts[mt.Prefix] = s
		order = append(order, s)
		for _, r := range mt.Routes {
			if method, path, ok := strings.Cut(r, " "); ok { // a handler, e.g. "POST /sign-in"
				s.Routes = append(s.Routes, handlerEntry(method, mt.Prefix+path, mt.Auth, nil, mt.DevOnly))
			}
		}
	}
	for _, rt := range p.app.app.Routes() {
		e := RouteEntry{
			Method: "LIVE", Path: rt.Path, Component: rt.Component, Access: access(rt.Auth), Checks: strings.Join(rt.Auth, ", "),
			Watch: strings.Join(rt.Watch, ", "), Limits: strings.Join(rt.RateLimit, "; "),
		}
		if !strings.Contains(rt.Path, "{") {
			e.Href = rt.Path
		}
		e.Search = strings.ToLower(e.Path + " " + e.Component)
		if s := mounts[rt.Prefix]; rt.Prefix != "" && s != nil {
			for _, mt := range m.Mounts {
				if mt.Prefix == rt.Prefix {
					e.DevOnly = mt.DevOnly
				}
			}
			s.Routes = append(s.Routes, e)
			continue
		}
		app.Routes = append(app.Routes, e)
	}
	devOnly := map[string]bool{}
	for _, r := range m.Routes {
		devOnly[r.Path] = r.DevOnly
	}
	for i := range app.Routes {
		app.Routes[i].DevOnly = devOnly[app.Routes[i].Path]
	}
	for _, h := range m.Handlers {
		method, path, _ := strings.Cut(h.Pattern, " ")
		app.Routes = append(app.Routes, handlerEntry(method, path, h.Auth, h.RateLimit, false))
	}
	out := []RouteSection{*app}
	for _, s := range order {
		if s.Prefix == p.app.prefix {
			s.Title += " (this dashboard)"
		}
		sort.SliceStable(s.Routes, func(i, j int) bool { return s.Routes[i].Path < s.Routes[j].Path })
		out = append(out, *s)
	}
	return out
}

// handlerEntry is a plain handler's route. Only a GET can be opened by
// a link.
func handlerEntry(method, path string, auth, limits []string, devOnly bool) RouteEntry {
	e := RouteEntry{Method: method, Path: path, Access: access(auth), Checks: strings.Join(auth, ", "), Limits: strings.Join(limits, "; "), DevOnly: devOnly}
	if method == "GET" && !strings.Contains(path, "{") {
		e.Href = path
	}
	e.Search = strings.ToLower(method + " " + path)
	return e
}

// access says who may open a route, from its auth checks' names:
// loom.SignedIn is anyone signed in, and any other check restricts it
// further (to admins, say).
func access(checks []string) string {
	switch {
	case len(checks) == 0:
		return "public"
	case len(checks) == 1 && checks[0] == "loom.SignedIn":
		return "signed in"
	}
	return "restricted"
}

// Assigns filters the routes, and fills in the view counts and the
// links to open pages.
func (p *RoutesPage) Assigns(c *loom.Ctx) RouteListAssigns {
	views := map[string]int{}
	for _, s := range p.app.app.Stats().Events {
		if s.Name == trace.PageMount && s.By == "route" {
			views[s.Key] = int(s.Count)
		}
	}
	open := map[string]string{} // route -> a URL open on it now
	for _, s := range p.app.app.Sessions() {
		if s.URL != "" && open[s.Path] == "" {
			open[s.Path] = s.URL
		}
	}
	a := RouteListAssigns{Filter: p.filter}
	total, shownN := 0, 0
	for _, s := range p.sections {
		shown := s
		shown.Routes = nil
		for _, r := range s.Routes {
			total++
			if p.filter != "" && !strings.Contains(r.Search, p.filter) {
				continue
			}
			r.Views = views[r.Path]
			if r.Href == "" && r.Method == "LIVE" && open[r.Path] != "" {
				r.Href, r.Example = open[r.Path], true
			}
			shown.Routes = append(shown.Routes, r)
		}
		shownN += len(shown.Routes)
		if len(shown.Routes) > 0 {
			a.Sections = append(a.Sections, shown)
		}
	}
	a.Summary = fmt.Sprintf("%d routes", total)
	if p.filter != "" {
		a.Summary = fmt.Sprintf("%d of %d routes", shownN, total)
	}
	return a
}
