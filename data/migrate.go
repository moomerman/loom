package data

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Migrations are plain SQL files named NNNN_name.sql (0001_create_todos.sql),
// applied in order of their number, each in its own transaction, and
// recorded with a checksum in the loom_migrations table. They are
// forward-only: editing one that was applied is an error (DB614). A
// line `-- loom:track <table>` in a migration turns on change tracking
// for that table once the migration's SQL has run (see Track).

// Migration is one migration file.
type Migration struct {
	Version  int64    `json:"version"`
	Name     string   `json:"name"`
	File     string   `json:"file"`
	Checksum string   `json:"checksum"` // sha256 of the file, in hex
	Track    []string `json:"track,omitempty"`
	From     string   `json:"from,omitempty"` // "<package> <file>": installed from a sub-app by `loom db install`
	// NoTransaction is set by `-- loom:no-transaction`: the migration runs
	// outside a transaction, for statements that can't run in one, such
	// as CREATE INDEX CONCURRENTLY.
	NoTransaction bool   `json:"no_transaction,omitempty"`
	SQL           string `json:"-"`
}

// MigrationState is a migration with its state in a database.
type MigrationState struct {
	Migration
	// State is "applied", "pending", "changed" (applied, then edited: its
	// checksum no longer matches) or "missing" (applied, but its file is
	// gone).
	State     string     `json:"state"`
	AppliedAt *time.Time `json:"applied_at,omitempty"`
}

// Status is the state of a database's migrations: what `loom migrate
// status` prints.
type Status struct {
	Migrations []MigrationState `json:"migrations"`
	Pending    int              `json:"pending"`
	// Problems are errors that stop Migrate: edited migrations (DB614).
	Problems []*Error `json:"problems"`
}

// Applied is a migration Migrate applied.
type Applied struct {
	Migration
	Ms int64 `json:"ms"`
}

var (
	migrationName = regexp.MustCompile(`^(\d{4,})_([a-z0-9_]+)\.sql$`)
	annotation    = regexp.MustCompile(`(?m)^[ \t]*--[ \t]*loom:(\S*)[ \t]*(.*?)[ \t]*$`)
	identifier    = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

// migrateLock is the advisory lock key Migrate holds: "loommigr".
const migrateLock = 0x6c6f6f6d6d696772

// LockTimeout is how long a migration waits for a lock on a table
// before failing (DB615). A migration that queues behind a long
// transaction would otherwise block every query on the table meanwhile.
// A migration can set its own with SET LOCAL lock_timeout.
var LockTimeout = 10 * time.Second

// LoadMigrations reads the migration files at the root of fsys, in
// order. Other .sql files, and duplicate numbers, are errors (DB613);
// files that don't end in .sql are ignored.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Migration
	byVersion := map[int64]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, &Error{Code: DB613, Msg: fmt.Sprintf("migration file %s isn't named NNNN_name.sql (four or more digits, then lowercase letters, digits and underscores)", e.Name()),
				Fix: "create migrations with `loom db new <name>`, which numbers them, or rename the file, e.g. 0003_add_due_dates.sql"}
		}
		v, _ := strconv.ParseInt(m[1], 10, 64)
		if other, dup := byVersion[v]; dup {
			return nil, &Error{Code: DB613, Msg: fmt.Sprintf("migration files %s and %s have the same number %d", other, e.Name(), v),
				Fix: "renumber the newer one after the highest existing number; if both came from merged branches, keep the applied one's number"}
		}
		byVersion[v] = e.Name()
		src, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(src)
		mig := Migration{Version: v, Name: m[2], File: e.Name(), Checksum: hex.EncodeToString(sum[:]), SQL: string(src)}
		if err := annotations(&mig); err != nil {
			return nil, err
		}
		out = append(out, mig)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// annotations parses a migration's `-- loom:` lines: the tables it
// tracks, whether it runs outside a transaction, and the sub-app
// migration it was installed from, if any.
func annotations(m *Migration) error {
	for _, a := range annotation.FindAllStringSubmatch(m.SQL, -1) {
		line := strings.Count(m.SQL[:strings.Index(m.SQL, a[0])], "\n") + 1
		switch {
		case a[1] == "from" && len(strings.Fields(a[2])) == 2 && m.From == "":
			m.From = a[2]
			continue
		case a[1] == "from":
			return &Error{Code: DB616, Msg: fmt.Sprintf("%s:%d: `-- loom:from` needs a package and a file, once, got %q", m.File, line, a[2]),
				Fix: "leave the line `loom db install` wrote as it is"}
		case a[1] == "no-transaction" && a[2] == "":
			m.NoTransaction = true
			continue
		case a[1] == "no-transaction":
			return &Error{Code: DB616, Msg: fmt.Sprintf("%s:%d: `-- loom:no-transaction` takes nothing after it, got %q", m.File, line, a[2]),
				Fix: "write the line as `-- loom:no-transaction`"}
		case a[1] != "track":
			return &Error{Code: DB616, Msg: fmt.Sprintf("%s:%d: unknown annotation `-- loom:%s`", m.File, line, a[1]),
				Fix: "the annotations are `-- loom:track <table>`, which turns on change tracking for a table, `-- loom:no-transaction`, which runs a migration outside a transaction, and `-- loom:from`, written by `loom db install`; write other comments without `loom:`"}
		case !identifier.MatchString(a[2]):
			return &Error{Code: DB616, Msg: fmt.Sprintf("%s:%d: `-- loom:track` needs one table name (lowercase, unquoted), got %q", m.File, line, a[2]),
				Fix: "write one annotation per table, e.g. `-- loom:track todos`"}
		}
		m.Track = append(m.Track, a[2])
	}
	if m.NoTransaction && len(m.Track) > 0 {
		return &Error{Code: DB616, Msg: fmt.Sprintf("%s: `-- loom:no-transaction` and `-- loom:track` are in the same migration", m.File),
			Fix: "track the table in its own migration: a migration outside a transaction should hold only the statement that needs it, such as CREATE INDEX CONCURRENTLY"}
	}
	return nil
}

// MigrationStatus reports which migrations in fsys are applied to db, pending,
// changed since they were applied, or missing from fsys.
func MigrationStatus(ctx context.Context, db *DB, fsys fs.FS) (*Status, error) {
	if db.sql == nil {
		return nil, noDatabase()
	}
	return status(ctx, db.sql, fsys)
}

func status(ctx context.Context, r runner, fsys fs.FS) (*Status, error) {
	files, err := LoadMigrations(fsys)
	if err != nil {
		return nil, err
	}
	type applied struct {
		name, checksum string
		at             time.Time
	}
	done := map[int64]applied{}
	var exists bool
	if err := queryRow(ctx, r, `SELECT to_regclass('loom_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return nil, MapError(err)
	}
	if exists {
		rows, err := r.QueryContext(ctx, `SELECT version, name, checksum, applied_at FROM loom_migrations`)
		if err != nil {
			return nil, MapError(err)
		}
		defer rows.Close()
		for rows.Next() {
			var v int64
			var a applied
			if err := rows.Scan(&v, &a.name, &a.checksum, &a.at); err != nil {
				return nil, err
			}
			done[v] = a
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	st := &Status{Migrations: []MigrationState{}, Problems: []*Error{}}
	for _, m := range files {
		ms := MigrationState{Migration: m, State: "pending"}
		if a, ok := done[m.Version]; ok {
			at := a.at.UTC()
			ms.AppliedAt = &at
			ms.State = "applied"
			if a.checksum != m.Checksum {
				ms.State = "changed"
				st.Problems = append(st.Problems, &Error{Code: DB614, Msg: fmt.Sprintf("migration %s was edited after it was applied: its checksum no longer matches the one recorded in loom_migrations", m.File),
					Fix: fmt.Sprintf("migrations are forward-only: restore %s as it was applied (e.g. `git checkout` it), and make the change in a new migration with `loom db new <name>`", m.File)})
			}
			delete(done, m.Version)
		} else {
			st.Pending++
		}
		st.Migrations = append(st.Migrations, ms)
	}
	for v, a := range done {
		at := a.at.UTC()
		st.Migrations = append(st.Migrations, MigrationState{Migration: Migration{Version: v, Name: a.name, File: fmt.Sprintf("%04d_%s.sql", v, a.name), Checksum: a.checksum}, State: "missing", AppliedAt: &at})
	}
	sort.Slice(st.Migrations, func(i, j int) bool { return st.Migrations[i].Version < st.Migrations[j].Version })
	return st, nil
}

// Migrate applies the pending migrations in fsys to db, in order, each in
// a transaction with its tracking triggers and its loom_migrations row
// (unless it is marked `-- loom:no-transaction`), waiting at most
// LockTimeout for each lock. It holds a Postgres advisory lock
// throughout, so processes migrating at once take turns and each
// migration runs once. Before applying anything it checks that no
// applied migration was edited (DB614). A failing migration is rolled
// back and stops the run (DB615); those before it stay applied.
func Migrate(ctx context.Context, db *DB, fsys fs.FS) ([]Applied, error) {
	if db.sql == nil {
		return nil, noDatabase()
	}
	conn, err := db.sql.Conn(ctx)
	if err != nil {
		return nil, MapError(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, int64(migrateLock)); err != nil {
		return nil, MapError(err)
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, int64(migrateLock)); err != nil {
			// Returned to the pool, the connection would keep the lock and
			// block every later migrate; closing it releases the lock.
			conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS loom_migrations (
	version bigint PRIMARY KEY,
	name text NOT NULL,
	checksum text NOT NULL,
	applied_at timestamptz NOT NULL DEFAULT now()
)`); err != nil {
		return nil, MapError(err)
	}
	st, err := status(ctx, conn, fsys)
	if err != nil {
		return nil, err
	}
	if len(st.Problems) > 0 {
		return nil, st.Problems[0]
	}
	applied := []Applied{}
	for _, ms := range st.Migrations {
		if ms.State != "pending" {
			continue
		}
		start := time.Now()
		if err := apply(ctx, conn, ms.Migration); err != nil {
			return applied, err
		}
		applied = append(applied, Applied{Migration: ms.Migration, Ms: time.Since(start).Milliseconds()})
	}
	if err := retrack(ctx, conn); err != nil {
		return applied, err
	}
	return applied, nil
}

func apply(ctx context.Context, conn *sql.Conn, m Migration) error {
	if m.NoTransaction {
		// No lock timeout: CREATE INDEX CONCURRENTLY waits for older
		// transactions by design, without blocking writes meanwhile.
		if _, err := conn.ExecContext(ctx, m.SQL); err != nil {
			return migrationError(m, err)
		}
		_, err := conn.ExecContext(ctx, `INSERT INTO loom_migrations (version, name, checksum) VALUES ($1, $2, $3)`, m.Version, m.Name, m.Checksum)
		return MapError(err)
	}
	tx, err := beginLocked(ctx, conn)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if hasStatements(m.SQL) {
		// No arguments: pgx sends the file as one simple query, so it
		// may hold many statements.
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			return migrationError(m, err)
		}
	}
	for _, table := range m.Track {
		if err := track(ctx, tx, table); err != nil {
			var e *Error
			if errors.As(err, &e) && e.Code == DB616 {
				e.Msg = m.File + ": " + e.Msg
				return e
			}
			return migrationError(m, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO loom_migrations (version, name, checksum) VALUES ($1, $2, $3)`, m.Version, m.Name, m.Checksum); err != nil {
		return MapError(err)
	}
	return MapError(tx.Commit())
}

// beginLocked begins a transaction that waits at most LockTimeout for a
// lock.
func beginLocked(ctx context.Context, conn *sql.Conn) (*sql.Tx, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, MapError(err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL lock_timeout = '%dms'`, LockTimeout.Milliseconds())); err != nil {
		tx.Rollback()
		return nil, MapError(err)
	}
	return tx, nil
}

// hasStatements reports whether src has anything but comments and space.
func hasStatements(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "--") {
			return true
		}
	}
	return false
}

// migrationError explains a failed migration, with the line and column
// Postgres points at.
func migrationError(m Migration, err error) error {
	where := m.File
	fix := "fix the SQL at the line shown and run `loom migrate` again; nothing from this migration was applied, because it runs in a transaction"
	if m.NoTransaction {
		fix = "fix the migration and run `loom migrate` again; it runs outside a transaction, so undo what it did first (a failed CREATE INDEX CONCURRENTLY leaves an invalid index: DROP INDEX CONCURRENTLY it)"
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "55P03" {
		return &Error{Code: DB615, Msg: fmt.Sprintf("migration %s failed: it waited %s for a lock on a table that another transaction holds (lock_timeout), and gave up rather than block queries on the table meanwhile", m.File, LockTimeout),
			Fix: "run `loom migrate` again when the long transaction has finished (see pg_stat_activity); nothing from this migration was applied. If it must wait longer, begin it with SET LOCAL lock_timeout = '1min'", Err: err}
	}
	if errors.As(err, &pg) && pg.Position > 0 {
		line, col, text := position(m.SQL, int(pg.Position))
		where = fmt.Sprintf("%s:%d:%d", m.File, line, col)
		msg := fmt.Sprintf("migration %s failed: %s\n    %d | %s\n    %s^", where, pg.Message, line, text, strings.Repeat(" ", len(strconv.Itoa(line))+3+col-1))
		return &Error{Code: DB615, Msg: msg, Fix: fix, Err: err}
	}
	msg := err.Error()
	if mapped, ok := MapError(err).(*Error); ok && mapped != err {
		msg = mapped.Msg
	}
	if !m.NoTransaction {
		fix = "fix the migration and run `loom migrate` again; nothing from this migration was applied, because it runs in a transaction"
	}
	return &Error{Code: DB615, Msg: fmt.Sprintf("migration %s failed: %s", where, msg), Fix: fix, Err: err}
}

// position converts Postgres's 1-based character offset into a line,
// column and the text of that line.
func position(src string, pos int) (line, col int, text string) {
	line, col = 1, 1
	start := 0
	i := 0
	for off, r := range src {
		if i == pos-1 {
			break
		}
		i++
		if r == '\n' {
			line, col, start = line+1, 1, off+1
		} else {
			col++
		}
	}
	text = src[start:]
	if j := strings.IndexByte(text, '\n'); j >= 0 {
		text = text[:j]
	}
	return line, col, strings.TrimRight(text, "\r")
}

func queryRow(ctx context.Context, r runner, query string, args ...any) Row {
	rows, err := r.QueryContext(ctx, query, args...)
	if err != nil {
		return &row{err: MapError(err)}
	}
	return &row{rows: &mappedRows{Rows: rows}}
}
