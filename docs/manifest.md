# The app manifest

`loom.manifest` says what an app does, in words a reviewer can approve
without reading Go, templates or SQL: its routes and who may reach them,
the events each page sends and what handles them, the tables each page
watches, its migrations and its background jobs. It is committed with
the code, so a pull request that changes the app's behaviour changes it
too: "this feature adds a route, two events, a migration and a daily
job".

## Keeping it

One test per app keeps it fresh:

```go
func TestManifest(t *testing.T) {
	app := loom.New()
	todo.Setup(app)
	loomtest.Manifest(t, app)
}
```

`loomtest.Manifest` builds the manifest and compares it with
`loom.manifest` in the test's package directory. When they differ the
test fails with the difference; `go test -update` rewrites the file.
Commit it with the change. It needs no database and no server: it comes
from what the app registers, what `loom build` recorded in the
generated code, and the migrations in the nearest `migrations`
directory (found as `loomtest.DB` finds it). So a stale `*_hx.go` gives a stale manifest; run
`loom build` first.

## What it lists

```
routes

  /todos/{id}  live page templates.TodoDetail in templates.App
    auth: none
    events, handled by (*todo.TodoDetailPage).Events:
      toggle_todo (on click, val-id)
    re-renders when these tables change: todos

mounts

  /admin/jobs  *jobsweb.App
    auth: todo.requireAdmin
    routes: /, /jobs, /jobs/{id}, /cron
```

- **routes**: each page outside a mount, its component and app layout,
  its auth checks (outer first), its rate limits (`loom.RateLimit`, e.g.
  "rate limit: 5 per minute by email on sign_in"), the events its markup sends by name
  with the DOM event and `val-*` names (including events of the
  components it calls) and the Go value that handles them, and the
  tables it watches (`loom.Watch`), and whether the page keeps state in
  its URL (`HandleParams`, see `live.md`). An event a caller chooses through
  an event parameter is listed under the caller.
- **layouts**: the events an app layout sends, and what handles them:
  every page in it, or a live layout's own `Events` (`layouts.md`). A
  page's event that its live layout handles says so.
- **handlers**: plain HTTP handlers (`Group.Handle`), their auth and
  rate limits.
- **mounts**: each mounted sub-app, its auth, its routes and their rate
  limits (once for a limit on every route), and "dev
  only" when it is served only in dev (`loom.DevOnly`, as `devmail`
  is). Its own events are its package's business.
- **database (migrations)**: each migration, the tables it tracks
  (`-- loom:track`), and the first line of each schema statement;
  statements that change rows (seed data, backfills) fold into one line
  naming their tables.
- **background jobs**: job types with queue, attempts, timeout and the
  args they carry; cron entries; queues and their workers; whether this
  node runs them (`Config.Jobs`, JSON `jobs.mode`).
- **broadcasts**: how `app.Broadcast` and jobs' broadcasts reach pages:
  every node's by Postgres NOTIFY (the default), or through the
  `Config.PubSub` named (JSON `pubsub`).
- **security headers**: the Content-Security-Policy responses carry,
  and Strict-Transport-Security when `BaseURL` is https (JSON
  `security`).
- **for review**: things worth a look that may be intended, so they are
  not errors, such as a mount with no auth.

Template positions are left out of the file, so moving markup doesn't
change it. `loom manifest --json` (and the `manifest` MCP tool) gives
the same from a running dev server, positions included; the JSON is
`loom.Manifest`.

## Reviewing a change: loom diff

```
loom diff [--json] [BASE[..HEAD]]
```

`loom diff` compares the `loom.manifest` files of two revisions and
says, per file and section, which items (routes, migrations, jobs,
flags) were added, removed or changed:

```
changes from 0232b0e to 5d1c9a2

examples/todo/loom.manifest (modified)

  routes
    ~ /  live page templates.TodoApp in templates.App
        + archive_todo (on click, val-id)

  database (migrations)
    + 0005_archive.sql
        ALTER TABLE todos ADD COLUMN archived_at timestamptz
```

`main..HEAD` compares from where HEAD branched off main, as a pull
request does; `BASE` alone compares with the working tree. The default
is `origin/main` (or `main`) against the working tree. It needs git but
no app; the `manifest_diff` MCP tool takes the same range.
