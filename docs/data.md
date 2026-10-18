# Data

Loom's data layer is SQL-first: you write SQL, Loom runs it in real
transactions, scans rows into structs, maps errors to fixes, and applies
migrations. It is not an ORM and has no query builder. Postgres is the
database, through the pgx driver.

## Connecting

```go
app := loom.New(loom.Config{DB: os.Getenv("DATABASE_URL")})
db := app.DB() // *loom.DB
```

`app.Run` checks the connection first (DB601). With a database, jobs are
stored in it (`pgjobs`, tables created at start) and enqueue in your
transactions. Without `Config.DB`, `app.DB()` is a local DB: transactions
commit, roll back and run hooks, but SQL fails (DB602).

Tests and tools that already have a `*loom.DB` give it with
`app.UseDB(db)`, before any jobs method (RT411). `loom.OpenDB(ctx, url)`
opens one directly.

## Reads

```go
type Todo struct {
	ID     int64
	UserID string        // column user_id
	Title  string
	DoneAt *time.Time    // NULL-able: a pointer or sql.Null*
	Note   string `db:"body"`
	Draft  bool   `db:"-"` // never scanned
}

todos, err := loom.Select[Todo](ctx, db, `select * from todos where user_id = $1 order by id`, userID)
todo, err := loom.Get[Todo](ctx, db, `select * from todos where id = $1`, id)
if errors.Is(err, loom.ErrNotFound) { ... }
n, err := loom.Get[int](ctx, db, `select count(*) from todos`)
```

- Fields take the column of their `db` tag, or else their name in
  snake_case (`UserID` takes `user_id`). Embedded structs contribute
  their fields. Fields with no column are left alone.
- A column with no field is DB610, naming both; a value that doesn't fit
  its field (a NULL into `string`) is DB611.
- A non-struct `T` (`int`, `string`, `time.Time`) takes a single column.
- `Select` returns an empty, non-nil slice for no rows. `Get` returns
  DB604 (`ErrNotFound`) for none and DB605 for more than one.
- Placeholders are Postgres's `$1, $2`. SQL is the query language.

## Writes and transactions

`*loom.DB` and `loom.Tx` both implement `loom.Querier` (`Exec`, `Query`,
`QueryRow`), so `Select` and `Get` take either.

```go
err := db.Tx(ctx, func(tx loom.Tx) error {
	if _, err := tx.Exec(ctx, `update todos set done = not done where id = $1`, id); err != nil {
		return err
	}
	if _, err := jobs.Enqueue(tx, NotifyToggle{ID: id}); err != nil {
		return err
	}
	tx.AfterCommit(func() { app.Broadcast("todos:"+userID, "changed", nil) })
	return nil
})
```

- `db.Tx` commits when `f` returns nil. An error or a panic rolls back;
  the error is returned and the panic continues.
- `tx.AfterCommit(f)` runs after a successful commit, never on rollback.
  A hook that panics is reported (DB624) and the hooks after it run.
  This is how live pages hear of committed changes: pages
  `c.Subscribe("todos:"+userID)` and reload in `HandleInfo`.
- Jobs enqueued in a `Tx` exist if and only if it commits (`jobs.md`).
- Use the `Tx` only inside `f` (DB603 afterwards).
- In `Mount` and event handlers, pass `c.Context()` to queries: it ends
  with the page. Every statement also stops after
  `Config.StatementTimeout` (30 seconds by default); a slow job raises it
  for its transaction with `SET LOCAL statement_timeout`.
- Each commit and rollback is a `tx.commit` or `tx.rollback` event, and
  a statement slower than `Config.SlowQuery` (500ms) a `db.slow`
  warning, under the trace of the action that ran it (`events.md`). An
  app's database reports to its stream; another `*loom.DB` does after
  `db.Observe(app.Stream(), slow)`.
- Every statement is also counted by its text, fast ones too, with its
  time, rows and errors: `loom queries` (`db.QueryStats()`) lists them
  the busiest first, Loom's own job polling included. A cheap query
  that runs twelve times a second is the top line there, though it is
  never slow enough for `db.slow`. Code that must run SQL on
  `db.SQL()` directly counts it with `db.Record(ctx, query, start,
  rows, err)`.

## Errors

Database errors that Loom can explain become `*data.Error` with a code
and a fix; the driver's error stays reachable with `errors.As`.

| Case | Code | Test with |
|---|---|---|
| unique violation | DB606 | `errors.Is(err, loom.ErrUniqueViolation)` |
| foreign key violation | DB607 | `errors.Is(err, loom.ErrForeignKeyViolation)` |
| not-null violation | DB608 | `errors.Is(err, loom.ErrNotNullViolation)` |
| table doesn't exist | DB609 | (run `loom migrate`) |
| column doesn't exist | DB618 | (Postgres's "perhaps you meant", `loom db schema`) |
| SQL syntax error | DB619 | |
| value of the wrong type for a column | DB620 | |
| no function or operator for these types | DB621 | (cast, e.g. `$1::text`) |

Errors that Postgres places in the statement (DB609 and DB618 to DB621)
quote the statement's line with a caret
under the spot.

## Migrations

Migrations are plain SQL files in `migrations/`, named `NNNN_name.sql`.

```
loom db new create_todos       # creates migrations/0001_create_todos.sql
loom migrate                   # applies pending migrations
loom migrate status            # applied, pending, changed, missing
```

```sql
-- migrations/0001_create_todos.sql
CREATE TABLE todos (
	id bigserial PRIMARY KEY,
	user_id text NOT NULL,
	title text NOT NULL,
	done boolean NOT NULL DEFAULT false
);
-- loom:track todos
```

- Applied in order of their number, each in its own transaction, and
  recorded with a sha256 checksum in `loom_migrations`.
- Forward-only: editing an applied migration is DB614; make the change
  in a new one. A failing migration is rolled back and reported with
  file, line and column (DB615).
- `loom migrate` holds an advisory lock, so deploys migrating at once
  take turns and each migration runs once.
- Each migration waits at most 10s for a lock on a table
  (`data.LockTimeout`), then fails (DB615) rather than queue behind a
  long transaction and block every query on the table meanwhile. Begin
  one with `SET LOCAL lock_timeout = '1min';` to wait longer.
- A migration with a line `-- loom:no-transaction` runs outside a
  transaction, for a statement that can't run in one:
  `CREATE INDEX CONCURRENTLY` builds an index without blocking writes.
  Keep it to that one statement; if it fails, undo what it did (`DROP
  INDEX CONCURRENTLY` the invalid index) before running it again.
- `loom dev` applies pending migrations on start and whenever a file in
  `migrations/` changes (with `$DATABASE_URL` set). In production, only
  `loom migrate` or the app's own `myapp migrate` (`loom.MigrateCommand`
  on its embedded migrations; `loom.Migrate` in Go) applies them, before
  the app starts; it never migrates itself. See `deploy.md`.
- Loom's own tables (`loom_clients`, `loom_user_sessions`,
  `loom_rate_limits`, `loom_jobs`) are migrations too, the first in a
  new app's `migrations/`; `loom db install github.com/moomerman/loom`
  adds them, and new ones after an upgrade. `Run` refuses to start with
  a database that lacks them (RT433).
- All `loom db` and `loom migrate` commands read `--db` or
  `$DATABASE_URL`, and `--dir` (default `migrations`).

A mounted sub-app's migrations are installed into yours with `loom db
install <package>` (`subapps.md`), then applied by `loom migrate`.

## Change tracking

A table is tracked when a migration has a line `-- loom:track <table>`
(in the migration that creates it, or a later one). After the
migration's SQL runs, Loom adds a statement trigger that sends
`NOTIFY loom_sync` with the table's name when a transaction that
changed it commits; a rolled-back change sends nothing. Pages watch
tracked tables with `loom.Watch` (`live.md`), on every node. Tracking
needs no primary key, and `loom db schema` marks tracked tables.

Databases tracked by an older Loom also have a `version` and a
`loom_xid` column on each tracked table, a `loom_version_seq` sequence
and a `loom_tombstones` table. Nothing uses them now; migrating drops
their triggers and leaves the columns and tables in place, harmless
(`select *` scans ignore `loom_xid`). Drop them in a migration if you
like.

## Inspecting the database

```
loom db schema                         # tables, columns, types, keys, tracked
loom db query "select * from todos"    # read-only transaction, 100 rows
```

Both have `--json` and are `loom mcp` tools (`db_schema`, `db_query`,
`migrate_status`), so agents can read the real schema instead of
guessing. `db query` runs in a read-only transaction with a 10-second
timeout; writes fail.

## Testing

```go
db := loomtest.DB(t)                       // this test's transaction
db := loomtest.DB(t, loomtest.Committed()) // a database of its own
```

- Postgres comes from `LOOM_TEST_DATABASE_URL` (its user needs
  CREATEDB); without it the test is skipped.
- The database is built from the nearest `migrations` directory at or
  above the package (`loomtest.Migrations(dir)` to choose), once, as a
  template named after the migrations' checksums.
- Default: everything runs in one transaction rolled back when the test
  ends. `db.Tx` makes a savepoint; `AfterCommit` runs when it is
  released; a failed statement doesn't spoil the test's transaction.
  Use it from one goroutine. Sequences aren't rolled back, so look ids
  up rather than assuming them.
- Jobs: `loomtest.Jobs(t, loomtest.UseDB(db))` makes enqueues in `db`
  land in the test's job queue.
- `Committed()` clones the template into a fresh database, dropped
  afterwards, for real commits, concurrency, `LISTEN/NOTIFY` and the
  Postgres job store.

```go
func TestToggle(t *testing.T) {
	app := loom.New()
	app.UseDB(loomtest.DB(t))
	todo.Setup(app)
	p := loomtest.Mount(t, app, "/")
	p.Event("toggle", loom.Payload{"id": "1"})
	p.AssertHTML("li.done", 1)
}
```
