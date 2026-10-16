package loom

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/pubsub"
	"github.com/moomerman/loom/trace"
)

// The data layer lives in package data; these names are how apps use it.
// See docs/data.md.

// DB is a database handle: Postgres from OpenDB, or a local DB with no
// database (NewLocalDB) whose transactions still commit and roll back
// jobs.
type DB = data.DB

// Tx is a database transaction, given to the function passed to DB.Tx.
// App code and jobs.Enqueue take a Tx.
type Tx = data.Tx

// Querier runs SQL: both *DB and Tx implement it.
type Querier = data.Querier

// Select runs a query and scans each row into a T: a struct whose fields
// take the columns of their db tag or snake_case name, or a single value
// for one column. See data.Select.
//
//	todos, err := loom.Select[Todo](ctx, db, `select * from todos where user_id = $1 order by id`, userID)
func Select[T any](ctx context.Context, q Querier, query string, args ...any) ([]T, error) {
	return data.Select[T](ctx, q, query, args...)
}

// Get runs a query for one row and scans it into a T, as Select does. No
// row is an error matching ErrNotFound; more than one is DB605.
//
//	todo, err := loom.Get[Todo](ctx, db, `select * from todos where id = $1`, id)
func Get[T any](ctx context.Context, q Querier, query string, args ...any) (T, error) {
	return data.Get[T](ctx, q, query, args...)
}

// Errors to test for with errors.Is.
var (
	ErrNotFound            = data.ErrNotFound            // DB604: Get found no row
	ErrUniqueViolation     = data.ErrUniqueViolation     // DB606
	ErrForeignKeyViolation = data.ErrForeignKeyViolation // DB607
	ErrNotNullViolation    = data.ErrNotNullViolation    // DB608
)

// OpenDB connects to a Postgres database URL with the pgx driver.
func OpenDB(ctx context.Context, url string) (*DB, error) { return data.Open(ctx, url) }

// NewLocalDB returns a DB with no database behind it, for in-memory apps:
// its transactions run AfterCommit hooks and keep or drop the jobs
// enqueued in them, but SQL fails (DB602).
func NewLocalDB() *DB { return data.NewLocal() }

// UseDB sets the app's database to one already open, such as a test's
// loomtest.DB(t); apps give the URL in Config.DB instead. It must be
// called before any jobs method, so the app's job store binds to it.
func (a *App) UseDB(db *DB) {
	if a.jobs != nil || a.db != nil {
		panic(&Error{Code: RT411, Msg: "UseDB was called after the app already had a database: from loom.Config{DB: ...}, or because app.DB or a jobs method used the default", Fix: "call app.UseDB first, before app.DB, app.JobStore, app.Jobs, app.Queues and app.Cron"})
	}
	a.db = db
	a.observe(db)
}

// DB returns the app's database: the one from Config.DB or UseDB, the
// job store's, or a local DB. Enqueue jobs in its transactions:
//
//	app.DB().Tx(ctx, func(tx loom.Tx) error {
//		_, err := jobs.Enqueue(tx, Export{UserID: id})
//		return err
//	})
func (a *App) DB() *DB {
	if a.db == nil {
		a.db = data.NewLocal()
		a.observe(a.db)
	}
	return a.db
}

// openConfigDB sets the app's database from Config.DB, with the pool
// limits and statement timeout of the Config. The connection is checked
// by Run.
func (a *App) openConfigDB(url string) {
	cc, err := pgx.ParseConfig(url)
	if err != nil {
		panic(&data.Error{Code: data.DB601, Msg: "can't parse the database URL: " + err.Error(), Fix: "check Config.DB: postgres://user:password@host:5432/dbname", Err: err})
	}
	// Session settings every connection starts with, unless the URL sets
	// them: a statement that hangs fails instead of holding a page or
	// the job runner, and pg_stat_activity names the app.
	if _, ok := cc.RuntimeParams["statement_timeout"]; !ok {
		cc.RuntimeParams["statement_timeout"] = strconv.FormatInt(a.cfg.StatementTimeout.Milliseconds(), 10)
	}
	if _, ok := cc.RuntimeParams["application_name"]; !ok {
		cc.RuntimeParams["application_name"] = "loom"
	}
	sdb := stdlib.OpenDB(*cc)
	sdb.SetMaxOpenConns(a.cfg.DBMaxConns)
	sdb.SetMaxIdleConns(min(a.cfg.DBMaxConns, 10))
	sdb.SetConnMaxLifetime(30 * time.Minute) // so connections follow a failover or a config change
	a.db = data.FromSQL(sdb)
	a.observe(a.db)
}

// checkDB checks the connection to a Postgres database, for Run.
func (a *App) checkDB(ctx context.Context) error {
	if a.db == nil || a.db.SQL() == nil {
		return nil
	}
	if err := a.db.SQL().PingContext(ctx); err != nil {
		return &data.Error{Code: data.DB601, Msg: "can't connect to the database: " + err.Error(), Fix: "check the database URL (postgres://user:password@host:5432/dbname) and that Postgres is running", Err: err}
	}
	return nil
}

// checkSessionMode checks, for Run, that a connection keeps the session
// state LISTEN needs (DB623). Table watch, the jobs runner's wake-ups and
// its leader lock all hold a connection for LISTEN or an advisory lock;
// behind a pooler in transaction mode (pgbouncer's default) the server
// connection changes between statements, so they would fail silently:
// pages that never re-render, cron that never runs. The check LISTENs on
// one connection, notifies from another and waits for the notification.
func (a *App) checkSessionMode(ctx context.Context) error {
	if a.db == nil || a.db.SQL() == nil {
		return nil
	}
	channel := "loom_check_" + newSID()[:12]
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	listen, heard := context.WithCancel(ctx) // cancelled by the notification
	defer heard()
	listening := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- listenOnce(listen, a.db.SQL(), channel, func() { close(listening) }, func(string) { heard() })
	}()
	select {
	case <-listening:
	case err := <-done:
		return poolerError(err)
	}
	if _, err := a.db.Exec(ctx, `SELECT pg_notify($1, '')`, channel); err != nil {
		return poolerError(err)
	}
	if err := <-done; ctx.Err() != nil { // the timeout ended the wait, not the notification
		return poolerError(err)
	}
	return nil
}

func poolerError(err error) error {
	return &data.Error{Code: data.DB623, Msg: fmt.Sprintf("a LISTEN on one database connection didn't hear a NOTIFY from another (%v), as behind a pooler in transaction mode", err),
		Fix: "connect directly to Postgres, or through a pooler in session mode (pgbouncer pool_mode = session); table watch, job wake-ups and the cron leader lock need connections that keep their session", Err: err}
}

// Broadcast publishes a message to the pages subscribed to topic, as a
// job's c.Broadcast does. Call it in tx.AfterCommit to tell pages about a
// committed change:
//
//	tx.AfterCommit(func() { app.Broadcast("todos:"+userID, "changed", nil) })
func (a *App) Broadcast(topic, event string, payload map[string]string) error {
	a.stream.Emit(context.Background(), trace.PubSubPublish, "topic", topic, "event", event, "subscribers", pubsub.Subscribers(a.cfg.PubSub, topic))
	return a.cfg.PubSub.Publish(pubsub.Message{Topic: topic, Event: event, Payload: payload})
}
