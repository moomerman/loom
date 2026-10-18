package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/data"
)

// The database commands talk to Postgres directly, not to a running app:
// `loom db new`, `loom migrate [status]`, `loom db schema` and `loom db
// query`. They find the database in --db or $DATABASE_URL, the variable
// apps pass to loom.Config, and migrations in --dir (default
// ./migrations). See docs/data.md and docs/cli.md.

const defaultMigrations = "migrations"

func runDB(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "new":
			return runDBNew(args[1:], stdout, stderr)
		case "install":
			return runDBInstall(args[1:], stdout, stderr)
		case "schema":
			return runDBSchema(args[1:], stdout, stderr)
		case "query":
			return runDBQuery(args[1:], stdout, stderr)
		case "stats":
			return runDBStats(args[1:], stdout, stderr)
		}
	}
	fmt.Fprint(stderr, "usage: loom db new|install|schema|query|stats [--json] ...\n\nRun \"loom help\" for details.\n")
	return 2
}

// errUsage marks an error that is the caller's mistake (exit 2), such as
// a missing database URL.
var errUsage = errors.New("usage")

// dbCommand parses the common flags of a database command and runs it.
// run returns the JSON value and a function printing it for people. Data
// errors print as text, or as {"code", "message", "fix"} with --json,
// and exit 1; other errors exit 2.
func dbCommand(name, usageLine string, args []string, stdout, stderr io.Writer, nargs int, setup func(fs *flag.FlagSet),
	run func(o *dbOpts, args []string) (v any, human func(w io.Writer), code int, err error)) int {
	fs := newCommand(name, usageLine, "print JSON", stderr)
	o := &dbOpts{}
	fs.StringVar(&o.url, "db", os.Getenv("DATABASE_URL"), "the database URL (default $DATABASE_URL)")
	fs.StringVar(&o.dir, "dir", defaultMigrations, "the migrations directory")
	if setup != nil {
		setup(fs.FlagSet)
	}
	if !fs.parse(args, nargs, nargs) {
		return 2
	}
	asJSON := fs.JSON
	v, human, code, err := run(o, fs.Args())
	var de *data.Error
	switch {
	case errors.As(err, &de):
		if *asJSON {
			writeJSON(stdout, de)
		} else {
			fmt.Fprintln(stdout, de.Error())
		}
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "loom %s: %v\n", name, strings.TrimPrefix(err.Error(), errUsage.Error()+": "))
		return 2
	}
	if *asJSON {
		writeJSON(stdout, v)
	} else {
		human(stdout)
	}
	return code
}

type dbOpts struct {
	url string
	dir string
}

// open connects to the database.
func (o *dbOpts) open(ctx context.Context) (*data.DB, error) {
	if o.url == "" {
		return nil, fmt.Errorf("%w: no database: set DATABASE_URL (e.g. postgres://postgres:postgres@localhost:5432/myapp) or pass --db", errUsage)
	}
	return data.Open(ctx, o.url)
}

func (o *dbOpts) fsys() fs.FS { return os.DirFS(o.dir) }

// file is a migration's path for people: dir/name.
func (o *dbOpts) file(name string) string { return filepath.Join(o.dir, name) }

var migrationNameArg = regexp.MustCompile(`^[a-z0-9_]+$`)

// NewMigration is the JSON of `loom db new`.
type NewMigration struct {
	File    string `json:"file"`
	Version int64  `json:"version"`
	Name    string `json:"name"`
}

func runDBNew(args []string, stdout, stderr io.Writer) int {
	return dbCommand("db new", "db new [--json] [--dir DIR] NAME", args, stdout, stderr, 1, nil,
		func(o *dbOpts, args []string) (any, func(io.Writer), int, error) {
			name := args[0]
			if !migrationNameArg.MatchString(name) {
				return nil, nil, 0, fmt.Errorf("%w: migration names are lowercase letters, digits and underscores, e.g. add_due_dates; got %q", errUsage, name)
			}
			migs, err := data.LoadMigrations(o.fsys())
			if err != nil {
				return nil, nil, 0, err
			}
			next := int64(1)
			if n := len(migs); n > 0 {
				next = migs[n-1].Version + 1
			}
			if err := os.MkdirAll(o.dir, 0o755); err != nil {
				return nil, nil, 0, err
			}
			base := fmt.Sprintf("%04d_%s", next, name)
			path := o.file(base + ".sql")
			src := fmt.Sprintf("-- %s: plain SQL, applied once, in a transaction, by `loom migrate`.\n-- Migrations are forward-only: once this is applied, change things in a new one.\n-- To re-render pages that loom.Watch a table when it changes, track it: -- loom:track <table>\n\n", base)
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return nil, nil, 0, err
			}
			defer f.Close()
			if _, err := f.WriteString(src); err != nil {
				return nil, nil, 0, err
			}
			res := NewMigration{File: path, Version: next, Name: name}
			return res, func(w io.Writer) { fmt.Fprintf(w, "created %s\n", path) }, 0, nil
		})
}

// Installed is the JSON of `loom db install`: the migration files it
// created, in order (empty when everything was installed already).
type Installed struct {
	Package string         `json:"package"`
	Created []NewMigration `json:"created"`
}

// runDBInstall copies a sub-app package's migrations into the app's
// migrations directory, numbered after the app's own, each with a
// `-- loom:from <package> <file>` line. Ones already installed are
// skipped, so running it again after upgrading the package installs only
// the new ones. See loom.HasMigrations.
func runDBInstall(args []string, stdout, stderr io.Writer) int {
	return dbCommand("db install", "db install [--json] [--dir DIR] PACKAGE", args, stdout, stderr, 1, nil,
		func(o *dbOpts, args []string) (any, func(io.Writer), int, error) {
			pkg := args[0]
			out, err := exec.Command("go", "list", "-f", "{{.Dir}}", pkg).Output()
			if err != nil {
				return nil, nil, 0, fmt.Errorf("%w: can't find package %s (go list: %v); run it in the app's module, with the package in go.mod", errUsage, pkg, err)
			}
			src := filepath.Join(strings.TrimSpace(string(out)), "migrations")
			theirs, err := data.LoadMigrations(os.DirFS(src))
			if err != nil {
				return nil, nil, 0, err
			}
			if len(theirs) == 0 {
				return nil, nil, 0, fmt.Errorf("%w: package %s has no migrations (no %s)", errUsage, pkg, src)
			}
			created, err := installMigrations(o.dir, pkg, theirs)
			if err != nil {
				return nil, nil, 0, err
			}
			res := Installed{Package: pkg, Created: created}
			return res, func(w io.Writer) {
				if len(res.Created) == 0 {
					fmt.Fprintf(w, "%s's migrations are all installed\n", pkg)
				}
				for _, c := range res.Created {
					fmt.Fprintf(w, "created %s\n", c.File)
				}
				if len(res.Created) > 0 {
					fmt.Fprintln(w, "run `loom migrate` to apply them, and commit them with the app")
				}
			}, 0, nil
		})
}

// installMigrations copies pkg's migrations into dir, numbered after the
// ones there, each with a `-- loom:from <package> <file>` line, skipping
// those installed already, and returns the files it created.
func installMigrations(dir, pkg string, theirs []data.Migration) ([]NewMigration, error) {
	ours, err := data.LoadMigrations(os.DirFS(dir))
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	next := int64(1)
	for _, m := range ours {
		have[m.From] = true
		next = m.Version + 1
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	created := []NewMigration{}
	for _, m := range theirs {
		from := pkg + " " + m.File
		if have[from] {
			continue
		}
		name := loom.InstalledName(pkg, m.File)
		path := filepath.Join(dir, fmt.Sprintf("%04d_%s.sql", next, name))
		body := fmt.Sprintf("-- loom:from %s\n-- Installed by `loom db install %s`; applied by `loom migrate` like the app's own.\n\n%s", from, pkg, m.SQL)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return nil, err
		}
		created = append(created, NewMigration{File: path, Version: next, Name: name})
		next++
	}
	return created, nil
}

// runMigrate is `loom migrate [status]`: loom.MigrateCommand, which an
// app's own binary runs too, on the migrations in --dir.
func runMigrate(args []string, stdout, stderr io.Writer) int {
	dir, rest := defaultMigrations, []string{}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case (a == "--dir" || a == "-dir") && i+1 < len(args):
			dir = args[i+1]
			i++
		case strings.HasPrefix(a, "--dir="), strings.HasPrefix(a, "-dir="):
			dir = a[strings.IndexByte(a, '=')+1:]
		default:
			rest = append(rest, a)
		}
	}
	return loom.MigrateCommand(os.DirFS(dir), rest, stdout, stderr)
}

func migrationStatus(ctx context.Context, o *dbOpts) (*data.Status, error) {
	db, err := o.open(ctx)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return data.MigrationStatus(ctx, db, o.fsys())
}

func dbSchema(ctx context.Context, o *dbOpts) (*data.SchemaInfo, error) {
	db, err := o.open(ctx)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return data.Schema(ctx, db)
}

func runDBSchema(args []string, stdout, stderr io.Writer) int {
	return dbCommand("db schema", "db schema [--json] [--db URL]", args, stdout, stderr, 0, nil,
		func(o *dbOpts, _ []string) (any, func(io.Writer), int, error) {
			s, err := dbSchema(context.Background(), o)
			if err != nil {
				return nil, nil, 0, err
			}
			return s, func(w io.Writer) { printSchema(w, s) }, 0, nil
		})
}

func printSchema(w io.Writer, s *data.SchemaInfo) {
	if len(s.Tables) == 0 {
		fmt.Fprintln(w, "no tables; create them in a migration (`loom db new <name>`) and run `loom migrate`")
		return
	}
	for i, t := range s.Tables {
		if i > 0 {
			fmt.Fprintln(w)
		}
		title := t.Name
		if t.Tracked {
			title += " (tracked)"
		}
		fmt.Fprintln(w, title)
		key := map[string]bool{}
		for _, k := range t.PrimaryKey {
			key[k] = true
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, c := range t.Columns {
			var notes []string
			if key[c.Name] {
				notes = append(notes, "primary key")
			}
			if !c.Nullable {
				notes = append(notes, "not null")
			}
			if c.Default != "" {
				notes = append(notes, "default "+c.Default)
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", c.Name, c.Type, strings.Join(notes, ", "))
		}
		tw.Flush()
	}
}

func dbQuery(ctx context.Context, o *dbOpts, query string, limit int) (*data.QueryResult, error) {
	db, err := o.open(ctx)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return data.QueryReadOnly(ctx, db, query, limit)
}

func runDBQuery(args []string, stdout, stderr io.Writer) int {
	var limit int
	return dbCommand("db query", "db query [--json] [--db URL] [--limit N] SQL", args, stdout, stderr, 1,
		func(fs *flag.FlagSet) { fs.IntVar(&limit, "limit", 100, "print at most this many rows") },
		func(o *dbOpts, args []string) (any, func(io.Writer), int, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			res, err := dbQuery(ctx, o, args[0], limit)
			if err != nil {
				return nil, nil, 0, err
			}
			return res, func(w io.Writer) { printRows(w, res) }, 0, nil
		})
}

func printRows(w io.Writer, res *data.QueryResult) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.ToUpper(strings.Join(res.Columns, "\t")))
	for _, r := range res.Rows {
		cells := make([]string, len(r))
		for i, v := range r {
			switch v := v.(type) {
			case nil:
				cells[i] = "NULL"
			case time.Time:
				cells[i] = v.Format(time.RFC3339Nano)
			default:
				cells[i] = oneLine(fmt.Sprint(v), 60)
			}
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	tw.Flush()
	if res.Truncated {
		fmt.Fprintf(w, "(first %d rows; raise --limit to see more)\n", len(res.Rows))
	} else {
		fmt.Fprintf(w, "(%d %s)\n", len(res.Rows), plural(len(res.Rows), "row"))
	}
}
