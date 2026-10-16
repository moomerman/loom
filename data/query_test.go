package data_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/loomtest"
)

func exec(t *testing.T, q data.Querier, query string, args ...any) {
	t.Helper()
	if _, err := q.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// wantCode fails unless err is a data error with the code and its message
// contains each of the parts.
func wantCode(t *testing.T, err error, code string, parts ...string) {
	t.Helper()
	if !data.IsCode(err, code) {
		t.Fatalf("got %v, want %s", err, code)
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("%s error doesn't mention %q:\n%v", code, p, err)
		}
	}
}

type Audit struct {
	CreatedBy string `db:"created_by"`
}

type Person struct {
	ID       int64
	FullName string
	Nickname *string
	HTMLBio  string    `db:"bio"`
	JoinedAt time.Time // joined_at
	Secret   string    `db:"-"`
	//lint:ignore U1000 an unexported field is never a column, which the test checks
	unchanged int
	Audit
}

func setupPeople(t *testing.T) *data.DB {
	db := loomtest.DB(t)
	exec(t, db, `CREATE TABLE people (id bigint PRIMARY KEY, full_name text NOT NULL, nickname text, bio text NOT NULL DEFAULT '', joined_at timestamptz NOT NULL DEFAULT now(), created_by text NOT NULL DEFAULT 'test')`)
	exec(t, db, `INSERT INTO people (id, full_name, nickname) VALUES (1, 'Ada Lovelace', 'Ada'), (2, 'Grace Hopper', NULL)`)
	return db
}

func TestSelectAndGet(t *testing.T) {
	ctx := context.Background()
	db := setupPeople(t)

	people, err := data.Select[Person](ctx, db, `SELECT * FROM people ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 2 || people[0].FullName != "Ada Lovelace" || *people[0].Nickname != "Ada" || people[1].Nickname != nil || people[0].CreatedBy != "test" || people[0].JoinedAt.IsZero() {
		t.Fatalf("people: %+v", people)
	}

	none, err := data.Select[Person](ctx, db, `SELECT * FROM people WHERE id < 0`)
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("no rows: %v, %v; want an empty, non-nil slice", none, err)
	}

	ids, err := data.Select[int64](ctx, db, `SELECT id FROM people ORDER BY id`)
	if err != nil || len(ids) != 2 || ids[1] != 2 {
		t.Fatalf("ids: %v, %v", ids, err)
	}

	err = db.Tx(ctx, func(tx data.Tx) error {
		p, err := data.Get[Person](ctx, tx, `SELECT * FROM people WHERE id = $1`, 2)
		if err != nil || p.FullName != "Grace Hopper" {
			t.Errorf("Get in a Tx: %+v, %v", p, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = data.Get[Person](ctx, db, `SELECT * FROM people WHERE id = $1`, 3)
	if !errors.Is(err, data.ErrNotFound) {
		t.Fatalf("Get of a missing row: %v, want ErrNotFound", err)
	}
	wantCode(t, err, data.DB604, "WHERE id = $1 [3]")

	_, err = data.Get[Person](ctx, db, `SELECT * FROM people`)
	wantCode(t, err, data.DB605, "LIMIT 1")

	n, err := data.Get[int](ctx, db, `SELECT count(*) FROM people`)
	if err != nil || n != 2 {
		t.Fatalf("count: %d, %v", n, err)
	}
}

func TestScanErrors(t *testing.T) {
	ctx := context.Background()
	db := setupPeople(t)

	type Short struct{ ID int64 }
	_, err := data.Select[Short](ctx, db, `SELECT id, full_name FROM people`)
	wantCode(t, err, data.DB610, `column "full_name"`, "data_test.Short", "FullName", `db:"full_name"`)

	type NoNulls struct {
		ID       int64
		Nickname string
	}
	_, err = data.Select[NoNulls](ctx, db, `SELECT id, nickname FROM people ORDER BY id`)
	wantCode(t, err, data.DB611, `column "nickname"`, "NoNulls.Nickname", "*string")

	type WrongType struct {
		FullName int
	}
	_, err = data.Select[WrongType](ctx, db, `SELECT full_name FROM people`)
	wantCode(t, err, data.DB611, `column "full_name"`, "WrongType.FullName (int)")

	_, err = data.Select[int64](ctx, db, `SELECT id, full_name FROM people`)
	wantCode(t, err, data.DB612, "2 columns", "int64")

	_, err = data.Select[Short](ctx, db, `SELECT id, id FROM people`)
	wantCode(t, err, data.DB612, `column "id" twice`)
}

func TestMappedErrors(t *testing.T) {
	ctx := context.Background()
	db := loomtest.DB(t)
	exec(t, db, `CREATE TABLE lists (id int PRIMARY KEY, name text NOT NULL UNIQUE)`)
	exec(t, db, `CREATE TABLE items (id int PRIMARY KEY, list_id int NOT NULL REFERENCES lists)`)
	exec(t, db, `INSERT INTO lists VALUES (1, 'home')`)

	_, err := db.Exec(ctx, `INSERT INTO lists VALUES (2, 'home')`)
	if !errors.Is(err, data.ErrUniqueViolation) {
		t.Fatalf("duplicate: %v", err)
	}
	wantCode(t, err, data.DB606, "lists_name_key", "Key (name)=(home) already exists", "ON CONFLICT")
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23505" {
		t.Errorf("the driver's error isn't reachable with errors.As: %v", err)
	}

	err = db.Tx(ctx, func(tx data.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO items VALUES (1, 99)`)
		return err
	})
	if !errors.Is(err, data.ErrForeignKeyViolation) {
		t.Fatalf("bad reference: %v", err)
	}
	wantCode(t, err, data.DB607, "items_list_id_fkey")

	_, err = db.Exec(ctx, `INSERT INTO lists (id) VALUES (3)`)
	wantCode(t, err, data.DB608, `column "name" of lists`)

	err = db.QueryRow(ctx, `SELECT * FROM nope`).Scan()
	wantCode(t, err, data.DB609, `relation "nope" does not exist`, "loom migrate")

	_, err = data.Select[struct{ Name string }](ctx, db, `SELECT nmae FROM lists`)
	wantCode(t, err, data.DB618, `column "nmae" does not exist`, "SELECT nmae FROM lists\n           ^", `Perhaps you meant to reference the column "lists.name"`)

	_, err = db.Exec(ctx, "UPDATE lists\n  SET name = ?\n  WHERE id = 1", "x")
	wantCode(t, err, data.DB619, `syntax error at or near "WHERE"`, "    WHERE id = 1\n    ^", "(not ?)")

	_, err = db.Exec(ctx, `UPDATE lists SET id = true WHERE id = 1`)
	wantCode(t, err, data.DB620, `column "id" is of type integer but expression is of type boolean`, "$1::bigint")

	_, err = data.Select[struct{ ID int }](ctx, db, `SELECT id FROM lists WHERE name = 1::int`)
	wantCode(t, err, data.DB621, `operator does not exist: text = integer`, "WHERE name = 1::int\n", "cast")

	// Failed statements didn't abort the test's transaction.
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM lists`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("after the errors: %d lists, %v", n, err)
	}
	if err := db.QueryRow(ctx, `SELECT 1 WHERE false`).Scan(&n); err != sql.ErrNoRows {
		t.Errorf("QueryRow with no row: %v, want sql.ErrNoRows", err)
	}
}

// TestInTx checks the default loomtest.DB: Tx makes savepoints, hooks
// follow them, and nothing outlives the test.
func TestInTx(t *testing.T) {
	ctx := context.Background()
	db := loomtest.DB(t)
	if !db.IsInTx() || db.SQL() != nil {
		t.Fatal("loomtest.DB should be an InTx DB with no pool")
	}
	exec(t, db, `CREATE TABLE intx (id int PRIMARY KEY)`)
	var hooks []string
	db.Tx(ctx, func(tx data.Tx) error {
		tx.AfterRollback(func() { hooks = append(hooks, "rolled back") })
		tx.Exec(ctx, `INSERT INTO intx VALUES (1)`)
		return errors.New("no")
	})
	err := db.Tx(ctx, func(tx data.Tx) error {
		tx.AfterCommit(func() { hooks = append(hooks, "committed") })
		if _, err := tx.Exec(ctx, `INSERT INTO intx VALUES (2)`); err != nil {
			return err
		}
		// A failed nested Tx rolls back only its own savepoint.
		err := db.Tx(ctx, func(tx data.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO intx VALUES (2)`)
			return err
		})
		if !errors.Is(err, data.ErrUniqueViolation) {
			t.Errorf("nested duplicate: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := data.Select[int](ctx, db, `SELECT id FROM intx`)
	if err != nil || len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("ids %v, %v; want [2]", ids, err)
	}
	if strings.Join(hooks, ",") != "rolled back,committed" {
		t.Errorf("hooks: %q", hooks)
	}
	t.Run("isolated", func(t *testing.T) {
		other := loomtest.DB(t)
		if _, err := other.Exec(ctx, `SELECT 1 FROM intx`); !data.IsCode(err, data.DB609) {
			t.Errorf("another test's DB sees this test's table: %v", err)
		}
	})
}

func TestSnakeCase(t *testing.T) {
	for in, want := range map[string]string{"ID": "id", "UserID": "user_id", "HTMLBody": "html_body", "DoneAt": "done_at", "Version2": "version2", "Name": "name", "APIKeyID": "api_key_id"} {
		if got := data.SnakeCase(in); got != want {
			t.Errorf("SnakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQueryReadOnly(t *testing.T) {
	ctx := context.Background()
	db := loomtest.DB(t, loomtest.Committed())
	exec(t, db, `CREATE TABLE ro (id int, note text)`)
	exec(t, db, `INSERT INTO ro VALUES (1, 'a'), (2, NULL), (3, 'c')`)
	res, err := data.QueryReadOnly(ctx, db, `SELECT id, note FROM ro ORDER BY id`, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Columns, ",") != "id,note" || len(res.Rows) != 2 || !res.Truncated || res.Rows[1][1] != nil || res.Rows[0][1] != "a" {
		t.Fatalf("result: %+v", res)
	}
	if _, err := data.QueryReadOnly(ctx, db, `DELETE FROM ro`, 10); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("a write in QueryReadOnly: %v", err)
	}
	if n, _ := data.Get[int](ctx, db, `SELECT count(*) FROM ro`); n != 3 {
		t.Fatalf("%d rows left", n)
	}
}
