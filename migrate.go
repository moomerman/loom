package loom

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"text/tabwriter"

	"github.com/moomerman/loom/data"
)

// Migrating in production (docs/deploy.md). An app embeds its
// migrations and runs them itself, so a deploy needs only its binary,
// not the loom command and a migrations directory on disk:
//
//	//go:embed migrations
//	var migrations embed.FS
//
//	func main() {
//		if len(os.Args) > 1 && os.Args[1] == "migrate" {
//			m, _ := fs.Sub(migrations, "migrations")
//			os.Exit(loom.MigrateCommand(m, os.Args[2:], os.Stdout, os.Stderr))
//		}
//		...
//	}
//
// `myapp migrate [--json]` then does what `loom migrate` does, with the
// same output and exit codes; `loom new` writes this main.

// Migrate applies the pending migrations in fsys to db, in order, each in
// its own transaction, under an advisory lock so two deploys can't apply
// them at once, and returns those it applied. An edited migration that
// was already applied is DB614. See docs/data.md.
func Migrate(ctx context.Context, db *DB, fsys fs.FS) ([]data.Applied, error) {
	return data.Migrate(ctx, db, fsys)
}

// MigrateResult is the JSON of `migrate --json`: the migrations applied,
// in order (empty when none were pending).
type MigrateResult struct {
	Applied []data.Applied `json:"applied"`
}

// MigrateCommand runs `migrate [status] [--json] [--db URL]` against the
// migrations in fsys, as `loom migrate` does, writing to stdout and
// stderr, and returns the exit code: 0 done, 1 a migration or status
// problem (printed as text, or as {"code", "message", "fix"} with
// --json), 2 a usage or connection error. The database is --db, else
// $DATABASE_URL.
func MigrateCommand(fsys fs.FS, args []string, stdout, stderr io.Writer) int {
	status := len(args) > 0 && args[0] == "status"
	if status {
		args = args[1:]
	}
	fl := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprintln(stderr, "usage: migrate [status] [--json] [--db URL]"); fl.PrintDefaults() }
	asJSON := fl.Bool("json", false, "print JSON")
	url := fl.String("db", os.Getenv("DATABASE_URL"), "the database URL (default $DATABASE_URL)")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() != 0 {
		fl.Usage()
		return 2
	}
	if *url == "" {
		fmt.Fprintln(stderr, "migrate: no database: set DATABASE_URL (e.g. postgres://postgres:postgres@localhost:5432/myapp) or pass --db")
		return 2
	}
	ctx := context.Background()
	db, err := data.Open(ctx, *url)
	if err != nil {
		return migrateFailed(err, *asJSON, stdout, stderr)
	}
	defer db.Close()
	if status {
		st, err := data.MigrationStatus(ctx, db, fsys)
		if err != nil {
			return migrateFailed(err, *asJSON, stdout, stderr)
		}
		if *asJSON {
			writeIndented(stdout, st)
		} else {
			printMigrationStatus(stdout, st)
		}
		if len(st.Problems) > 0 {
			return 1
		}
		return 0
	}
	applied, err := data.Migrate(ctx, db, fsys)
	if err != nil {
		for _, a := range applied {
			fmt.Fprintf(stderr, "applied %s (%dms)\n", a.File, a.Ms)
		}
		return migrateFailed(err, *asJSON, stdout, stderr)
	}
	if *asJSON {
		writeIndented(stdout, MigrateResult{Applied: applied})
		return 0
	}
	if len(applied) == 0 {
		fmt.Fprintln(stdout, "no pending migrations")
	}
	for _, a := range applied {
		fmt.Fprintf(stdout, "applied %s (%dms)\n", a.File, a.Ms)
	}
	return 0
}

// migrateFailed reports err: a data error (an edited migration, failed
// SQL, no connection) on stdout with exit 1, anything else on stderr
// with exit 2.
func migrateFailed(err error, asJSON bool, stdout, stderr io.Writer) int {
	var de *data.Error
	if !errors.As(err, &de) {
		fmt.Fprintf(stderr, "migrate: %v\n", err)
		return 2
	}
	if asJSON {
		writeIndented(stdout, de)
	} else {
		fmt.Fprintln(stdout, de.Error())
	}
	return 1
}

func printMigrationStatus(w io.Writer, st *data.Status) {
	if len(st.Migrations) == 0 {
		fmt.Fprintln(w, "no migrations; create one with `loom db new <name>`")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "FILE\tSTATE\tAPPLIED")
	for _, m := range st.Migrations {
		at := "-"
		if m.AppliedAt != nil {
			at = m.AppliedAt.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", m.File, m.State, at)
	}
	tw.Flush()
	fmt.Fprintf(w, "%d pending\n", st.Pending)
	for _, p := range st.Problems {
		fmt.Fprintf(w, "\n%s\n", p.Error())
	}
}

func writeIndented(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
