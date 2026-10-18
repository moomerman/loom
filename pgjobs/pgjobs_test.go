package pgjobs_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/jobs/jobstest"
	"github.com/moomerman/loom/pgjobs"
	"github.com/moomerman/loom/trace"
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

// The store runs its SQL on database/sql's own types, but counts each
// statement in the DB's query stats and reports slow ones, as app
// queries are: the runner's polling must be visible in `loom queries`.
func TestStoreQueriesAreObserved(t *testing.T) {
	sdb := openDB(t)
	ctx := context.Background()
	if _, err := data.Migrate(ctx, data.FromSQL(sdb), os.DirFS("../migrations")); err != nil {
		t.Fatal(err)
	}
	db := data.FromSQL(sdb)
	stream := trace.NewStream()
	db.Observe(stream, time.Nanosecond) // every statement is slow
	s := pgjobs.New(db, pgjobs.Options{})
	defer s.Close()
	now := time.Now()
	err := db.Tx(ctx, func(tx data.Tx) error {
		_, err := s.Insert(tx, jobs.NewJob{Type: "t.Observed", Queue: "observed", Args: []byte(`{}`), MaxAttempts: 1, InsertedAt: now, ScheduledAt: now})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	recs, err := s.Fetch(ctx, jobs.FetchRequest{Queue: "observed", Limit: 5, Now: now, Node: "n", LeaseUntil: now.Add(time.Minute)})
	if err != nil || len(recs) != 1 {
		t.Fatalf("fetch: %v, %d jobs", err, len(recs))
	}
	fetch := false
	for _, q := range db.QueryStats() {
		if strings.HasPrefix(q.Query, "UPDATE loom_jobs SET state = 'executing'") && q.Count == 1 {
			fetch = true
		}
	}
	if !fetch {
		t.Errorf("no fetch in the query stats: %+v", db.QueryStats())
	}
	slow := false
	for _, e := range stream.Since(0, trace.Filter{Name: trace.DBSlow}) {
		if strings.HasPrefix(e.String("query"), "UPDATE loom_jobs SET state = 'executing'") {
			slow = true
		}
	}
	if !slow {
		t.Error("the fetch wasn't reported as db.slow")
	}
	if _, err := sdb.ExecContext(ctx, `DELETE FROM loom_jobs WHERE queue = 'observed'`); err != nil {
		t.Fatal(err)
	}
}
