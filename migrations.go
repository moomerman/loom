package loom

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"strings"

	"github.com/moomerman/loom/data"
)

// Sub-app migrations (docs/subapps.md). A sub-app that keeps data in the
// app's database ships its migrations in its package; `loom db install`
// copies each into the app's migrations directory as
// NNNN_<package>_<name>.sql, with a `-- loom:from <package> <file>`
// line. From there they are the app's migrations: reviewed in its pull
// requests, applied by `loom migrate` and `loom dev` in order with the
// app's own, checksummed, and shown by `migrate status`, `db schema`
// and the manifest.

// Loom's own tables (loom_clients, loom_user_sessions, loom_rate_limits,
// loom_jobs, loom_nodes) come the same way, from this package's migrations/:
// `loom db install github.com/moomerman/loom` copies them, `loom new`
// does it for a new app, and Run refuses to start with a database that
// lacks them (RT433). The runtime never creates tables itself.

//go:embed migrations
var ownMigrations embed.FS

// Migrations returns Loom's own migrations, the files `loom db install
// github.com/moomerman/loom` copies into an app's migrations directory.
func Migrations() fs.FS {
	m, _ := fs.Sub(ownMigrations, "migrations")
	return m
}

// loomPackage is Loom's import path, as `loom db install` takes it.
var loomPackage = reflect.TypeOf(App{}).PkgPath()

// SubAppPackage returns the import path of a sub-app's package, as
// `loom db install` takes it.
func SubAppPackage(sub SubApp) string {
	t := reflect.TypeOf(sub)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.PkgPath()
}

// InstalledName is the name (without number) a sub-app migration file
// has once installed: "0001_users.sql" of package .../auth is
// "auth_users". A name that already starts with the package's
// ("0001_loom_clients.sql" of Loom) keeps it.
func InstalledName(pkg, file string) string {
	name := strings.TrimSuffix(file, ".sql")
	if i := strings.IndexByte(name, '_'); i >= 0 {
		name = name[i+1:]
	}
	if base := path.Base(pkg); !strings.HasPrefix(name, base+"_") {
		name = base + "_" + name
	}
	return name
}

// checkMigrations checks that Loom's own migrations, if the app has a
// database, and every mounted sub-app's are applied to it (RT433).
func (a *App) checkMigrations(ctx context.Context) error {
	hasDB := a.db != nil && (a.db.SQL() != nil || a.db.IsInTx())
	var applied map[string]bool
	check := func(pkg, who string, fsys fs.FS) error {
		migs, err := data.LoadMigrations(fsys)
		if err != nil {
			return fmt.Errorf("%s's migrations: %w", pkg, err)
		}
		if len(migs) == 0 {
			return nil
		}
		install := fmt.Sprintf("run `loom db install %s`, then `loom migrate`", pkg)
		if !hasDB {
			return &Error{Code: RT433, Msg: fmt.Sprintf("%s keeps data in the app's database, but the app has none", who),
				Fix: "give the app a Postgres database (loom.Config{DB: ...}, or app.UseDB in tests), then " + install}
		}
		if applied == nil {
			names, err := data.Select[string](ctx, a.db, `SELECT name FROM loom_migrations`)
			if err != nil && !strings.Contains(err.Error(), "loom_migrations") {
				return err
			}
			applied = map[string]bool{}
			for _, n := range names {
				applied[n] = true
			}
		}
		var missing []string
		for _, m := range migs {
			if !applied[InstalledName(pkg, m.File)] {
				missing = append(missing, m.File)
			}
		}
		if len(missing) > 0 {
			return &Error{Code: RT433, Msg: fmt.Sprintf("%s needs its migrations %s, which aren't applied to the app's database", who, strings.Join(missing, ", ")),
				Fix: install + "; the copies in migrations/ are the app's to commit"}
		}
		return nil
	}
	if hasDB {
		if err := check(loomPackage, "Loom (its tables loom_clients, loom_user_sessions, loom_rate_limits, loom_jobs and loom_nodes)", Migrations()); err != nil {
			return err
		}
	}
	for _, sub := range a.subMigrations {
		if err := check(SubAppPackage(sub), fmt.Sprintf("%T", sub), sub.Migrations()); err != nil {
			return err
		}
	}
	return nil
}
