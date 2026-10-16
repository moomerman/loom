package loom

import (
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/render"
)

// The manifest is the app's behaviour as a reviewer sees it: its routes
// and who may reach them, the events each page sends and what handles
// them, the tables whose changes re-render them, and its background
// jobs. It is built from what the app registered and what the
// compiler recorded in the generated code, so it needs no database and
// no running server, and it is the same on every run.
//
// An app commits it as loom.manifest, kept fresh by a test that calls
// loomtest.Manifest, so every pull request that changes the app's
// behaviour also changes that file, in words a reviewer can approve
// without reading Go or templates. `loom manifest --json` gives the
// same from a running dev server. See docs/manifest.md.

// ManifestSecurity is the security headers every response carries:
// the Content-Security-Policy (Config.CSP) and whether
// Strict-Transport-Security is sent (Config.BaseURL is https).
type ManifestSecurity struct {
	CSP  string `json:"csp"`
	HSTS bool   `json:"hsts"`
}

// Manifest describes an app's behaviour for review.
type Manifest struct {
	Routes   []ManifestRoute   `json:"routes"`
	Layouts  []ManifestLayout  `json:"layouts"`
	Handlers []ManifestHandler `json:"handlers"`
	Mounts   []ManifestMount   `json:"mounts"`
	Jobs     jobs.Setup        `json:"jobs"`
	// PubSub is how broadcasts reach pages: "postgres" for the default
	// (every node's, by NOTIFY, when the app has a database; in-process
	// without one), or the Go type of Config.PubSub.
	PubSub string `json:"pubsub"`
	// Security is the headers every response carries (headers.go).
	Security ManifestSecurity `json:"security"`
	// Migrations are set by AddMigrations; App.Manifest doesn't know
	// where they are.
	Migrations []ManifestMigration `json:"migrations"`
	Flags      []ManifestFlag      `json:"flags"`
}

// ManifestRoute is a page route outside any mount.
type ManifestRoute struct {
	Path      string          `json:"path"`
	Page      string          `json:"page"`       // the Go type handling the page's events
	Component string          `json:"component"`  // e.g. templates.TodoApp
	Layout    string          `json:"layout"`     // the app layout; "" for none
	Auth      []string        `json:"auth"`       // auth checks, outer first
	RateLimit []string        `json:"rate_limit"` // rate limits, outer first (loom.RateLimit)
	Events    []ManifestEvent `json:"events"`     // what the page and its layout send
	Handler   string          `json:"handler"`    // what handles them
	Watch     []string        `json:"watch"`      // tables whose changes re-render the page
	Params    string          `json:"params"`     // what reads the URL's state (HandleParams); "" for none
	DevOnly   bool            `json:"dev_only"`   // served only in dev (loom.DevOnly)
}

// ManifestLayout is an app layout with the events it sends. A live
// layout (LiveLayout) handles them itself, named in Handler; otherwise
// each page in it does, and Handler is "".
type ManifestLayout struct {
	Component string          `json:"component"`
	Handler   string          `json:"handler,omitempty"`
	Events    []ManifestEvent `json:"events"`
}

// ManifestEvent is a server event a page sends, with every place that
// sends it.
type ManifestEvent struct {
	Name string `json:"name"`
	// Handler is set on a route's event its live layout handles rather
	// than the page, e.g. (*issues.Shell).Events.
	Handler string   `json:"handler,omitempty"`
	On      []string `json:"on"`   // the DOM or behaviour events, e.g. click
	Vals    []string `json:"vals"` // val-* names sent with it
	At      []string `json:"at"`   // template positions, e.g. todo_app.hx:26:59
}

// ManifestHandler is a plain HTTP handler (Group.Handle).
type ManifestHandler struct {
	Pattern   string   `json:"pattern"` // e.g. POST /login
	Auth      []string `json:"auth"`
	RateLimit []string `json:"rate_limit"`
}

// ManifestMount is a mounted sub-app. Its own pages are its package's
// business; the app decides where it lives and who may reach it.
type ManifestMount struct {
	Prefix string   `json:"prefix"`
	Type   string   `json:"type"`
	Auth   []string `json:"auth"`
	Routes []string `json:"routes"`
	// RateLimits are the limits on the sub-app's routes: "every route: "
	// and the limit for one that covers them all, else the route and the
	// limit, e.g. "/sign-in: 5 per minute by email on sign_in".
	RateLimits []string `json:"rate_limits"`
	// DevOnly is set when the sub-app's pages are served only in dev
	// (loom.DevOnly), such as devmail's.
	DevOnly bool `json:"dev_only"`
}

// ManifestMigration is a migration file: what its statements do, and
// the tables it turns change tracking on for.
type ManifestMigration struct {
	File       string   `json:"file"`
	Statements []string `json:"statements"` // each statement's first line
	Tracks     []string `json:"tracks"`
}

// ManifestFlag is something a reviewer should look at: not an error,
// since it may be intended, but easy to miss in the code.
type ManifestFlag struct {
	Subject string `json:"subject"` // e.g. "mount /admin/jobs"
	Message string `json:"message"`
}

// Manifest describes the app's behaviour for review; see loom.manifest
// and docs/manifest.md.
func (a *App) Manifest() *Manifest {
	m := &Manifest{Routes: []ManifestRoute{}, Layouts: []ManifestLayout{}, Handlers: []ManifestHandler{}, Mounts: []ManifestMount{}, Migrations: []ManifestMigration{}, Flags: []ManifestFlag{}}
	mounts := map[string]int{}                  // index in m.Mounts, by prefix
	public := map[string]bool{}                 // sub-apps public by design, by prefix
	limited := map[string]map[*limit][]string{} // the routes each limit covers, by prefix
	mount := func(g *Group, auth []AuthFunc, limits []*limit, sub string) {
		i, ok := mounts[g.prefix]
		if !ok {
			i = len(m.Mounts)
			mounts[g.prefix] = i
			m.Mounts = append(m.Mounts, ManifestMount{Prefix: g.prefix, Type: g.mountType(), Auth: manifestAuth(auth), RateLimits: []string{}})
			limited[g.prefix] = map[*limit][]string{}
			if mg := g.mountGroup(); mg != nil {
				public[g.prefix] = mg.public
			}
		}
		m.Mounts[i].Routes = append(m.Mounts[i].Routes, sub)
		for _, l := range limits {
			limited[g.prefix][l] = append(limited[g.prefix][l], sub)
		}
		m.Mounts[i].DevOnly = m.Mounts[i].DevOnly || g.devOnly()
	}
	layouts := map[string]bool{}
	for _, rt := range a.routes {
		if rt.group.prefix != "" {
			sub := strings.TrimPrefix(rt.path, rt.group.prefix)
			if sub == "" {
				sub = "/"
			}
			mount(rt.group, rt.auth, rt.limits, sub)
			continue
		}
		r := ManifestRoute{Path: rt.path, Component: qualified(rt.tmpl), Layout: rt.group.appLayout().name(), Auth: manifestAuth(rt.auth), RateLimit: limitNames(rt.limits), Watch: append([]string{}, rt.watch...), DevOnly: rt.group.devOnly()}
		r.Events = manifestEvents(rt.tmpl.ComponentEvents())
		if lay := rt.group.appLayout(); lay != nil {
			ml := ManifestLayout{Component: r.Layout, Events: manifestEvents(lay.tmpl.ComponentEvents())}
			if lay.live != nil {
				ml.Handler = fmt.Sprintf("(%T).Events", lay.live)
				evs := eventsOf(lay.live)
				for i, e := range r.Events {
					if evs[e.Name] != nil {
						r.Events[i].Handler = ml.Handler
					}
				}
			}
			if !layouts[r.Layout] && len(ml.Events) > 0 {
				layouts[r.Layout] = true
				m.Layouts = append(m.Layouts, ml)
			}
		}
		r.Page = fmt.Sprintf("%T", rt.prototype)
		r.Handler = "(" + r.Page + ").Events"
		if _, ok := rt.prototype.(ParamsHandler); ok {
			r.Params = "(" + r.Page + ").HandleParams"
		}
		m.Routes = append(m.Routes, r)
	}
	for _, h := range a.handlers {
		if h.group.prefix != "" {
			method, path, _ := strings.Cut(h.pattern, " ")
			mount(h.group, h.auth, h.limits, method+" "+strings.TrimPrefix(path, h.group.prefix))
			continue
		}
		m.Handlers = append(m.Handlers, ManifestHandler{Pattern: h.pattern, Auth: manifestAuth(h.auth), RateLimit: limitNames(h.limits)})
	}
	for i, mt := range m.Mounts {
		m.Mounts[i].RateLimits = mountLimits(limited[mt.Prefix], len(mt.Routes))
		if len(mt.Auth) == 0 && !public[mt.Prefix] {
			m.flag("mount "+mt.Prefix, "%s is mounted with no auth check: anyone can reach it", mt.Type)
		}
	}
	if a.jobs != nil {
		m.Jobs = a.jobs.Setup()
	} else {
		m.Jobs = jobs.Setup{Types: []jobs.TypeSetup{}, Queues: []jobs.Queue{}, Cron: []jobs.CronSetup{}}
	}
	m.Jobs.Mode = string(a.cfg.Jobs)
	m.PubSub = "postgres"
	if _, ok := a.cfg.PubSub.(*nodePubSub); !ok {
		m.PubSub = fmt.Sprintf("%T", a.cfg.PubSub)
	}
	m.Security = ManifestSecurity{CSP: a.cfg.CSP, HSTS: a.hsts()}
	return m
}

// AddMigrations adds the migrations in fsys (as for `loom migrate`) to
// the manifest, each summarised by its statements' first lines, so a
// reviewer sees a schema change next to the behaviour it enables.
func (m *Manifest) AddMigrations(fsys fs.FS) error {
	migs, err := data.LoadMigrations(fsys)
	if err != nil {
		return err
	}
	m.Migrations = []ManifestMigration{}
	for _, mig := range migs {
		m.Migrations = append(m.Migrations, ManifestMigration{File: mig.File, Statements: summarise(statementLines(mig.SQL)), Tracks: append([]string{}, mig.Track...)})
	}
	return nil
}

// statementLines returns the first line of each SQL statement, without
// comments, shortened to 80 characters. Semicolons inside quotes and
// dollar-quoted bodies don't end a statement.
func statementLines(sql string) []string {
	out := []string{}
	var stmt strings.Builder
	flush := func() {
		text := strings.TrimSpace(stmt.String())
		stmt.Reset()
		if text == "" {
			return
		}
		first, _, _ := strings.Cut(text, "\n")
		first = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(first), "("))
		if len(first) > 80 {
			first = first[:77] + "..."
		}
		out = append(out, first)
	}
	dollar := ""
	quote := false
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		switch {
		case dollar != "":
			if strings.HasPrefix(sql[i:], dollar) {
				stmt.WriteString(dollar)
				i += len(dollar) - 1
				dollar = ""
				continue
			}
		case quote:
			if c == '\'' {
				quote = false
			}
		case c == '\'':
			quote = true
		case c == '-' && strings.HasPrefix(sql[i:], "--"):
			end := strings.IndexByte(sql[i:], '\n')
			if end < 0 {
				i = len(sql)
			} else {
				i += end - 1
			}
			continue
		case c == '$':
			if tag := dollarTag.FindString(sql[i:]); tag != "" {
				dollar = tag
				stmt.WriteString(tag)
				i += len(tag) - 1
				continue
			}
		case c == ';':
			flush()
			continue
		}
		stmt.WriteByte(c)
	}
	flush()
	return out
}

// dataStatement matches a statement that changes rows, not the schema,
// capturing its table.
var dataStatement = regexp.MustCompile(`(?i)^(?:insert\s+into|update|delete\s+from)\s+([a-z_][a-z0-9_.]*)`)

// summarise keeps schema statements and folds statements that change
// rows (seed data, backfills) into one line naming their tables, which
// says more to a reviewer than a page of INSERTs.
func summarise(lines []string) []string {
	out := []string{}
	var tables []string
	n := 0
	for _, l := range lines {
		if m := dataStatement.FindStringSubmatch(l); m != nil {
			tables = addUnique(tables, m[1])
			n++
			continue
		}
		out = append(out, l)
	}
	if n == 1 {
		out = append(out, "1 statement changing rows of "+tables[0])
	} else if n > 1 {
		out = append(out, fmt.Sprintf("%d statements changing rows of %s", n, strings.Join(tables, ", ")))
	}
	return out
}

var dollarTag = regexp.MustCompile(`^\$[A-Za-z_]*\$`)

func (m *Manifest) flag(subject, format string, args ...any) {
	m.Flags = append(m.Flags, ManifestFlag{Subject: subject, Message: fmt.Sprintf(format, args...)})
}

// manifestEvents merges event uses by name, in order of first use.
func manifestEvents(uses []render.EventUse) []ManifestEvent {
	out := []ManifestEvent{}
	index := map[string]int{}
	for _, u := range uses {
		i, ok := index[u.Name]
		if !ok {
			i = len(out)
			index[u.Name] = i
			out = append(out, ManifestEvent{Name: u.Name, On: []string{}, Vals: []string{}, At: []string{}})
		}
		e := &out[i]
		e.On = addUnique(e.On, u.On)
		for _, v := range u.Vals {
			e.Vals = addUnique(e.Vals, v)
		}
		e.At = addUnique(e.At, u.At)
	}
	return out
}

func addUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// Text renders the manifest for people: the committed loom.manifest.
// Template positions are left out, so moving markup within a file
// doesn't change it; the JSON has them.
func (m *Manifest) Text() string {
	var b strings.Builder
	line := func(indent int, format string, args ...any) {
		b.WriteString(strings.Repeat("  ", indent))
		fmt.Fprintf(&b, format, args...)
		b.WriteString("\n")
	}
	list := func(items []string) string {
		if len(items) == 0 {
			return "none"
		}
		return strings.Join(items, ", ")
	}
	describe := func(e ManifestEvent) string {
		detail := "on " + strings.Join(e.On, ", ")
		for _, v := range e.Vals {
			detail += ", val-" + v
		}
		if e.Handler != "" {
			return fmt.Sprintf("%s (%s), handled by its layout's %s", e.Name, detail, e.Handler)
		}
		return fmt.Sprintf("%s (%s)", e.Name, detail)
	}
	events := func(indent int, evs []ManifestEvent) {
		for _, e := range evs {
			line(indent, "%s", describe(e))
		}
	}
	line(0, "# What this app does, for review. Generated by loomtest.Manifest;")
	line(0, "# refresh it with go test -update and commit it with your change.")
	line(0, "")
	line(0, "routes")
	for _, r := range m.Routes {
		line(0, "")
		head := fmt.Sprintf("%s  live page %s", r.Path, r.Component)
		if r.Layout != "" {
			head += " in " + r.Layout
		}
		line(1, "%s", head)
		line(2, "auth: %s", list(r.Auth))
		if len(r.RateLimit) > 0 {
			line(2, "rate limit: %s", strings.Join(r.RateLimit, "; "))
		}
		if r.DevOnly {
			line(2, "dev only: 404 unless the app runs in dev")
		}
		if len(r.Events) == 0 && r.Layout != "" {
			line(2, "events: only its layout's")
		} else if len(r.Events) == 0 {
			line(2, "events: none")
		} else {
			byLayout := true
			for _, e := range r.Events {
				byLayout = byLayout && e.Handler != ""
			}
			if byLayout {
				line(2, "events, all handled by its layout:")
			} else {
				line(2, "events, handled by %s:", r.Handler)
			}
			events(3, r.Events)
		}
		if len(r.Watch) > 0 {
			line(2, "re-renders when these tables change: %s", strings.Join(r.Watch, ", "))
		}
		if r.Params != "" {
			line(2, "state in the URL, read by %s; links within the route patch it", r.Params)
		}
	}
	if len(m.Layouts) > 0 {
		line(0, "")
		line(0, "layouts")
		line(0, "")
		for _, l := range m.Layouts {
			line(1, "%s", l.Component)
			if l.Handler != "" {
				line(2, "events, handled by %s:", l.Handler)
			} else {
				line(2, "events, handled by each page in it:")
			}
			events(3, l.Events)
		}
	}
	if len(m.Handlers) > 0 {
		line(0, "")
		line(0, "handlers")
		line(0, "")
		for _, h := range m.Handlers {
			line(1, "%s  auth: %s", h.Pattern, list(h.Auth))
			if len(h.RateLimit) > 0 {
				line(2, "rate limit: %s", strings.Join(h.RateLimit, "; "))
			}
		}
	}
	if len(m.Mounts) > 0 {
		line(0, "")
		line(0, "mounts")
		line(0, "")
		for _, mt := range m.Mounts {
			line(1, "%s  %s", mt.Prefix, mt.Type)
			line(2, "auth: %s", list(mt.Auth))
			if mt.DevOnly {
				line(2, "dev only: 404 unless the app runs in dev")
			}
			line(2, "routes: %s", strings.Join(mt.Routes, ", "))
			for _, l := range mt.RateLimits {
				line(2, "rate limit: %s", l)
			}
		}
	}
	if len(m.Migrations) > 0 {
		line(0, "")
		line(0, "database (migrations)")
		line(0, "")
		for _, mig := range m.Migrations {
			head := mig.File
			if len(mig.Tracks) > 0 {
				head += "  tracks " + strings.Join(mig.Tracks, ", ")
			}
			line(1, "%s", head)
			for _, st := range mig.Statements {
				line(2, "%s", st)
			}
		}
	}
	j := m.Jobs
	if len(j.Types) > 0 || len(j.Cron) > 0 {
		line(0, "")
		line(0, "background jobs")
		line(0, "")
		for _, t := range j.Types {
			line(1, "%s  queue %s, %d attempts, timeout %s, args: %s", t.Type, t.Queue, t.MaxAttempts, t.Timeout, list(t.Args))
		}
		for _, c := range j.Cron {
			args := ""
			if c.Args != "{}" {
				args = " " + c.Args
			}
			line(1, "cron %q  %s%s", c.Expr, c.Type, args)
		}
		for _, q := range j.Queues {
			line(1, "queue %s  %d workers", q.Name, q.Concurrency)
		}
		switch JobsMode(j.Mode) {
		case JobsEnqueueOnly:
			line(1, "this node enqueues only; nodes with Config.Jobs run or jobs-only run them")
		case JobsOnly:
			line(1, "this node runs jobs and cron only, serving no pages")
		default:
			line(1, "every node runs jobs and cron (Config.Jobs run)")
		}
	}
	line(0, "")
	if m.PubSub == "postgres" {
		line(0, "broadcasts reach every node's pages, by Postgres NOTIFY (in-process without a database)")
	} else {
		line(0, "broadcasts go through %s", m.PubSub)
	}
	line(0, "responses carry Content-Security-Policy: %s", m.Security.CSP)
	if m.Security.HSTS {
		line(0, "and Strict-Transport-Security (BaseURL is https)")
	}
	if len(m.Flags) > 0 {
		line(0, "")
		line(0, "for review")
		line(0, "")
		for _, f := range m.Flags {
			line(1, "%s: %s", f.Subject, f.Message)
		}
	}
	return b.String()
}

// mountLimits lists a mount's rate limits: once for a limit on every
// route, else once per route it covers, in the order they were set.
func mountLimits(covers map[*limit][]string, routes int) []string {
	ls := make([]*limit, 0, len(covers))
	for l := range covers {
		ls = append(ls, l)
	}
	slices.SortFunc(ls, func(a, b *limit) int { return a.seq - b.seq })
	out := []string{}
	for _, l := range ls {
		if len(covers[l]) == routes {
			out = append(out, "every route: "+l.String())
			continue
		}
		for _, sub := range covers[l] {
			out = append(out, sub+": "+l.String())
		}
	}
	return out
}

// manifestAuth names auth checks, without the -fm Go adds to method
// values: issues.(*App).requireUser.
func manifestAuth(checks []AuthFunc) []string {
	names := authNames(checks)
	for i, n := range names {
		names[i] = strings.TrimSuffix(n, "-fm")
	}
	return names
}
