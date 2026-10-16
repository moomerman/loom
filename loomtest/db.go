package loomtest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver
	"github.com/moomerman/loom/data"
)

// DBOption changes how DB sets up a database.
type DBOption func(*dbOptions)

type dbOptions struct {
	committed bool
	dir       string
	dirSet    bool
}

// Committed gives the test a database of its own, where transactions
// really commit: for AfterCommit hooks, LISTEN/NOTIFY and the Postgres
// job store. It is a fresh copy of the migrated template, dropped when
// the test ends.
func Committed() DBOption { return func(o *dbOptions) { o.committed = true } }

// Migrations names the directory of migrations the database is built
// from. The default is the nearest directory named migrations in the
// test's package directory or above it, up to the module root; with none
// the database starts empty.
func Migrations(dir string) DBOption { return func(o *dbOptions) { o.dir, o.dirSet = dir, true } }

// DB returns a migrated Postgres database for a test.
//
//	db := loomtest.DB(t)
//	app := loom.New()
//	app.UseDB(db)
//
// By default everything the test does runs inside one transaction, rolled
// back when it ends, so tests are fast and leave nothing behind; db.Tx
// makes a savepoint, and AfterCommit hooks run when it is released. Jobs
// enqueued in it go to the in-memory store, so loomtest.Jobs works (pass
// the DB to it with UseDB). Use the DB from one goroutine at a time;
// tests that need real commits or concurrency use Committed.
//
// Postgres is found through LOOM_TEST_DATABASE_URL, whose user must be
// able to create databases; without it the test is skipped. Databases
// are built once from the migrations (named loomtest_<hash of the
// migrations>) and cloned from that template.
func DB(t testing.TB, opts ...DBOption) *data.DB {
	t.Helper()
	admin := os.Getenv("LOOM_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("loomtest.DB: set LOOM_TEST_DATABASE_URL to run tests that need Postgres, e.g. LOOM_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/loom_test (its user must be able to create databases)")
	}
	var o dbOptions
	for _, opt := range opts {
		opt(&o)
	}
	if !o.dirSet {
		o.dir = findMigrations()
	}
	ctx := context.Background()
	tpl, err := template(ctx, admin, o.dir)
	if err != nil {
		t.Fatalf("loomtest.DB: %v", err)
	}
	if o.committed {
		name := tpl + "_" + randomHex(8)
		if err := clone(ctx, admin, tpl, name); err != nil {
			t.Fatalf("loomtest.DB: %v", err)
		}
		db, err := data.Open(ctx, withDB(admin, name))
		if err != nil {
			t.Fatalf("loomtest.DB: %v", err)
		}
		t.Cleanup(func() {
			db.Close()
			if err := withAdmin(ctx, admin, func(a *sql.DB) error {
				_, err := a.ExecContext(ctx, `DROP DATABASE IF EXISTS `+quote(name)+` WITH (FORCE)`)
				return err
			}); err != nil {
				t.Errorf("loomtest.DB: dropping %s: %v", name, err)
			}
		})
		return db
	}
	pool, err := sharedPool(ctx, admin, tpl)
	if err != nil {
		t.Fatalf("loomtest.DB: %v", err)
	}
	stx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("loomtest.DB: %v", err)
	}
	t.Cleanup(func() { stx.Rollback() })
	return data.InTx(stx)
}

// findMigrations returns the nearest migrations directory at or above
// the working directory, stopping at the module root.
func findMigrations() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		m := filepath.Join(dir, "migrations")
		if info, err := os.Stat(m); err == nil && info.IsDir() {
			return m
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// templateVersion is part of template names; change it when Loom changes
// what migrating creates (such as tracking triggers), so old templates
// aren't reused.
const templateVersion = "loomtest 2\x00"

var (
	templates sync.Map // dir -> template name, once built in this process
	pools     sync.Map // template name -> *sql.DB of its shared rollback database
	poolMu    sync.Mutex
)

// template returns the name of the template database for the migrations
// in dir, building it if no process has.
func template(ctx context.Context, admin, dir string) (string, error) {
	if name, ok := templates.Load(dir); ok {
		return name.(string), nil
	}
	var fsys fs.FS = emptyFS{}
	if dir != "" {
		fsys = os.DirFS(dir)
	}
	migs, err := data.LoadMigrations(fsys)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(templateVersion))
	for _, m := range migs {
		fmt.Fprintf(h, "%s\x00%s\x00", m.File, m.Checksum)
	}
	name := "loomtest_" + hex.EncodeToString(h.Sum(nil))[:12]
	err = locked(ctx, admin, name, func(a *sql.DB) error {
		if ok, err := dbExists(ctx, a, name); err != nil || ok {
			return err
		}
		building := name + "_building"
		if _, err := a.ExecContext(ctx, `DROP DATABASE IF EXISTS `+quote(building)+` WITH (FORCE)`); err != nil {
			return err
		}
		if _, err := a.ExecContext(ctx, `CREATE DATABASE `+quote(building)); err != nil {
			return fmt.Errorf("creating a test database (does the user of LOOM_TEST_DATABASE_URL have CREATEDB?): %w", err)
		}
		db, err := data.Open(ctx, withDB(admin, building))
		if err != nil {
			return err
		}
		_, err = data.Migrate(ctx, db, fsys)
		db.Close()
		if err != nil {
			return fmt.Errorf("migrating the test template from %s: %w", dir, err)
		}
		_, err = a.ExecContext(ctx, `ALTER DATABASE `+quote(building)+` RENAME TO `+quote(name))
		return err
	})
	if err != nil {
		return "", err
	}
	templates.Store(dir, name)
	return name, nil
}

// sharedPool returns a pool on the template's shared rollback database,
// which every default-mode test of these migrations uses inside its own
// transaction.
func sharedPool(ctx context.Context, admin, tpl string) (*sql.DB, error) {
	poolMu.Lock()
	defer poolMu.Unlock()
	if p, ok := pools.Load(tpl); ok {
		return p.(*sql.DB), nil
	}
	name := tpl + "_tx"
	if err := clone(ctx, admin, tpl, name); err != nil {
		return nil, err
	}
	db, err := data.Open(ctx, withDB(admin, name))
	if err != nil {
		return nil, err
	}
	pools.Store(tpl, db.SQL())
	return db.SQL(), nil
}

// clone creates database name from template tpl, unless it exists.
// Clones of a template are serialized, since Postgres refuses to copy a
// database while anything else uses it.
func clone(ctx context.Context, admin, tpl, name string) error {
	return locked(ctx, admin, tpl, func(a *sql.DB) error {
		if ok, err := dbExists(ctx, a, name); err != nil || ok {
			return err
		}
		_, err := a.ExecContext(ctx, `CREATE DATABASE `+quote(name)+` TEMPLATE `+quote(tpl))
		return err
	})
}

// locked runs f on the admin database holding an advisory lock on key,
// so processes running tests at once take turns.
func locked(ctx context.Context, admin, key string, f func(a *sql.DB) error) error {
	return withAdmin(ctx, admin, func(a *sql.DB) error {
		a.SetMaxOpenConns(2)
		conn, err := a.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		h := fnv.New64a()
		h.Write([]byte("loomtest:" + key))
		lock := int64(h.Sum64())
		if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lock); err != nil {
			return err
		}
		defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lock)
		return f(a)
	})
}

func withAdmin(ctx context.Context, admin string, f func(a *sql.DB) error) error {
	a, err := sql.Open("pgx", admin)
	if err != nil {
		return err
	}
	defer a.Close()
	return f(a)
}

func dbExists(ctx context.Context, a *sql.DB, name string) (bool, error) {
	var ok bool
	err := a.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&ok)
	return ok, err
}

// withDB returns the URL with its database name replaced.
func withDB(u, name string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	p.Path = "/" + name
	return p.String()
}

func quote(ident string) string { return `"` + ident + `"` }

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// emptyFS is a directory with no files.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}
