package data_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/trace"
)

// An observed DB reports each transaction's end under the trace of the
// context it was given, and statements slower than its threshold.
func TestObserve(t *testing.T) {
	for name, db := range dbs(t) {
		t.Run(name, func(t *testing.T) {
			s := trace.NewStream()
			db.Observe(s, time.Nanosecond) // every statement is slow
			ctx := trace.WithTrace(context.Background(), "t1")
			pg := db.SQL() != nil
			db.Tx(ctx, func(tx data.Tx) error {
				if pg {
					tx.Exec(ctx, "SELECT 1")
				}
				return nil
			})
			db.Tx(ctx, func(tx data.Tx) error { return errors.New("no") })
			func() {
				defer func() { recover() }()
				db.Tx(ctx, func(tx data.Tx) error { panic("boom") })
			}()
			var got []string
			for _, e := range s.Since(0, trace.Filter{Trace: "t1"}) {
				got = append(got, e.Name+" "+e.String("statements")+e.String("reason"))
				if e.Name == trace.DBSlow && e.String("query") != "SELECT 1" {
					t.Errorf("db.slow query = %q", e.String("query"))
				}
			}
			want := "tx.commit 0 tx.rollback 0error tx.rollback 0panic"
			if pg {
				want = "db.slow  tx.commit 1 tx.rollback 0error tx.rollback 0panic"
			}
			if strings.Join(got, " ") != want {
				t.Errorf("events: %q\nwant %q", strings.Join(got, " "), want)
			}
		})
	}
}

// A panicking hook doesn't escape db.Tx: it is reported (DB624), and
// the hooks after it run.
func TestHookPanic(t *testing.T) {
	db := data.NewLocal()
	s := trace.NewStream()
	db.Observe(s, 0)
	ran := false
	err := db.Tx(context.Background(), func(tx data.Tx) error {
		tx.AfterCommit(func() { panic("broadcast failed") })
		tx.AfterCommit(func() { ran = true })
		return nil
	})
	if err != nil || !ran {
		t.Fatalf("Tx = %v, later hook ran %v", err, ran)
	}
	evs := s.Since(0, trace.Filter{Name: trace.Error})
	if len(evs) != 1 || evs[0].String("code") != data.DB624 || !strings.Contains(evs[0].String("msg"), "broadcast failed") {
		t.Fatalf("events: %+v", evs)
	}
}

func TestQueryStats(t *testing.T) {
	db := dbs(t)["postgres"]
	if db == nil {
		t.Skip("needs LOOM_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	for range 3 {
		var n int
		if err := db.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
			t.Fatal(err)
		}
	}
	err := db.Tx(ctx, func(tx data.Tx) error {
		rows, err := tx.Query(ctx, `SELECT g
			FROM generate_series(1, 4) g`)
		if err != nil {
			return err
		}
		for rows.Next() {
		}
		return rows.Close()
	})
	db.Exec(ctx, "SELECT nope")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]data.QueryStat{}
	for _, s := range db.QueryStats() {
		got[s.Query] = s
	}
	if s := got["SELECT 1"]; s.Count != 3 || s.Rows != 3 || s.Timed != 3 || s.LastMinute != 3 {
		t.Errorf("SELECT 1: %+v", s)
	}
	if s := got["SELECT g FROM generate_series(1, 4) g"]; s.Count != 1 || s.Rows != 4 {
		t.Errorf("series: %+v", s)
	}
	if s := got["SELECT nope"]; s.Count != 1 || s.Errors != 1 {
		t.Errorf("failed statement: %+v", s)
	}
	start := time.Now()
	db.Record(ctx, "SELECT from_a_store", start, 7, nil)
	for _, s := range db.QueryStats() {
		if s.Query == "SELECT from_a_store" && (s.Count != 1 || s.Rows != 7) {
			t.Errorf("recorded: %+v", s)
		}
	}
}

// Postgres's own counts show a table read by sequential scans of many
// rows, with a warning naming it.
func TestServerStats(t *testing.T) {
	db := dbs(t)["postgres"]
	if db == nil {
		t.Skip("needs LOOM_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	mustExec := func(q string) {
		t.Helper()
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`DROP TABLE IF EXISTS stats_scanned`)
	mustExec(`CREATE TABLE stats_scanned (id bigint PRIMARY KEY, n int)`)
	t.Cleanup(func() { db.Exec(ctx, `DROP TABLE stats_scanned`) })
	mustExec(`INSERT INTO stats_scanned SELECT g, g FROM generate_series(1, 3000) g`)
	for range 3 {
		mustExec(`SELECT count(*) FROM stats_scanned WHERE n = 7`) // n has no index
	}
	var got data.TableStat
	for range 50 { // Postgres publishes its counts a moment after each transaction
		mustExec(`SELECT pg_stat_force_next_flush()`)
		st, err := db.Stats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, tb := range st.Tables {
			if tb.Name == "stats_scanned" {
				got = tb
			}
		}
		if got.SeqScans >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got.SeqScans < 3 || got.SeqRows < 9000 || !strings.Contains(got.Warning, "stats_scanned has no index") {
		t.Errorf("stats_scanned: %+v", got)
	}
}
