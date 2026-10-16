package data_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/loomtest"
)

// migrations writes migration files to a new directory.
func migrations(t *testing.T, files map[string]string) string {
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// emptyDB is a fresh database with nothing migrated.
func emptyDB(t *testing.T) *data.DB {
	return loomtest.DB(t, loomtest.Committed(), loomtest.Migrations(""))
}

func states(st *data.Status) string {
	var b []string
	for _, m := range st.Migrations {
		b = append(b, m.File+"="+m.State)
	}
	return strings.Join(b, " ")
}

func TestMigrate(t *testing.T) {
	ctx := context.Background()
	db := emptyDB(t)
	dir := migrations(t, map[string]string{
		"0001_create_lists.sql": "CREATE TABLE lists (id bigserial PRIMARY KEY, name text NOT NULL);\n",
		"0002_add_items.sql":    "-- items on lists\nCREATE TABLE items (id bigserial PRIMARY KEY, list_id bigint REFERENCES lists);\nCREATE INDEX ON items (list_id);\n",
		"README.md":             "not a migration",
	})
	fsys := os.DirFS(dir)

	st, err := data.MigrationStatus(ctx, db, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if states(st) != "0001_create_lists.sql=pending 0002_add_items.sql=pending" || st.Pending != 2 {
		t.Fatalf("before: %s", states(st))
	}

	applied, err := data.Migrate(ctx, db, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 || applied[0].Version != 1 || applied[1].Name != "add_items" {
		t.Fatalf("applied %+v", applied)
	}
	if applied, err := data.Migrate(ctx, db, fsys); err != nil || len(applied) != 0 {
		t.Fatalf("second run applied %v, %v; want nothing", applied, err)
	}

	// A new migration is pending; an edited one is a problem.
	os.WriteFile(filepath.Join(dir, "0003_more.sql"), []byte("ALTER TABLE lists ADD COLUMN archived boolean NOT NULL DEFAULT false;"), 0o644)
	os.WriteFile(filepath.Join(dir, "0001_create_lists.sql"), []byte("CREATE TABLE lists (id serial PRIMARY KEY);"), 0o644)
	st, err = data.MigrationStatus(ctx, db, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if states(st) != "0001_create_lists.sql=changed 0002_add_items.sql=applied 0003_more.sql=pending" || st.Pending != 1 || len(st.Problems) != 1 {
		t.Fatalf("after edits: %s, %d problems", states(st), len(st.Problems))
	}
	if st.Migrations[1].AppliedAt == nil {
		t.Error("no applied_at")
	}
	_, err = data.Migrate(ctx, db, fsys)
	wantCode(t, err, data.DB614, "0001_create_lists.sql was edited", "loom db new")
	if n, _ := data.Get[int](ctx, db, `SELECT count(*) FROM loom_migrations`); n != 2 {
		t.Fatalf("migrate applied %d migrations despite the edited one", n)
	}

	// A removed file shows as missing.
	os.Remove(filepath.Join(dir, "0002_add_items.sql"))
	st, _ = data.MigrationStatus(ctx, db, fsys)
	if !strings.Contains(states(st), "0002_add_items.sql=missing") {
		t.Fatalf("after removal: %s", states(st))
	}
}

func TestMigrationErrors(t *testing.T) {
	ctx := context.Background()
	load := func(files map[string]string) error {
		_, err := data.LoadMigrations(os.DirFS(migrations(t, files)))
		return err
	}
	wantCode(t, load(map[string]string{"1_init.sql": ""}), data.DB613, "1_init.sql", "loom db new")
	wantCode(t, load(map[string]string{"0001_Init.sql": ""}), data.DB613, "0001_Init.sql")
	wantCode(t, load(map[string]string{"0001_a.sql": "", "0001_b.sql": ""}), data.DB613, "same number 1")
	wantCode(t, load(map[string]string{"0001_a.sql": "\n-- loom:trak todos\n"}), data.DB616, "0001_a.sql:2", "loom:trak")
	wantCode(t, load(map[string]string{"0001_a.sql": "-- loom:track todos, items\n"}), data.DB616, "one table name")

	db := emptyDB(t)
	dir := migrations(t, map[string]string{
		"0001_ok.sql":   "CREATE TABLE ok (id int PRIMARY KEY);",
		"0002_bad.sql":  "CREATE TABLE fine (id int);\nCREATE TABEL broken (id int);\n",
		"0003_next.sql": "CREATE TABLE next (id int);",
	})
	_, err := data.Migrate(ctx, db, os.DirFS(dir))
	wantCode(t, err, data.DB615, "0002_bad.sql:2:8", `syntax error at or near "TABEL"`, "2 | CREATE TABEL broken", "nothing from this migration was applied")
	st, _ := data.MigrationStatus(ctx, db, os.DirFS(dir))
	if states(st) != "0001_ok.sql=applied 0002_bad.sql=pending 0003_next.sql=pending" {
		t.Fatalf("after the failure: %s", states(st))
	}
	if _, err := db.Exec(ctx, `SELECT 1 FROM fine`); !data.IsCode(err, data.DB609) {
		t.Errorf("the failed migration's first statement wasn't rolled back: %v", err)
	}

	_, err = data.Migrate(ctx, emptyDB(t), os.DirFS(migrations(t, map[string]string{"0001_t.sql": "-- loom:track nothing\n"})))
	wantCode(t, err, data.DB616, "0001_t.sql", "there is no table nothing")
	wantCode(t, load(map[string]string{"0001_a.sql": "-- loom:no-transaction now\n"}), data.DB616, "takes nothing after it")
	wantCode(t, load(map[string]string{"0001_a.sql": "-- loom:no-transaction\n-- loom:track a\n"}), data.DB616, "in the same migration")
}

// A migration waits LockTimeout for a lock, then fails, rather than
// queue behind a long transaction and block the table meanwhile; one
// marked no-transaction can build an index concurrently.
func TestMigrateLocks(t *testing.T) {
	ctx := context.Background()
	db := emptyDB(t)
	first := map[string]string{"0001_notes.sql": "CREATE TABLE notes (id int PRIMARY KEY, body text);\nINSERT INTO notes VALUES (1, 'a');\n"}
	if _, err := data.Migrate(ctx, db, os.DirFS(migrations(t, first))); err != nil {
		t.Fatal(err)
	}
	defer func(d time.Duration) { data.LockTimeout = d }(data.LockTimeout)
	data.LockTimeout = 200 * time.Millisecond
	long, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := long.ExecContext(ctx, `UPDATE notes SET body = 'b'`); err != nil {
		t.Fatal(err)
	}
	second := map[string]string{"0002_due.sql": "ALTER TABLE notes ADD COLUMN due date;\n"}
	for k, v := range first {
		second[k] = v
	}
	_, err = data.Migrate(ctx, db, os.DirFS(migrations(t, second)))
	wantCode(t, err, data.DB615, "0002_due.sql", "waited 200ms for a lock", "SET LOCAL lock_timeout")
	long.Rollback()
	second["0003_index.sql"] = "-- loom:no-transaction\nCREATE INDEX CONCURRENTLY notes_body ON notes (body);\n"
	applied, err := data.Migrate(ctx, db, os.DirFS(migrations(t, second)))
	if err != nil || len(applied) != 2 || !applied[1].NoTransaction {
		t.Fatalf("after the lock is released: %+v, %v", applied, err)
	}
}

// TestMigrateConcurrently migrates from several connections at once, as
// processes deploying together would: each migration runs once.
func TestMigrateConcurrently(t *testing.T) {
	ctx := context.Background()
	db := emptyDB(t)
	files := map[string]string{}
	for _, n := range []string{"0001_a", "0002_b", "0003_c", "0004_d"} {
		files[n+".sql"] = "CREATE TABLE t" + n[5:] + " (id int); SELECT pg_sleep(0.05);"
	}
	fsys := os.DirFS(migrations(t, files))
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			applied, err := data.Migrate(ctx, db, fsys)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			total += len(applied)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != 4 {
		t.Fatalf("applied %d migrations in total, want 4", total)
	}
}

const trackedMigration = `-- loom:track notes
CREATE TABLE notes (id bigserial PRIMARY KEY, body text NOT NULL);
CREATE TABLE plain (id int PRIMARY KEY);
CREATE TABLE pairs (a int, b text);
-- loom:track pairs
`

// TestTracking tracks tables, with and without a primary key, and
// brings a table tracked by an older Loom up to date: its version and
// delete triggers go, its columns stay.
func TestTracking(t *testing.T) {
	ctx := context.Background()
	db := emptyDB(t)
	if _, err := data.Migrate(ctx, db, os.DirFS(migrations(t, map[string]string{"0001_notes.sql": trackedMigration}))); err != nil {
		t.Fatal(err)
	}
	tracked, err := data.Tracked(ctx, db)
	if err != nil || strings.Join(tracked, ",") != "notes,pairs" {
		t.Fatalf("tracked: %v, %v", tracked, err)
	}
	exec(t, db, `CREATE TABLE old (id int PRIMARY KEY, version bigint);
CREATE FUNCTION loom_track_version() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.version := 1; RETURN NEW; END $$;
CREATE TRIGGER loom_track_version BEFORE INSERT ON old FOR EACH ROW EXECUTE FUNCTION loom_track_version()`)
	_, err = data.Migrate(ctx, db, os.DirFS(migrations(t, map[string]string{
		"0001_notes.sql": trackedMigration,
		"0002_old.sql":   "-- loom:track old\n",
	})))
	if err != nil {
		t.Fatal(err)
	}
	triggers, err := data.Select[string](ctx, db, `SELECT tgname FROM pg_trigger WHERE tgrelid = 'old'::regclass ORDER BY tgname`)
	if err != nil || strings.Join(triggers, ",") != "loom_track_notify" {
		t.Errorf("old's triggers: %v, %v", triggers, err)
	}

	// A migrate with nothing to do doesn't replace up-to-date triggers,
	// which would wait for, then block, writes to their tables.
	busy, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := busy.ExecContext(ctx, `INSERT INTO notes (body) VALUES ('busy')`); err != nil {
		t.Fatal(err)
	}
	fsys := os.DirFS(migrations(t, map[string]string{"0001_notes.sql": trackedMigration, "0002_old.sql": "-- loom:track old\n"}))
	done := make(chan error, 1)
	go func() { _, err := data.Migrate(ctx, db, fsys); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("migrate waited for a write to a tracked table")
	}
	busy.Rollback()
	// A table an older Loom tracked is brought up to date.
	exec(t, db, `CREATE TRIGGER loom_track_version BEFORE INSERT ON notes FOR EACH ROW EXECUTE FUNCTION loom_track_version()`)
	if _, err := data.Migrate(ctx, db, fsys); err != nil {
		t.Fatal(err)
	}
	if triggers, _ := data.Select[string](ctx, db, `SELECT tgname FROM pg_trigger WHERE tgrelid = 'notes'::regclass ORDER BY tgname`); strings.Join(triggers, ",") != "loom_track_notify" {
		t.Errorf("notes' triggers after retrack: %v", triggers)
	}
	exec(t, db, `INSERT INTO notes (body) VALUES ('one'); TRUNCATE pairs; INSERT INTO old VALUES (1, NULL)`)

	s, err := data.Schema(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tb := range s.Tables {
		got = append(got, tb.Name)
		if tb.Name == "notes" {
			if !tb.Tracked || len(tb.Columns) != 2 || tb.Columns[1].Nullable || !strings.Contains(tb.Columns[0].Default, "nextval") {
				t.Errorf("notes: %+v", tb)
			}
		}
		if tb.Name == "notes" && strings.Join(tb.PrimaryKey, ",") != "id" {
			t.Errorf("notes key: %v", tb.PrimaryKey)
		}
		if tb.Name == "plain" && tb.Tracked {
			t.Error("plain is tracked")
		}
	}
	if strings.Join(got, ",") != "notes,old,pairs,plain" {
		t.Errorf("tables: %v (loom_ tables are left out)", got)
	}
}
