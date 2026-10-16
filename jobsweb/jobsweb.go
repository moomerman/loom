// Package jobsweb is a live dashboard for Loom's background jobs, mounted
// in an app with one line:
//
//	admin := app.Group(loom.Auth(requireAdmin))
//	admin.Mount("/admin/jobs", jobsweb.New(jobsweb.Config{
//		ReadOnly: func(c *loom.Ctx) bool { return !isSuperAdmin(c.Request) },
//	}))
//
// It shows queues, jobs, a job's attempts and history, and cron entries,
// and lets operators retry, cancel and delete jobs and run cron entries
// now. Pages update live as jobs change, without polling.
//
// jobsweb is also the reference example of a Loom app, written to be
// read: one file per page (overview.go and overview.hx, ...), shared
// markup in components.hx, and comments that say why. It uses only
// Loom's public API. docs/example-jobsweb.md walks through it.
//
//loom:package
package jobsweb

//go:generate go run github.com/moomerman/loom/cmd/loom build .

import (
	"embed"
	"encoding/json"
	"io/fs"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
)

// Config configures the dashboard. The zero value shows everything and
// allows every action.
type Config struct {
	// ReadOnly, if set and true for a request, hides the action buttons
	// and rejects action events. Use it for operators who may look but
	// not touch. It is checked on every event, not only on page load.
	ReadOnly func(c *loom.Ctx) bool
	// RedactArgs, if set, rewrites a job's args before they are shown,
	// e.g. to hide an email address or token. Job args often hold
	// personal data and are shown in full by default, so set it if
	// yours do. Searching args is turned off when it is set, since a
	// search would reveal what the redaction hides.
	RedactArgs func(jobType string, args json.RawMessage) json.RawMessage
	// Runner is the job runner to show. It defaults to the app's
	// (app.JobRunner()); tests pass loomtest.Jobs' runner.
	Runner *jobs.Runner
}

// App is the mountable dashboard. Create it with New and mount it with
// Group.Mount.
type App struct {
	cfg Config
	app *loom.App // set by Routes
}

// New returns the dashboard with the given configuration.
func New(cfg Config) *App { return &App{cfg: cfg} }

// RequiresAuth makes mounting the dashboard without a loom.Auth check a
// startup error: it can cancel and delete jobs, so it must never be
// public by accident.
func (a *App) RequiresAuth() bool { return true }

// files holds the favicon, linked with asset("icon.svg").
//
//go:embed assets
var files embed.FS

// Routes registers the pages, relative to the mount prefix. They get
// their own root layout, styled with the package's own tokens.toml, so
// the dashboard looks the same in any host app, and an app layout with
// the navigation. The only embedded asset is the favicon: styles travel
// in the components' scoped <style> blocks, not in a CSS file.
func (a *App) Routes(g *loom.Group) {
	a.app = g.App()
	assets, err := fs.Sub(files, "assets")
	if err != nil {
		panic(err)
	}
	g = g.Group(
		loom.Assets(assets),
		loom.RootLayout(Root, func(c *loom.Ctx) RootAssigns {
			return RootAssigns{Title: c.TitleOr("Jobs")}
		}),
		loom.Layout(Shell, func(c *loom.Ctx) ShellAssigns {
			section, _ := c.Get("section").(string)
			return ShellAssigns{Section: section, ReadOnly: a.readOnly(c)}
		}),
	)
	g.Live("/", &OverviewPage{app: a}, Overview)
	g.Live("/jobs", &JobsPage{app: a}, JobList)
	g.Live("/jobs/{id}", &JobPage{app: a}, JobDetail)
	g.Live("/cron", &CronPage{app: a}, Cron)
}

// runner is the runner the dashboard shows. It is looked up when a page
// mounts, not in Routes, so the app may configure its job store after
// mounting the dashboard.
func (a *App) runner() *jobs.Runner {
	if a.cfg.Runner != nil {
		return a.cfg.Runner
	}
	return a.app.JobRunner()
}

func (a *App) readOnly(c *loom.Ctx) bool { return a.cfg.ReadOnly != nil && a.cfg.ReadOnly(c) }

// args formats a job's args for display, redacted if configured.
func (a *App) args(jobType string, args json.RawMessage) string {
	if a.cfg.RedactArgs != nil {
		args = a.cfg.RedactArgs(jobType, args)
	}
	return string(args)
}
