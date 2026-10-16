package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/data"
)

// testDatabase creates an empty database for a test and returns its URL.
func testDatabase(t *testing.T) string {
	t.Helper()
	admin := os.Getenv("LOOM_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("set LOOM_TEST_DATABASE_URL to run the database command tests")
	}
	a, err := sql.Open("pgx", admin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b := make([]byte, 6)
	rand.Read(b)
	name := "loom_cli_" + hex.EncodeToString(b)
	if _, err := a.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`) })
	u, _ := url.Parse(admin)
	u.Path = "/" + name
	return u.String()
}

// cli runs a database command and returns its exit code and output.
func cli(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runWith(args, &out, &errOut)
	return code, out.String() + errOut.String()
}

func runWith(args []string, stdout, stderr *bytes.Buffer) int {
	switch args[0] {
	case "db":
		return runDB(args[1:], stdout, stderr)
	case "migrate":
		return runMigrate(args[1:], stdout, stderr)
	}
	panic(args[0])
}

func TestDBNew(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "migrations")
	code, out := cli(t, "db", "new", "--dir", dir, "create_todos")
	if code != 0 || !strings.Contains(out, "created "+filepath.Join(dir, "0001_create_todos.sql")) {
		t.Fatalf("exit %d: %s", code, out)
	}
	code, out = cli(t, "db", "new", "--json", "--dir", dir, "add_due_dates")
	var res NewMigration
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || res.Version != 2 || res.File != filepath.Join(dir, "0002_add_due_dates.sql") {
		t.Fatalf("exit %d: %s", code, out)
	}
	migs, err := data.LoadMigrations(os.DirFS(dir))
	if err != nil || len(migs) != 2 || len(migs[0].Track) != 0 {
		t.Fatalf("the new files don't load cleanly: %v %v", migs, err)
	}
	if code, out := cli(t, "db", "new", "--dir", dir, "Bad-Name"); code != 2 || !strings.Contains(out, "lowercase letters") {
		t.Errorf("bad name: exit %d: %s", code, out)
	}
	if code, out := cli(t, "migrate", "status", "--db", ""); code != 2 || !strings.Contains(out, "set DATABASE_URL") {
		t.Errorf("no database: exit %d: %s", code, out)
	}
}

func TestMigrateCommands(t *testing.T) {
	db := testDatabase(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "0001_create_todos.sql"), []byte("-- loom:track todos\nCREATE TABLE todos (id bigserial PRIMARY KEY, title text NOT NULL, done boolean NOT NULL DEFAULT false);\nINSERT INTO todos (title) VALUES ('Try Loom');\n"), 0o644)

	code, out := cli(t, "migrate", "status", "--db", db, "--dir", dir)
	if code != 0 || !strings.Contains(out, "0001_create_todos.sql  pending") || !strings.Contains(out, "1 pending") {
		t.Fatalf("status before: exit %d:\n%s", code, out)
	}
	code, out = cli(t, "migrate", "--json", "--db", db, "--dir", dir)
	var res loom.MigrateResult
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || len(res.Applied) != 1 || res.Applied[0].File != "0001_create_todos.sql" {
		t.Fatalf("migrate: exit %d:\n%s", code, out)
	}
	if code, out := cli(t, "migrate", "--db", db, "--dir", dir); code != 0 || out != "no pending migrations\n" {
		t.Fatalf("migrate again: exit %d:\n%s", code, out)
	}

	code, out = cli(t, "db", "schema", "--db", db)
	if code != 0 || !strings.Contains(out, "todos (tracked)") || !strings.Contains(out, "title  text") || !strings.Contains(out, "primary key, not null, default nextval") {
		t.Fatalf("schema: exit %d:\n%s", code, out)
	}
	code, out = cli(t, "db", "schema", "--json", "--db", db)
	var s data.SchemaInfo
	if code != 0 || json.Unmarshal([]byte(out), &s) != nil || len(s.Tables) != 1 || !s.Tables[0].Tracked || len(s.Tables[0].Columns) != 3 {
		t.Fatalf("schema --json: exit %d:\n%s", code, out)
	}

	code, out = cli(t, "db", "query", "--db", db, "SELECT title, done FROM todos")
	if code != 0 || !strings.Contains(out, "TITLE     DONE") || !strings.Contains(out, "Try Loom  false") || !strings.Contains(out, "(1 row)") {
		t.Fatalf("query: exit %d:\n%s", code, out)
	}
	code, out = cli(t, "db", "query", "--json", "--db", db, "DELETE FROM todos")
	if code != 2 || !strings.Contains(out, "read-only") {
		t.Fatalf("a write: exit %d:\n%s", code, out)
	}
	code, out = cli(t, "db", "query", "--json", "--db", db, "SELECT * FROM nope")
	if code != 1 || !strings.Contains(out, `"code": "DB609"`) {
		t.Fatalf("a missing table: exit %d:\n%s", code, out)
	}

	// Edit the applied migration: status reports it and exits 1, migrate refuses.
	os.WriteFile(filepath.Join(dir, "0001_create_todos.sql"), []byte("CREATE TABLE todos (id int);"), 0o644)
	code, out = cli(t, "migrate", "status", "--json", "--db", db, "--dir", dir)
	var st data.Status
	if code != 1 || json.Unmarshal([]byte(out), &st) != nil || st.Migrations[0].State != "changed" || st.Problems[0].Code != "DB614" {
		t.Fatalf("status after an edit: exit %d:\n%s", code, out)
	}
	if code, out := cli(t, "migrate", "--db", db, "--dir", dir); code != 1 || !strings.Contains(out, "error DB614") {
		t.Fatalf("migrate after an edit: exit %d:\n%s", code, out)
	}
}

func TestMCPDatabaseTools(t *testing.T) {
	db := testDatabase(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "0001_t.sql"), []byte("CREATE TABLE t (id int PRIMARY KEY);"), 0o644)
	var in bytes.Buffer
	for _, line := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"migrate_status","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"db_schema","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"db_query","arguments":{"sql":"SELECT 1 AS one","limit":5}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"db_query","arguments":{"sql":"SELECT * FROM t"}}}`,
	} {
		in.WriteString(line + "\n")
	}
	var out, errOut bytes.Buffer
	if code := runMCP([]string{"--db", db, "--migrations", dir}, &in, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var texts []string
	dec := json.NewDecoder(&out)
	for dec.More() {
		var m struct {
			Result struct {
				Content []struct{ Text string }
				IsError bool
			}
		}
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, m.Result.Content[0].Text)
	}
	for i, want := range []string{`"state":"pending"`, `"tables":[]`, `"rows":[[1]]`, `"code":"DB609"`} {
		if !strings.Contains(texts[i], want) {
			t.Errorf("call %d: %s, want %s", i+1, texts[i], want)
		}
	}
}

func TestDevMigrate(t *testing.T) {
	db := testDatabase(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "0001_t.sql")
	os.WriteFile(file, []byte("CREATE TABLE t (id int PRIMARY KEY);"), 0o644)
	if _, ok := snapshot(nil, dir)[file]; !ok {
		t.Fatal("loom dev doesn't watch migration files")
	}
	var out bytes.Buffer
	d := &devServer{dbURL: db, migrations: dir, asJSON: true, out: &out}
	if err := d.migrate(); err != nil {
		t.Fatal(err)
	}
	var e devEvent
	if err := json.Unmarshal(out.Bytes(), &e); err != nil || e.Event != "migrated" || len(e.Applied) != 1 || e.Applied[0] != file {
		t.Fatalf("event %s", out.String())
	}
	out.Reset()
	os.WriteFile(filepath.Join(dir, "0002_bad.sql"), []byte("CREATE TABLE t (id int);"), 0o644)
	if err := d.migrate(); !data.IsCode(err, data.DB615) || out.Len() != 0 {
		t.Fatalf("a failing migration: %v, events %s", err, out.String())
	}
	d.dbURL = ""
	if err := d.migrate(); err != nil {
		t.Fatalf("without a database, loom dev skips migrations: %v", err)
	}
}

// TestDBInstall copies a sub-app's migrations into the app's, after its
// own, once; loom migrate then applies them like the app's.
func TestDBInstall(t *testing.T) {
	const pkg = "github.com/moomerman/loom/internal/notestest"
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "0001_create_todos.sql"), []byte("CREATE TABLE todos (id bigserial PRIMARY KEY);\n"), 0o644)

	code, out := cli(t, "db", "install", "--json", "--dir", dir, pkg)
	var res Installed
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || len(res.Created) != 2 ||
		res.Created[0].Version != 2 || res.Created[0].Name != "notestest_notes" || res.Created[1].Name != "notestest_tags" {
		t.Fatalf("install: exit %d:\n%s", code, out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "0002_notestest_notes.sql"))
	if !strings.HasPrefix(string(b), "-- loom:from "+pkg+" 0001_notes.sql\n") || !strings.Contains(string(b), "CREATE TABLE notestest_notes") {
		t.Errorf("0002_notestest_notes.sql:\n%s", b)
	}
	if code, out := cli(t, "db", "install", "--dir", dir, pkg); code != 0 || !strings.Contains(out, "all installed") {
		t.Fatalf("install again: exit %d:\n%s", code, out)
	}
	if code, out := cli(t, "db", "install", "--dir", dir, "github.com/moomerman/loom/pubsub"); code != 2 || !strings.Contains(out, "no migrations") {
		t.Errorf("a package without migrations: exit %d:\n%s", code, out)
	}

	db := testDatabase(t)
	code, out = cli(t, "migrate", "status", "--db", db, "--dir", dir)
	if code != 0 || !strings.Contains(out, "0003_notestest_tags.sql   pending") {
		t.Fatalf("status: exit %d:\n%s", code, out)
	}
	if code, out := cli(t, "migrate", "--db", db, "--dir", dir); code != 0 || !strings.Contains(out, "0002_notestest_notes.sql") {
		t.Fatalf("migrate: exit %d:\n%s", code, out)
	}
}
