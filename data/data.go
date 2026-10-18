// Package data is Loom's SQL-first persistence layer: a database handle,
// transactions with AfterCommit hooks, the Querier interface that read
// helpers and jobs accept, typed reads (Select, Get),
// mapped errors, migrations and change tracking. The root package
// re-exports it as loom.DB, loom.Tx, loom.Select and so on; use those
// names. See docs/data.md.
//
//	db, err := loom.OpenDB(ctx, os.Getenv("DATABASE_URL"))
//	err = db.Tx(ctx, func(tx loom.Tx) error {
//		if _, err := tx.Exec(ctx, `update todos set done = not done where id = $1`, id); err != nil {
//			return err
//		}
//		jobs.Enqueue(tx, NotifyToggle{ID: id})
//		tx.AfterCommit(func() { app.PubSub().Publish(loom.Message{Topic: "todos", Event: "changed"}) })
//		return nil
//	})
//
// Postgres comes first, through the pgx driver; nothing in these
// interfaces is Postgres-specific, so a SQLite backend can follow.
package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"github.com/moomerman/loom/trace"
)

// Result is the outcome of Exec.
type Result interface {
	RowsAffected() (int64, error)
}

// Rows are the rows of a query. *sql.Rows implements it.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Columns() ([]string, error)
	Err() error
	Close() error
}

// Row is one row of a query; Scan returns sql.ErrNoRows if there was none.
// *sql.Row implements it.
type Row interface {
	Scan(dest ...any) error
}

// Querier runs SQL. Both *DB and Tx implement it, so read helpers accept
// either. Errors the database reports are mapped to *Error where Loom
// knows a fix (unique and foreign key violations, a missing table); the
// driver's error stays available through errors.As.
type Querier interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Query(ctx context.Context, query string, args ...any) (Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) Row
}

// Tx is a database transaction, given to the function passed to DB.Tx.
// It is the one transaction type in Loom: app code, jobs.Enqueue and
// jobs take a Tx.
type Tx interface {
	Querier
	// Context is the context the transaction was started with.
	Context() context.Context
	// AfterCommit runs f after the transaction commits, never if it rolls
	// back. Use it to notify live pages of committed changes.
	AfterCommit(f func())
	// AfterRollback runs f if the transaction rolls back, for code that
	// keeps state on the transaction's behalf (in-memory job stores).
	AfterRollback(f func())
	// DB is the database the transaction belongs to.
	DB() *DB
}

// runner runs SQL: *sql.DB and *sql.Tx both do.
type runner interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// DB is a database handle, safe for concurrent use. It is one of three
// kinds:
//
//   - a Postgres database, from Open or FromSQL;
//   - a local DB with no database (NewLocal), whose transactions still
//     commit, roll back and run hooks, for in-memory apps;
//   - a DB inside one transaction (InTx), whose Tx calls are savepoints,
//     for tests that roll everything back (loomtest.DB).
type DB struct {
	sql    *sql.DB // the pool of a Postgres DB
	within *sql.Tx // the transaction of an InTx DB

	mu         sync.Mutex
	values     map[any]any
	savepoints int                      // savepoints made in within, for unique names
	obs        atomic.Pointer[observer] // where it reports (Observe)
	queries    queryStats               // every statement, by text (QueryStats)
}

// Open connects to a Postgres database URL with the pgx driver and checks
// the connection.
func Open(ctx context.Context, url string) (*DB, error) {
	sdb, err := sql.Open("pgx", url)
	if err != nil {
		return nil, connectError(err)
	}
	if err := sdb.PingContext(ctx); err != nil {
		sdb.Close()
		return nil, connectError(err)
	}
	return FromSQL(sdb), nil
}

func connectError(err error) error {
	return &Error{Code: DB601, Msg: fmt.Sprintf("can't connect to the database: %v", err), Fix: "check the database URL (postgres://user:password@host:5432/dbname) and that Postgres is running", Err: err}
}

// FromSQL wraps a *sql.DB. Use the pgx driver (sql.Open("pgx", url)) for
// Postgres; the jobs store's LISTEN needs it.
func FromSQL(sdb *sql.DB) *DB { return &DB{sql: sdb} }

// NewLocal returns a DB with no database behind it. Its transactions run
// AfterCommit and AfterRollback hooks, so jobs enqueued in them follow
// the same rules, but any SQL fails with DB602.
func NewLocal() *DB { return &DB{} }

// InTx returns a DB whose SQL all runs inside stx. Its Tx method makes a
// savepoint, released on success (running AfterCommit hooks) and rolled
// back on error or panic; statements run on the DB itself get a savepoint
// each too, so a failed one doesn't abort stx. Roll stx back afterwards
// and nothing persists: loomtest.DB gives each test one of these. Use it
// from one goroutine at a time, like the *sql.Tx it wraps. Its SQL method
// returns nil, so it can't host a Postgres job store or LISTEN.
func InTx(stx *sql.Tx) *DB { return &DB{within: stx} }

// SQL returns the underlying *sql.DB, or nil for a local or InTx DB.
func (db *DB) SQL() *sql.DB { return db.sql }

// IsInTx reports whether db was made by InTx.
func (db *DB) IsInTx() bool { return db.within != nil }

// Close closes the database. It does nothing for a local or InTx DB.
func (db *DB) Close() error {
	if db.sql == nil {
		return nil
	}
	return db.sql.Close()
}

// SetValue attaches a value to the DB under key, for code that works with
// a DB's transactions: a jobs store binds itself this way, so
// jobs.Enqueue(tx, ...) finds the store of tx's DB.
func (db *DB) SetValue(key, value any) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.values == nil {
		db.values = map[any]any{}
	}
	db.values[key] = value
}

// Value returns the value attached under key, or nil.
func (db *DB) Value(key any) any {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.values[key]
}

func noDatabase() error {
	return &Error{Code: DB602, Msg: "this DB has no database behind it, so it can't run SQL", Fix: "open a database with loom.OpenDB(ctx, url) (or loom.Config{DB: url}) and use it instead of the in-memory default"}
}

// savepoint makes a savepoint in an InTx DB's transaction and returns a
// function that releases it (ok) or rolls back to it.
func (db *DB) savepoint(ctx context.Context) (func(ok bool) error, error) {
	db.mu.Lock()
	db.savepoints++
	name := fmt.Sprintf("loom_sp_%d", db.savepoints)
	db.mu.Unlock()
	if _, err := db.within.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return nil, MapError(err)
	}
	return func(ok bool) error {
		if ok {
			_, err := db.within.ExecContext(ctx, "RELEASE SAVEPOINT "+name)
			return MapError(err)
		}
		_, err := db.within.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+name)
		return MapError(err)
	}, nil
}

// Exec runs a statement outside a transaction.
func (db *DB) Exec(ctx context.Context, query string, args ...any) (res Result, err error) {
	defer db.execDone(ctx, query, time.Now(), &res, &err)
	switch {
	case db.within != nil:
		end, err := db.savepoint(ctx)
		if err != nil {
			return nil, err
		}
		res, err := db.within.ExecContext(ctx, query, args...)
		if endErr := end(err == nil); err == nil {
			err = endErr
		}
		return res, mapQueryError(err, query)
	case db.sql != nil:
		res, err := db.sql.ExecContext(ctx, query, args...)
		return res, mapQueryError(err, query)
	}
	return nil, noDatabase()
}

// Query runs a query outside a transaction.
func (db *DB) Query(ctx context.Context, query string, args ...any) (rows Rows, err error) {
	defer db.queryDone(ctx, query, time.Now(), &rows, &err)
	switch {
	case db.within != nil:
		end, err := db.savepoint(ctx)
		if err != nil {
			return nil, err
		}
		rows, err := db.within.QueryContext(ctx, query, args...)
		if err != nil {
			end(false)
			return nil, mapQueryError(err, query)
		}
		return &mappedRows{Rows: rows, end: end}, nil
	case db.sql != nil:
		return query1(ctx, db.sql, query, args)
	}
	return nil, noDatabase()
}

// QueryRow runs a query that returns at most one row.
func (db *DB) QueryRow(ctx context.Context, query string, args ...any) Row {
	return firstRow(db.Query(ctx, query, args...))
}

func query1(ctx context.Context, r runner, query string, args []any) (Rows, error) {
	rows, err := r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapQueryError(err, query)
	}
	return &mappedRows{Rows: rows}, nil
}

// mappedRows maps the errors of *sql.Rows, and ends the savepoint of a
// query on an InTx DB when closed.
type mappedRows struct {
	*sql.Rows
	end    func(ok bool) error
	closed bool

	read  int64            // rows read by Next
	count func(rows int64) // adds them to the query's stats, once, on Close
}

func (r *mappedRows) Next() bool {
	if r.Rows.Next() {
		r.read++
		return true
	}
	return false
}

func (r *mappedRows) Scan(dest ...any) error { return MapError(r.Rows.Scan(dest...)) }
func (r *mappedRows) Err() error             { return MapError(r.Rows.Err()) }

func (r *mappedRows) Close() error {
	err := r.Rows.Close()
	if r.count != nil {
		r.count(r.read)
		r.count = nil
	}
	if r.end != nil && !r.closed {
		r.closed = true
		ok := err == nil && r.Rows.Err() == nil
		if endErr := r.end(ok); err == nil {
			err = endErr
		}
	}
	return MapError(err)
}

// row is the first row of a query, read when scanned.
type row struct {
	rows Rows
	err  error
}

func firstRow(rows Rows, err error) Row { return &row{rows, err} }

func (r *row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer r.rows.Close()
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	if err := r.rows.Scan(dest...); err != nil {
		return err
	}
	return r.rows.Close()
}

// Tx runs f in a transaction. It commits if f returns nil, and rolls back
// if f returns an error or panics (the panic continues after the
// rollback). AfterCommit hooks run after a successful commit, in order;
// AfterRollback hooks run after a rollback, including a failed commit.
func (db *DB) Tx(ctx context.Context, f func(tx Tx) error) (err error) {
	start := time.Now()
	t, err := db.begin(ctx)
	if err != nil {
		return err
	}
	committed, reason := false, "panic"
	defer func() {
		if committed {
			return
		}
		p := recover()
		t.rollback()
		db.finished(t, start, reason)
		if p != nil {
			panic(p)
		}
	}()
	if err := f(t); err != nil {
		reason = "error"
		return err
	}
	if err := t.commit(); err != nil {
		reason = "commit failed"
		return err
	}
	committed = true
	db.finished(t, start, "")
	t.runHooks(true)
	return nil
}

func (db *DB) begin(ctx context.Context) (*tx, error) {
	t := &tx{ctx: ctx, db: db}
	switch {
	case db.within != nil:
		end, err := db.savepoint(ctx)
		if err != nil {
			return nil, err
		}
		t.sql, t.end = db.within, end
	case db.sql != nil:
		stx, err := db.sql.BeginTx(ctx, nil)
		if err != nil {
			return nil, MapError(err)
		}
		t.sql = stx
		t.end = func(commit bool) error {
			if commit {
				return MapError(stx.Commit())
			}
			return stx.Rollback()
		}
	}
	return t, nil
}

type tx struct {
	ctx context.Context
	db  *DB
	sql runner                  // nil for a local transaction
	end func(commit bool) error // commits or rolls back sql

	statements atomic.Int64 // run so far, for tx.commit

	mu         sync.Mutex
	done       bool
	onCommit   []func()
	onRollback []func()
}

func (t *tx) Context() context.Context { return t.ctx }
func (t *tx) DB() *DB                  { return t.db }

func (t *tx) AfterCommit(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onCommit = append(t.onCommit, f)
}

func (t *tx) AfterRollback(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onRollback = append(t.onRollback, f)
}

func (t *tx) check() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return &Error{Code: DB603, Msg: "the transaction has already committed or rolled back", Fix: "use the Tx only inside the function passed to db.Tx; don't keep it after that function returns"}
	}
	if t.sql == nil {
		return noDatabase()
	}
	return nil
}

func (t *tx) Exec(ctx context.Context, query string, args ...any) (res Result, err error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	t.statements.Add(1)
	defer t.db.execDone(ctx, query, time.Now(), &res, &err)
	r, err := t.sql.ExecContext(ctx, query, args...)
	return r, mapQueryError(err, query)
}

func (t *tx) Query(ctx context.Context, query string, args ...any) (rows Rows, err error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	t.statements.Add(1)
	defer t.db.queryDone(ctx, query, time.Now(), &rows, &err)
	return query1(ctx, t.sql, query, args)
}

func (t *tx) QueryRow(ctx context.Context, query string, args ...any) Row {
	return firstRow(t.Query(ctx, query, args...))
}

func (t *tx) finish() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return false
	}
	t.done = true
	return true
}

func (t *tx) commit() error {
	if !t.finish() {
		return nil
	}
	if t.end != nil {
		if err := t.end(true); err != nil {
			if t.db.within != nil {
				t.end(false) // a savepoint that failed to release is still open
			}
			t.runHooks(false)
			return err
		}
	}
	return nil
}

func (t *tx) rollback() {
	if !t.finish() {
		return
	}
	if t.end != nil {
		t.end(false)
	}
	t.runHooks(false)
}

func (t *tx) runHooks(committed bool) {
	t.mu.Lock()
	hooks := t.onRollback
	if committed {
		hooks = t.onCommit
	}
	t.onCommit, t.onRollback = nil, nil
	t.mu.Unlock()
	for _, f := range hooks {
		t.runHook(f, committed)
	}
}

// runHook runs a hook, recovering a panic: the transaction is already
// over, so the panic is reported (DB624) rather than crash the caller,
// and the hooks after it still run.
func (t *tx) runHook(f func(), committed bool) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		which, state := "AfterRollback", "rolled back"
		if committed {
			which, state = "AfterCommit", "committed"
		}
		e := &Error{Code: DB624, Msg: fmt.Sprintf("an %s hook panicked: %v; the transaction had %s, and the hooks after it ran", which, p, state),
			Fix: "find the panic in the stack and fix the hook; hooks run after the transaction, so they can't undo it"}
		if o := t.db.obs.Load(); o != nil {
			o.stream.Emit(t.ctx, trace.Error, "msg", e.Msg, "code", DB624, "fix", e.Fix, "stack", string(debug.Stack()))
			return
		}
		slog.Error(e.Error(), "stack", string(debug.Stack()))
	}()
	f()
}

// Error is a data-layer error with a stable code, documented in
// docs/errors.md, and a fix. Errors of the same code match with
// errors.Is, so errors.Is(err, loom.ErrNotFound) works whatever the
// message. Err is the driver's error, if any.
type Error struct {
	Code string `json:"code"` // e.g. "DB601"
	Msg  string `json:"message"`
	Fix  string `json:"fix"`
	Err  error  `json:"-"`
}

func (e *Error) Error() string {
	s := fmt.Sprintf("error %s: %s", e.Code, e.Msg)
	if e.Fix != "" {
		s += "\n  fix: " + e.Fix
	}
	return s
}

// Unwrap returns the driver's error.
func (e *Error) Unwrap() error { return e.Err }

// Is reports whether target is an *Error with the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Data error codes.
const (
	DB601 = "DB601" // can't connect to the database
	DB602 = "DB602" // SQL on a DB with no database
	DB603 = "DB603" // a Tx used after its transaction ended
	DB604 = "DB604" // Get found no row (ErrNotFound)
	DB605 = "DB605" // Get found more than one row
	DB606 = "DB606" // unique violation
	DB607 = "DB607" // foreign key violation
	DB608 = "DB608" // not-null violation
	DB609 = "DB609" // the table doesn't exist
	DB610 = "DB610" // a column has no matching struct field
	DB611 = "DB611" // a column's value doesn't fit its field
	DB612 = "DB612" // the query's columns don't fit the type
	DB613 = "DB613" // a badly named or duplicate migration file
	DB614 = "DB614" // an applied migration was edited
	DB615 = "DB615" // a migration failed
	DB616 = "DB616" // a bad loom: annotation in a migration
	DB618 = "DB618" // the statement names a column that doesn't exist
	DB619 = "DB619" // the statement has a syntax error
	DB620 = "DB620" // a value's type doesn't fit its column
	DB621 = "DB621" // no function or operator takes these argument types
	DB622 = "DB622" // loom.Watch names a table that isn't tracked, or the app has no database
	DB623 = "DB623" // the connection doesn't keep LISTEN: a transaction-mode pooler such as pgbouncer
	DB624 = "DB624" // an AfterCommit or AfterRollback hook panicked
)

// Errors to test for with errors.Is. Each matches every error of its
// code, whatever its message.
var (
	ErrNotFound            = &Error{Code: DB604, Msg: "no row"}
	ErrUniqueViolation     = &Error{Code: DB606, Msg: "unique violation"}
	ErrForeignKeyViolation = &Error{Code: DB607, Msg: "foreign key violation"}
	ErrNotNullViolation    = &Error{Code: DB608, Msg: "not-null violation"}
)

// IsCode reports whether err is a data error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}
