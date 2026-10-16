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
