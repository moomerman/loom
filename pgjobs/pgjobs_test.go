package pgjobs_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/jobs/jobstest"
	"github.com/moomerman/loom/pgjobs"
)

// The conformance suite needs a Postgres database it may empty, named by
// LOOM_TEST_DATABASE_URL, e.g.
// postgres://postgres:postgres@localhost:5432/loom_test.
func openDB(t *testing.T) *sql.DB {
	url := os.Getenv("LOOM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LOOM_TEST_DATABASE_URL to a database URL to run the Postgres tests")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestConformance(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	// loom_jobs comes from Loom's own migrations, as in an app.
	if _, err := data.Migrate(ctx, data.FromSQL(db), os.DirFS("../migrations")); err != nil {
		t.Fatal(err)
	}
	jobstest.Run(t, func(t *testing.T, clock jobs.Clock) func() jobs.Store {
		if _, err := db.ExecContext(ctx, `TRUNCATE loom_jobs, loom_nodes RESTART IDENTITY`); err != nil {
			t.Fatal(err)
		}
		return func() jobs.Store {
			s := pgjobs.New(data.FromSQL(db), pgjobs.Options{Clock: clock}) // one DB value per node
			t.Cleanup(func() { s.Close() })
			return s
		}
	})
}
