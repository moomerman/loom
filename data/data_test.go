package data_test

import (
	"context"
	"errors"
	"os"
	"regexp"
	"testing"

	"github.com/moomerman/loom/data"
)

// dbs returns the DBs to test transactions on: a local DB, and Postgres
// when LOOM_TEST_DATABASE_URL is set.
func dbs(t *testing.T) map[string]*data.DB {
	out := map[string]*data.DB{"local": data.NewLocal()}
	if url := os.Getenv("LOOM_TEST_DATABASE_URL"); url != "" {
		db, err := data.Open(context.Background(), url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		out["postgres"] = db
	} else {
		t.Log("LOOM_TEST_DATABASE_URL is not set; testing the local DB only")
	}
	return out
}

func TestAfterCommit(t *testing.T) {
	ctx := context.Background()
	for name, db := range dbs(t) {
		t.Run(name, func(t *testing.T) {
			var got []string
			hooks := func(tx data.Tx, label string) {
				tx.AfterCommit(func() { got = append(got, label+" committed") })
				tx.AfterRollback(func() { got = append(got, label+" rolled back") })
			}

			err := db.Tx(ctx, func(tx data.Tx) error {
				hooks(tx, "ok")
				tx.AfterCommit(func() { got = append(got, "second hook") })
				if len(got) != 0 {
					t.Error("a hook ran before commit")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}

			boom := errors.New("boom")
			if err := db.Tx(ctx, func(tx data.Tx) error { hooks(tx, "error"); return boom }); err != boom {
				t.Fatalf("Tx returned %v, want f's error", err)
			}

			func() {
				defer func() {
					if p := recover(); p != "panic" {
						t.Errorf("recovered %v, want the panic to continue", p)
					}
				}()
				db.Tx(ctx, func(tx data.Tx) error { hooks(tx, "panic"); panic("panic") })
			}()

			want := []string{"ok committed", "second hook", "error rolled back", "panic rolled back"}
			if len(got) != len(want) {
				t.Fatalf("hooks: %q, want %q", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("hooks: %q, want %q", got, want)
				}
			}
		})
	}
}

func TestTxSQL(t *testing.T) {
	ctx := context.Background()
	for name, db := range dbs(t) {
		if name == "local" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS data_test (id int PRIMARY KEY)`); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Exec(ctx, `DROP TABLE data_test`) })
			db.Tx(ctx, func(tx data.Tx) error {
				tx.Exec(ctx, `INSERT INTO data_test VALUES (1)`)
				return errors.New("roll back")
			})
			func() {
				defer func() { recover() }()
				db.Tx(ctx, func(tx data.Tx) error {
					tx.Exec(ctx, `INSERT INTO data_test VALUES (2)`)
					panic("roll back")
				})
			}()
			err := db.Tx(ctx, func(tx data.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO data_test VALUES (3)`)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			var n, id int
			if err := db.QueryRow(ctx, `SELECT count(*), max(id) FROM data_test`).Scan(&n, &id); err != nil {
				t.Fatal(err)
			}
			if n != 1 || id != 3 {
				t.Fatalf("%d rows, max id %d; want only the committed row 3", n, id)
			}
		})
	}
}

func TestLocalDB(t *testing.T) {
	ctx := context.Background()
	db := data.NewLocal()
	if _, err := db.Exec(ctx, `SELECT 1`); !data.IsCode(err, data.DB602) {
		t.Errorf("Exec on a local DB: %v, want DB602", err)
	}
	var kept data.Tx
	db.Tx(ctx, func(tx data.Tx) error {
		kept = tx
		if err := tx.QueryRow(ctx, `SELECT 1`).Scan(); !data.IsCode(err, data.DB602) {
			t.Errorf("QueryRow in a local Tx: %v, want DB602", err)
		}
		if tx.DB() != db || tx.Context() != ctx {
			t.Error("wrong Tx fields")
		}
		return nil
	})
	if _, err := kept.Exec(ctx, `SELECT 1`); !data.IsCode(err, data.DB603) {
		t.Errorf("Exec after the transaction: %v, want DB603", err)
	}
	if _, err := data.Open(ctx, "postgres://nobody@127.0.0.1:1/none"); !data.IsCode(err, data.DB601) {
		t.Errorf("Open with nothing listening: %v, want DB601", err)
	}
	db.SetValue("k", 1)
	if db.Value("k") != 1 || db.Value("other") != nil {
		t.Error("values")
	}
}

// TestCodesDocumented keeps docs/errors.md in sync with the DB codes.
func TestCodesDocumented(t *testing.T) {
	doc, err := os.ReadFile("../docs/errors.md")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("data.go")
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^### (DB\d{3})$`).FindAllStringSubmatch(string(doc), -1) {
		documented[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`(?m)^\t(DB\d{3}) = "DB\d{3}"`).FindAllStringSubmatch(string(src), -1) {
		if !documented[m[1]] {
			t.Errorf("%s is not documented in docs/errors.md", m[1])
		}
		delete(documented, m[1])
	}
	for c := range documented {
		t.Errorf("docs/errors.md documents %s, which data.go doesn't define", c)
	}
}
