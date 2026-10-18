package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/compiler"
	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/preview"
)

// Check is one finding of `loom doctor`. OK is false for a problem that
// stops the app or its checks; a check that couldn't run (no database,
// no app running) is OK with a message saying so.
type Check struct {
	Check   string `json:"check"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// runDoctor implements `loom doctor`: the checks an agent would
// otherwise run one by one, each with a fix. It exits 1 if any fails.
func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := newCommand("doctor", "doctor [--json] [--url URL] [--db URL] [--dir DIR] [path]", "print the checks as a JSON array", stderr)
	asJSON := fs.JSON
	base := fs.String("url", appURL(), "the running app's URL (default $LOOM_URL, or http://localhost:$PORT, or "+defaultURL+")")
	o := &dbOpts{}
	fs.StringVar(&o.url, "db", os.Getenv("DATABASE_URL"), "the database URL (default $DATABASE_URL)")
	fs.StringVar(&o.dir, "dir", defaultMigrations, "the migrations directory")
	if !fs.parse(args, 0, 1) {
		return 2
	}
	path := "."
	if fs.NArg() == 1 {
		path = fs.Arg(0)
	}
	checks := doctor(path, o, &devClient{base: strings.TrimRight(*base, "/"), http: &http.Client{Timeout: 5 * time.Second}, token: os.Getenv("LOOM_INSPECT_TOKEN")})
	failed := false
	for _, c := range checks {
		failed = failed || !c.OK
	}
	if *asJSON {
		writeJSON(stdout, checks)
	} else {
		for _, c := range checks {
			mark := "ok  "
			if !c.OK {
				mark = "FAIL"
			}
			fmt.Fprintf(stdout, "%s %-10s %s\n", mark, c.Check, c.Message)
			if c.Fix != "" {
				fmt.Fprintf(stdout, "     %-10s fix: %s\n", "", c.Fix)
			}
		}
	}
	if failed {
		return 1
	}
	return 0
}

// doctor runs every check, in the order an agent would fix them.
func doctor(path string, o *dbOpts, c *devClient) []Check {
	checks := []Check{templatesCheck(path)}
	checks = append(checks, databaseChecks(o)...)
	checks = append(checks, chromeCheck(), mcpCheck(path))
	return append(checks, appChecks(c)...)
}

// templatesCheck type-checks the templates and compares the generated
// code with what `loom build` would write.
func templatesCheck(path string) Check {
	c := Check{Check: "templates"}
	res, stale, err := compiler.Stale(path)
	switch {
	case err != nil:
		c.Message = err.Error()
		c.Fix = "pass the directory of the app's templates"
	case res.Diagnostics.HasErrors():
		n := 0
		for _, d := range res.Diagnostics {
			if d.Severity() == "error" {
				n++
			}
		}
		c.Message = fmt.Sprintf("%d template %s", n, plural(n, "error"))
		c.Fix = "run `loom check " + path + "` and fix each error it shows"
	case len(stale) > 0:
		c.Message = "generated code is out of date: " + strings.Join(stale, ", ")
		c.Fix = "run `loom build " + path + "` and commit the result"
	default:
		c.OK = true
		c.Message = fmt.Sprintf("%d %s checked; the generated code is up to date", res.Checked, plural(res.Checked, "template"))
	}
	return c
}

// databaseChecks checks DATABASE_URL and the migrations.
func databaseChecks(o *dbOpts) []Check {
	_, statErr := os.Stat(o.dir)
	hasMigrations := statErr == nil
	if o.url == "" {
		if hasMigrations {
			return []Check{{Check: "database", Message: o.dir + "/ has migrations, but DATABASE_URL isn't set",
				Fix: "export DATABASE_URL=postgres://user:password@localhost:5432/app, or pass --db"}}
		}
		return []Check{{Check: "database", OK: true, Message: "no DATABASE_URL and no migrations: the app keeps its data in memory"}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := o.open(ctx)
	if err != nil {
		return []Check{{Check: "database", Message: err.Error(), Fix: "check DATABASE_URL and that Postgres is running"}}
	}
	db.Close()
	out := []Check{{Check: "database", OK: true, Message: "connected"}}
	if !hasMigrations {
		return out
	}
	st, err := migrationStatus(ctx, o)
	c := Check{Check: "migrations"}
	switch {
	case err != nil:
		c.Message = err.Error()
	case len(st.Problems) > 0:
		c.Message, c.Fix = st.Problems[0].Msg, st.Problems[0].Fix
	case st.Pending > 0:
		c.Message = fmt.Sprintf("%d pending %s", st.Pending, plural(st.Pending, "migration"))
		c.Fix = "run `loom migrate` (loom dev applies them on start)"
	default:
		c.OK, c.Message = true, fmt.Sprintf("%d applied, none pending", len(st.Migrations))
	}
	return append(out, c)
}

// chromeCheck looks for the browser previews and e2e tests use. Only
// they need it, so it is advice, not a failure.
func chromeCheck() Check {
	p, err := preview.Chrome()
	if err != nil {
		return Check{Check: "chrome", OK: true, Message: "not found: only `loom preview screenshot` and browser tests need it", Fix: "install Chrome or Chromium, or set LOOM_CHROME to its path"}
	}
	return Check{Check: "chrome", OK: true, Message: p}
}

// mcpCheck looks for the .mcp.json that gives agents `loom mcp`.
func mcpCheck(path string) Check {
	for dir, _ := filepath.Abs(path); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); err == nil {
			return Check{Check: "mcp", OK: true, Message: filepath.Join(dir, ".mcp.json")}
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil || filepath.Dir(dir) == dir {
			break
		}
	}
	return Check{Check: "mcp", OK: true, Message: "no .mcp.json: agents don't get the loom tools",
		Fix: `add .mcp.json with {"mcpServers": {"loom": {"command": "loom", "args": ["mcp"]}}} (loom new writes it)`}
}

// appChecks asks the running app for its health and runs app.Validate
// in it. With no app running they are skipped.
func appChecks(c *devClient) []Check {
	res, err := c.http.Get(c.base + loom.HealthPath)
	if err != nil {
		return []Check{{Check: "app", OK: true, Message: "no app answering at " + c.base + "; start it with `loom dev` to check it too"}}
	}
	defer res.Body.Close()
	var h loom.Health
	if err := json.NewDecoder(res.Body).Decode(&h); err != nil {
		return []Check{{Check: "app", Message: fmt.Sprintf("%s%s answered %s, not Loom's health JSON", c.base, loom.HealthPath, res.Status), Fix: "pass --url with the app's address"}}
	}
	app := Check{Check: "app", OK: h.Status == "ok", Message: fmt.Sprintf("%s: database %s, jobs %s, %d live pages", h.Status, h.DB, h.Jobs, h.Sessions)}
	if !app.OK {
		app.Fix = "see `loom errors` and the app's log"
	}
	out := []Check{app}
	data, err := c.do("GET", "/_loom/dev/validate", nil)
	var v loom.ValidateResult
	switch {
	case err != nil || json.Unmarshal(data, &v) != nil:
		out = append(out, Check{Check: "startup", OK: true, Message: "the app isn't in dev mode, so its startup checks weren't run"})
	case !v.OK:
		msg := v.Message
		if v.Code != "" {
			msg = v.Code + ": " + msg
		}
		out = append(out, Check{Check: "startup", Message: msg, Fix: v.Fix})
	default:
		out = append(out, Check{Check: "startup", OK: true, Message: "app.Validate passes: jobs, watched tables, migrations and startup checks"})
	}
	return append(out, scansCheck(c))
}

// scansCheck asks the app for Postgres's counts of how each table is
// read, and fails if one is read mostly by sequential scans of many
// rows: a query with no index it can use, as loom_jobs's fetch once was.
func scansCheck(c *devClient) Check {
	body, err := c.do("GET", "/_loom/dev/db/stats", nil)
	var st data.ServerStats
	if err != nil || json.Unmarshal(body, &st) != nil {
		return Check{Check: "scans", OK: true, Message: "the app has no database, or isn't in dev mode, so its tables' scans weren't checked"}
	}
	var warned []string
	for _, t := range st.Tables {
		if t.Warning != "" {
			warned = append(warned, t.Name)
		}
	}
	if len(warned) == 0 {
		return Check{Check: "scans", OK: true, Message: fmt.Sprintf("%d %s, none read mostly by large sequential scans", len(st.Tables), plural(len(st.Tables), "table"))}
	}
	for _, t := range st.Tables {
		if t.Warning != "" {
			return Check{Check: "scans", Message: "sequential scans of many rows on " + strings.Join(warned, ", "), Fix: t.Warning}
		}
	}
	return Check{}
}
