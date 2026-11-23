// Package dashboard is a live dashboard for a Loom app itself: what it
// serves, what it is doing now and how it is holding up. Mount it with
// one line:
//
//	admin := app.Group(loom.Auth(requireAdmin))
//	admin.Mount("/admin/loom", dashboard.New(dashboard.Config{}))
//
// It shows the same things the loom CLI asks a running app under `loom
// dev` (`loom routes`, `loom stats`, `loom events`, `loom queries`,
// `loom sessions`), as pages a person can keep open, in production as
// well as in dev:
//
//   - Overview: uptime, live pages and browsers, actions and errors in
//     the last minute, a chart of the last five minutes, the busiest
//     routes, the job queues, recent errors and the process's memory.
//   - Routes: every URL the app answers, grouped by the sub-app that
//     serves it, with who may see it, and links to open each one.
//   - Activity: the event stream as it happens, by kind.
//   - Queries: every SQL statement the app ran, the busiest first.
//   - Sessions: the pages open in browsers now.
//
// The sidebar also links to the app's other mounted sub-apps, such as
// the jobs dashboard, so their URLs needn't be remembered either.
//
// It reads only what the app keeps in memory (App.Stats, App.Queries,
// App.Stream, App.Sessions, App.Manifest), so it adds no tables and no
// queries of its own, apart from the job queues' counts. Pages re-read
// those every Config.Refresh while they are open. Its own pages' events
// are left out of what it shows, so watching the app doesn't change the
// picture.
//
// Like jobsweb, it uses only Loom's public API: one file per page, shared
// markup in components.hx.
//
//loom:package
package dashboard

//go:generate go run github.com/moomerman/loom/cmd/loom build .

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moomerman/loom"
)

// Config configures the dashboard. The zero value is ready to use.
type Config struct {
	// Refresh is how often an open page re-reads the app's numbers. It
	// defaults to two seconds; the activity feed refreshes twice as
	// often.
	Refresh time.Duration
}

// App is the mountable dashboard. Create it with New and mount it with
// Group.Mount.
type App struct {
	cfg    Config
	app    *loom.App // set by Routes
	prefix string    // where it is mounted, e.g. /admin/loom

	// links are the app's other sub-apps, for the sidebar. They are read
	// from the manifest on first use, not in Routes, since the app may
	// mount more after the dashboard.
	linksOnce sync.Once
	links     []Link
}

// New returns the dashboard with the given configuration.
func New(cfg Config) *App {
	if cfg.Refresh <= 0 {
		cfg.Refresh = 2 * time.Second
	}
	return &App{cfg: cfg}
}

// RequiresAuth makes mounting the dashboard without a loom.Auth check a
// startup error: it shows the app's routes, SQL and open sessions, so it
// must never be public by accident.
func (a *App) RequiresAuth() bool { return true }

// files holds the favicon, linked with asset("icon.svg").
//
//go:embed assets
var files embed.FS

// Routes registers the pages, relative to the mount prefix, with their
// own root layout and tokens, so the dashboard looks the same in any
// host app.
func (a *App) Routes(g *loom.Group) {
	a.app = g.App()
	a.prefix = g.Prefix()
	assets, err := fs.Sub(files, "assets")
	if err != nil {
		panic(err)
	}
	g = g.Group(
		loom.Assets(assets),
		loom.RootLayout(Root, func(c *loom.Ctx) RootAssigns {
			return RootAssigns{Title: c.TitleOr("Loom")}
		}),
		loom.Layout(Shell, func(c *loom.Ctx) ShellAssigns {
			section, _ := c.Get("section").(string)
			return ShellAssigns{Section: section, App: a.title(), Dev: a.app.Dev(), Links: a.mountLinks()}
		}),
	)
	g.Live("/", &OverviewPage{app: a}, Overview)
	g.Live("/routes", &RoutesPage{app: a}, RouteList)
	g.Live("/activity", &ActivityPage{app: a}, Activity)
	g.Live("/queries", &QueriesPage{app: a}, Queries)
	g.Live("/sessions", &SessionsPage{app: a}, Sessions)
}

// Link is another sub-app the app mounts, for the sidebar.
type Link struct {
	Label string // e.g. "jobsweb"
	Href  string // e.g. /admin/jobs
}

// mountLinks lists the app's other mounted sub-apps that a browser can
// open: its home, or else the first page it registers without a
// parameter. Dev-only ones are
// left out outside dev, where they answer 404.
func (a *App) mountLinks() []Link {
	a.linksOnce.Do(func() {
		for _, m := range a.app.Manifest().Mounts {
			if m.Prefix == a.prefix || (m.DevOnly && !a.app.Dev()) {
				continue
			}
			href := ""
			for _, r := range m.Routes {
				if !strings.HasPrefix(r, "/") || strings.Contains(r, "{") {
					continue // a handler, or a page that needs parameters
				}
				if r == "/" {
					href = m.Prefix // the sub-app's home: the best link
					break
				}
				if href == "" {
					href = m.Prefix + r // else the first page it registers
				}
			}
			if href == "" {
				continue
			}
			a.links = append(a.links, Link{Label: mountName(m.Type), Href: href})
		}
		sort.Slice(a.links, func(i, j int) bool { return a.links[i].Href < a.links[j].Href })
	})
	return a.links
}

// title is the app's name for the sidebar.
func (a *App) title() string {
	if t := a.app.Title(); t != "" {
		return t
	}
	return "Loom"
}

// own reports whether route is one of the dashboard's own pages, whose
// events it leaves out.
func (a *App) own(route string) bool {
	return a.prefix != "" && (route == a.prefix || strings.HasPrefix(route, a.prefix+"/"))
}

// ownSessions are the dashboard's own open pages, by sid: events such
// as stream.open carry a sid but no route.
func (a *App) ownSessions() map[string]bool {
	out := map[string]bool{}
	for _, s := range a.app.Sessions() {
		if a.own(s.Path) {
			out[s.SID] = true
		}
	}
	return out
}

// tick re-reads the page every Config.Refresh: each page's Mount starts
// it and its "tick" event sets it again.
func (a *App) tick(c *loom.Ctx) { c.SendAfter(a.cfg.Refresh, "tick", nil) }

// section tells the app layout which nav item to highlight.
func section(c *loom.Ctx, name string) { c.Set("section", name) }
