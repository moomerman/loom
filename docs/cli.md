# Command-line interface

Every command that prints output for people also accepts `--json`, which
prints the same information in the stable schema documented here.

## loom new

```
loom new [--json] [--module PATH] [--loom DIR] DIR
```

Creates an app in `DIR`, which must not exist or be empty (exit 2
otherwise): a module with one live page, its test, a root layout,
`tokens.toml`, the generated `*_hx.go` files, `migrations/` with Loom's
own tables (embedded, so `myapp migrate` applies them), a `Dockerfile`,
and files for agents (`AGENTS.md`, `CLAUDE.md`, `.mcp.json`,
`.claude/skills/loom/SKILL.md`). See `getting-started.md` and
`deploy.md`.

- `--module` is the module path (default: `DIR`'s base name). The
  package is its last element as a Go name (`hello-world` gives
  `helloworld`); `main` is in `cmd/<last element>`.
- `go.mod` gets Loom one of three ways, reported as `loom`: `"replace"`
  with `--loom DIR`, a checkout of Loom (go.sum is seeded from its
  go.sum); `"version"`, the version this `loom` was installed at; or
  `"missing"` when `loom` has none (built from source), with a warning
  to run `go get github.com/moomerman/loom@main`.
- With `"version"`, it then runs `go mod tidy` in `DIR`. If that fails
  (no network, no `go`), the app is still written: the failure is a
  warning and `go mod tidy` is added to `next`. With `"replace"`, go.mod
  lists Loom's own requirements instead, so the app builds offline.

Exit status: 0 when written, 1 if a template had errors (a bug), 2 for
usage errors, a non-empty `DIR`, or I/O errors.

With `--json`:

```json
{
  "dir": "myapp",
  "module": "myapp",
  "loom": "version",
  "files": [".claude/skills/loom/SKILL.md", ".gitignore", ".mcp.json", "AGENTS.md", "app.go", "..."],
  "diagnostics": [],
  "warnings": [],
  "next": ["cd myapp", "loom dev"]
}
```

| Field | Meaning |
|---|---|
| `files` | every file written, relative to `dir`, sorted |
| `diagnostics` | as for `loom check`; always empty unless something is broken |
| `warnings` | what still needs doing, in plain English |
| `next` | commands to run, in order, from where `loom new` ran |

## loom docs

```
loom docs [--json] [TOPIC]
```

Loom's docs, embedded in the command, so agents can read them outside
this repository. Without `TOPIC`, lists the topics in reading order (the
order of `llms.txt`); with one, such as `syntax`, prints its Markdown.
An unknown topic exits 2 and names the topics.

With `--json`, the list is:

```json
{"topics": [
  {"name": "getting-started", "title": "Getting started", "summary": "start a new app with `loom new`: the files it makes, the agent files, the loop"},
  {"name": "syntax", "title": "Template syntax", "summary": "the complete template language"}
]}
```

and a topic is `{"name", "title", "summary", "content"}`, `content`
being its Markdown.

## loom check

```
loom check [--json] [path ...]
```

Parses and type-checks every `.hx` file under the given paths (default
`.`). Templates in one directory form one package and are checked
together, against the `.go` files in that directory. Like the go tool, it
skips directories named `testdata` or starting with `.` or `_`.

Exit status: 0 when all templates are valid (warnings allowed), 1 when
there are errors, 2 for usage or I/O errors.

With `--json`, stdout is a JSON array of diagnostics (empty when all is
well):

```json
[
  {
    "code": "PA219",
    "severity": "error",
    "file": "templates/todo.hx",
    "line": 4,
    "col": 3,
    "message": "empty expression",
    "fix": "put an expression between the braces, or remove them"
  }
]
```

| Field | Type | Meaning |
|---|---|---|
| `code` | string | Stable error code, documented in `errors.md` |
| `severity` | string | `"error"`, or `"warning"` for a diagnostic that doesn't fail the build (TC336, ST705, ST706) |
| `file` | string | Path as found from the given path |
| `line`, `col` | int | 1-based; `col` counts characters |
| `message` | string | What is wrong |
| `fix` | string | How to fix it |

## loom build

```
loom build [--json] [path ...]
```

Checks templates like `loom check`, then writes the generated Go file next
to each template (`todo_list.hx` -> `todo_list_hx.go`). Files whose
contents haven't changed are not rewritten. Nothing is written if there
are any errors; warnings don't stop the build. Exit status as for `loom check`.

With `--json`, stdout is an object:

```json
{
  "written": ["templates/todo_list_hx.go"],
  "diagnostics": []
}
```

| Field | Type | Meaning |
|---|---|---|
| `written` | array of string | Files written, in order; empty if none changed |
| `diagnostics` | array | As for `loom check --json` |

## loom test

```
loom test [--json] [--race] [--run REGEXP] [PACKAGE ...]
```

Builds the templates under `.` like `loom build`, then runs `go test` on
the packages (default `./...`). Generated `*_hx.go` files from before an
edit still compile, so `go test` alone can pass against stale templates;
`loom test` can't. With template errors no tests run. Without `--json`,
go test's own output is printed. `--race` runs them with the race
detector (the MCP tool's `race`). Exit status: 0 when everything passed,
1 for template errors or failed tests, 2 for usage errors.

With `--json`:

```json
{
  "passed": false,
  "written": ["templates/home_hx.go"],
  "diagnostics": [],
  "packages": [
    {"package": "example.com/app", "result": "fail", "elapsed": 0.2,
     "failures": [{"test": "TestAdd", "output": "    app_test.go:9: got 1, want 2\n"}]},
    {"package": "example.com/app/templates", "result": "skip", "elapsed": 0, "failures": []}
  ]
}
```

| Field | Meaning |
|---|---|
| `passed` | no template errors, and every package passed |
| `written`, `diagnostics` | as for `loom build --json` |
| `packages[].result` | `pass`, `fail`, `skip` (no tests) or `build-fail` (it didn't compile, or `TestMain` failed; the one failure has `test` `""` and the compiler's output) |
| `packages[].failures` | each failed test with its output (the last 8 KB) |
| `output` | go's output that isn't a package's, such as a bad package pattern; omitted when empty |

## loom dev

```
loom dev [--json] [--addr localhost:4000] [--watch DIR]... [--db URL] [--migrations DIR] [PACKAGE]
```

Runs the app in `PACKAGE` for development. With no `PACKAGE` it finds
the app: `.` if that is a main package, else the main package in
`cmd/*` (Go's convention, and what `loom new` makes): the one with a
`//loom:dev` line, else the only one, else the only one that calls
`loom.New` and `Run`. When it can't tell, it says so (RT451) and lists
them; mark one with `//loom:dev`, or name it.
It serves on `--addr`, which defaults to `$LOOM_DEV_ADDR`, else
`localhost:$PORT`, else `localhost:4000`; so `PORT=5000 loom dev` serves
on `http://localhost:5000`.
It watches the `.hx`, `.go`, `tokens.toml` and behavior `.js` files under
each `--watch` directory (default `.`), and on a change it runs `loom build` on them, `go build`s
the app, restarts it and remounts every open page, keeping form input
(a page whose root layout changed reloads). A change takes about a
second to reach the browser.

Before each build, if `--db` (default `$DATABASE_URL`) is set and the
`--migrations` directory (default `migrations`) exists, it applies
pending migrations, and it also rebuilds when a `.sql` file there
changes. Production apps are migrated only by `loom migrate`.

On a migration, template or Go error the previous build keeps running and open pages
show the errors in an overlay until a build succeeds. The app's own output
goes to stderr.

The app runs with `LOOM_DEV=1`, which turns on dev mode (`Config.Dev`; the endpoints
below), `LOOM_DEV_TOKEN`, a secret made for the run, and `LOOM_ADDR`,
which `App.Run` listens on instead of its argument. `loom dev` serves
the app through a proxy, so the address stays the same across restarts;
the proxy adds the overlay script to HTML pages, and the token to every
request as `X-Loom-Dev-Token`. The app's dev endpoints and `DevOnly`
pages refuse requests without it, so they work through `loom dev`'s
address only. `LOOM_DEV=1` without a token is RT439 at startup, and dev
mode on an address other machines reach is RT440.

With `--json`, stdout has one JSON object per line:

| `event` | Fields | Meaning |
|---|---|---|
| `serving` | `url`, `package` | the proxy is listening; `package` is the main package it runs |
| `building` | `changed` (paths) | a build started; `changed` is empty for the first build |
| `migrated` | `applied` (paths) | pending migrations were applied |
| `ready` | `ms` | the new build is running; pages remount |
| `error` | `kind`, `output`, `diagnostics` | the build or the app failed |

`kind` is `migration` (`output` is the DB6xx error), `template` (with
`diagnostics` as for `loom check --json`),
`go` (`output` is the go compiler's output) or `exit` (the app exited;
`output` is the end of its output). `output` is always the errors as
text.

Exit status: 0 on interrupt, 2 if it can't start (e.g. the address is in
use, or RT451).

### loom status

```
loom status [--json] [--wait]   GET /_loom/dev/status
```

Asks `loom dev` (at `--url`) for its build state, so an agent can save a
file and ask whether the app rebuilt. `--wait` first waits for the
rebuild of a change saved just before to finish. With `--json`:

```json
{"state": "error", "running": true, "builds": 3,
 "last": {"event": "error", "kind": "template", "diagnostics": [...], "output": "..."}}
```

`state` is `building`, `ready` or `error`; `running` says whether an app
is serving (after an error, the last good build keeps running); `builds`
counts finished rebuilds; `last` is the last `ready` or `error` event,
as above, or null before the first build ends.

## loom doctor

```
loom doctor [--json] [--url URL] [--db URL] [--dir DIR] [path]
```

Runs the checks an agent would otherwise run one by one, in the order to
fix them, each with a fix: the templates type-check and their generated
code is current (`loom build`), `DATABASE_URL` reaches Postgres and no
migration is pending or edited, Chrome and `.mcp.json` are found, and,
if the app answers at `--url`, its `/_loom/health` and, in dev, its
startup checks (`app.Validate`, via `GET /_loom/dev/validate`) and
that no table is read mostly by large sequential scans (`scans`, from
`loom db stats`). Exits 1
if any check fails. A check that couldn't run (no app, no database) is
`ok` with a message saying so; Chrome and `.mcp.json` are advice, never
failures.

```json
[{"check": "templates", "ok": false, "message": "generated code is out of date: page_hx.go", "fix": "run `loom build .` and commit the result"},
 {"check": "app", "ok": true, "message": "ok: database ok, jobs running, 2 live pages"}]
```

## Asking a running app

These commands ask an app running in dev mode (under `loom dev`, or with
dev mode on) about itself, through its `/_loom/dev/` endpoints. They
take `--url` (default `$LOOM_URL`, else `http://localhost:$PORT`, else
`http://localhost:4000`) and
`--json`, which prints the endpoint's JSON as is.

Exit status: 0 on success; 1 when the app answers with an error, printed
(or with `--json`, printed as `{"code", "message", "fix"}` on stdout);
2 when no dev-mode app answers, or for usage errors.

The endpoints answer only requests from the local machine and don't exist
unless dev mode is on (`Config.Dev`), so they are never on in production.

A deployed app answers some of them to a request carrying its inspect
token (`Config.InspectToken`, default `$LOOM_INSPECT_TOKEN`, at least
32 characters): pass the same token with `--token` (default
`$LOOM_INSPECT_TOKEN`; `loom mcp` and `loom doctor` read the variable)
and the app's address with `--url`:

```
LOOM_INSPECT_TOKEN=... loom queries --url https://app.example.com
```

The token reads what the app does and how it performs: `loom routes`,
`manifest`, `sessions`, `errors`, `events` (and `trace`), `stats`,
`queries`, `db stats`, the heap and goroutine profiles, `jobs` and `jobs inspect`, `queues`, `nodes`, `cron`, and
`doctor`'s app checks. Everything that shows a page's state (`loom
inspect`'s assigns and HTML), renders, sends an event, reads the
mailbox or storage, or changes a job answers 403 to it: those stay in
dev. Events can carry URLs and user ids, so keep the token as secret
as the database's.

### loom routes

```
loom routes [--json]          GET /_loom/dev/routes
```

```json
[
  {"path": "/", "page": "*todo.TodoPage", "component": "templates.TodoApp",
   "root_layout": "templates.Root", "layout": "templates.App",
   "prefix": "", "mount": "", "auth": [], "middleware": 0, "watch": ["todos"], "rate_limit": []},
  {"path": "/admin/jobs/jobs/{id}", "page": "*jobsweb.JobPage", "component": "jobsweb.Job",
   "root_layout": "jobsweb.Root", "layout": "jobsweb.Shell",
   "prefix": "/admin/jobs", "mount": "*jobsweb.App", "auth": ["todo.requireAdmin"], "middleware": 0, "watch": [], "rate_limit": []}
]
```

`page` is the page's Go type. `root_layout` is `loom.DefaultRoot` when the app sets none; `layout` is
`""` for a route outside any group with an app layout. `path` is the full
path, with any mount prefix; `prefix` and `mount` are the mount prefix
and the mounted sub-app's Go type (`""` outside mounts); `auth` names the
route's `loom.Auth` checks, outer first; `watch` names the tables whose
changes re-render it (`loom.Watch`); `rate_limit` describes its rate
limits, outer first, e.g. `"5 per minute by email on sign_in"`
(`loom.RateLimit`); `middleware` counts the
`loom.Middleware` wrapping it. See `subapps.md`.

### loom manifest

```
loom manifest [--json]        GET /_loom/dev/manifest
```

What the app does, for review: the text of `loom.manifest`, or with
`--json` the `loom.Manifest` value (see `manifest.md`):

```json
{
  "routes": [{"path": "/todos/{id}", "page": "*todo.TodoDetailPage", "component": "templates.TodoDetail",
    "layout": "templates.App", "auth": [], "rate_limit": [], "handler": "(*todo.TodoDetailPage).Events",
    "events": [{"name": "toggle_todo", "on": ["click"], "vals": ["id"], "at": ["todo_app.hx:66:90"]}],
    "watch": ["todos"], "params": "", "keeps": [], "dev_only": false}],
  "layouts": [], "handlers": [],
  "mounts": [{"prefix": "/admin/jobs", "type": "*jobsweb.App", "auth": ["todo.requireAdmin"], "routes": ["/", "/jobs", "/jobs/{id}", "/cron"], "rate_limits": [], "dev_only": false}],
  "jobs": {"mode": "run", "types": [{"type": "todo.PruneCompleted", "queue": "default", "max_attempts": 20, "timeout": "1m0s", "args": []}],
    "queues": [{"name": "default", "concurrency": 10}],
    "cron": [{"expr": "0 3 * * *", "type": "todo.PruneCompleted", "args": "{}"}]},
  "migrations": [], "flags": []
}
```

`page` is the Go type whose `Events` handle the page's events, named in
`handler`. `events` are what the page's own markup sends by name, `at`
their template positions; an app layout's are under `layouts`, with a
`handler` for a live layout. An event of the page's markup that its
live layout handles has a `handler` of its own. `watch`
names the tables whose changes re-render the page, and `keeps` the
fields the page keeps across a remount (`Keep`; a live layout's are
its `keeps` under `layouts`). `rate_limit` (a
route's or handler's) and `rate_limits` (a mount's: `"every route: "`
and a limit on all its routes, else the route and the limit) describe
the `loom.RateLimit`s. `dev_only` is true
for a route or mount served only in dev (`loom.DevOnly`). `migrations` are
filled from the migrations directory by `loomtest.Manifest`; the dev
server leaves them empty.

### loom diff

```
loom diff [--json] [BASE[..HEAD]]     (git; no app)
```

The change to the app's behaviour between two revisions, from their
`loom.manifest` files (see `manifest.md`). `BASE..HEAD` compares from
the merge base of the two, as a pull request does; `BASE` alone
compares with the working tree. The default base is `origin/main`, or
`main` without it.

```json
{
  "base": "0232b0e38c7a771f219d402c600fc74acc0fe8f4",
  "head": "",
  "files": [{"path": "examples/todo/loom.manifest", "status": "modified", "sections": [
    {"section": "routes", "added": [], "removed": [], "changed": [
      {"item": "/  live page templates.TodoApp in templates.App",
       "removed": [],
       "added": ["  archive_todo (on click, val-id)"]}]}]}]
}
```

`head` is `""` for the working tree. `status` is `added`, `removed` or
`modified`. An item is the first line of a route, migration, job
or flag, matched across revisions by its path or name;
added and removed items carry their `lines`, changed ones the lines
`removed` and `added`, and `was` when the first line itself changed.
Exit status 2 for an unknown revision or outside a git repository.

### loom sessions

```
loom sessions [--json]        GET /_loom/dev/sessions
```

One entry per open page load, oldest first:

```json
[{
  "sid": "944687e59995efaa68154f1e2431ae26",
  "path": "/",
  "url": "/?filter=active",
  "page": "*todo.TodoPage",
  "version": 3,
  "connected": 1,
  "idle_seconds": 0,
  "restarts": 0,
  "busy_seconds": 0,
  "started_at": "2026-10-10T14:00:03Z"
}]
```

| Field | Meaning |
|---|---|
| `path` | the route's pattern, e.g. `/team/{key}` |
| `url` | the page's current URL: where it was loaded, or the last URL it patched to (`c.Patch`, `live.md`) |
| `version` | the page's render version (see `wire.md`) |
| `connected` | open SSE streams; 0 when the browser is gone |
| `idle_seconds` | seconds since the last stream closed; 0 while connected. The session stops after `Config.SessionTimeout` |
| `restarts` | restarts after errors in the last minute |
| `busy_seconds` | how long the handler now running has run; 0 when idle. Over 10 seconds is RT445 |

### loom inspect

```
loom inspect [--json] SID     GET /_loom/dev/sessions/SID
```

A session's fields as above, plus `component`, `assigns` (the current
assigns, with template field names: `{"todos": [{"id": "1", "title":
"Try Loom", "done": false}], ...}`), `layout_assigns` (the same for
the page's live layout, absent without one; see `layouts.md`), `html`
(the current render) and `timers` (the page's pending `c.SendAfter`
timers, soonest first: `[{"event": "save", "payload": {}, "in_ms":
1840}]`), `topics` (the PubSub topics the page and its live layout
subscribe to) and `watch` (the tables its route watches). An unknown SID
is RT404. `GET /_loom/dev/topics` maps each topic the open pages
subscribe to to their session ids (the `topics` MCP tool).

### loom event

```
loom event [--json] SID EVENT [KEY=VALUE ...]
                              POST /_loom/dev/sessions/SID/event {"event", "payload"}
```

Sends an event to a session, as its browser would: `loom event SID add
title=Milk` is a submit of a form with a `title` field. Open browsers get
the diff as usual. Prints the errors the event caused, then the page's
assigns and HTML. With `--json`, the session's fields as for `loom
inspect`, plus `errors` (as for `loom errors`, those of this session
logged while handling the event: RT402 for a panic, which restarts the
page, RT419 for `c.Fail`), `ended` (true when the session ended: an
auth check failed, or the page failed too often), `trace` (the event's
trace id, for `loom trace` once its jobs have run) and `events` (what it
did while it ran: its `page.event` with `ms` and `bytes`, commits, jobs
enqueued, broadcasts; `events.md`). An unknown SID is RT404.

### loom render

```
loom render [--json] [--assigns FILE|-] COMPONENT
                              POST /_loom/dev/render?component=COMPONENT
```

Renders any component of the app with assigns from a JSON file (`-` for
stdin; default `{}`), and prints the HTML. `COMPONENT` is a name such as
`Footer`, or `templates.Footer` if two packages have one. Assigns use
template names for every field, including nested ones, and missing fields
are zero values:

```
echo '{"remaining": 1, "filter": "all"}' | loom render --assigns - Footer
```

With `--json`: `{"component": "templates.Footer", "html": "..."}`. Errors
are RT408 (unknown component) and RT409 (assigns don't fit).

### loom errors

```
loom errors [--json]          GET /_loom/dev/errors
```

The app's last 50 runtime errors, oldest first: the server's (panics,
`c.Fail`, failed job attempts) and, in dev, the browser's. In dev,
`loom.js` reports a page's uncaught errors, rejected promises and
`console.error` calls, including RT417 and RT418 from behaviors and
events the server refused, to `POST /_loom/dev/errors`
(same-origin only).

```json
[{
  "time": "2026-10-10T14:02:11Z",
  "source": "server",
  "sid": "944687e59995efaa68154f1e2431ae26",
  "code": "RT402",
  "message": "*todo.TodoPage.Events()[\"add\"] panicked: ...",
  "where": "/app/todo.go:41 (example/todo.(*TodoPage).add)",
  "fix": "fix the panic at the location shown; the page was restarted with a fresh Mount"
}]
```

`source` is `server` or `browser`. A browser error also has `url`, the
page's address; `where` is the script's file, line and column when the
browser knows them. One with no Loom code is RT420. A server error in a
page has `route`, and one in an action (a click, a job) its `trace`.
These are the `error` events of the stream (`loom events --name
error`).

### loom events

```
loom events [--json] [--since N] [--name NAME] [--sid SID] [--trace ID] [--level L] [--follow]
                              GET /_loom/dev/events?since=&name=&sid=&trace=&level=[&follow=1]
```

What the app did, oldest first: the last 2000 events of its stream, one
line each (`events.md` lists every name and attribute). `--name` takes
a name or a group (`job.*`), `--level` debug, info, warn or error.
`--follow` keeps printing as they happen (with `--json`, one event's
JSON per line; the endpoint sends them as Server-Sent Events).

```json
{"events": [{
  "seq": 812,
  "time": "2026-10-13T14:02:13.88Z",
  "name": "page.event",
  "level": "DEBUG",
  "trace": "1a7f3c9e02d4-3",
  "attrs": {"sid": "944687e5...", "route": "/", "event": "add", "ms": 1.4, "diff": true, "bytes": 212, "outcome": "ok"}
}], "next": 815}
```

`next` is the cursor: pass it as `--since` to see only what happened
after this call, as an agent does around an action.

### loom trace

```
loom trace [--json] ID        GET /_loom/dev/events?trace=ID
```

Every kept event of one action, with each one's offset from the first:
a click's handler, commits, enqueued jobs, their attempts and
broadcasts, and the re-renders those caused. The JSON is as for `loom
events`.

### loom stats

```
loom stats [--json]           GET /_loom/dev/stats
```

How often each event happened since the app started: by name, and, for
these names, by what they concern: `page.*` and `limit.refused` by
route, `job.*` and `cron.tick` by job type, `watch.notify` by table.
So a route whose watch re-renders hundreds of times a minute, or a job
type that dominates the queue, is one line. Each has its errors, its
count in the last minute (`last_minute`, the current rate), and for
those that time something the total, mean, p95 (within 25%) and max
`ms`. `queries` totals the database statements the events ran (their
`queries` attribute), so the table's QUERIES EACH column is the
queries per page action or job attempt. After 200 keys for one name,
the rest count as `other`.

`runtime` is the process now: goroutines, the heap, the garbage
collector's runs and its longest recent pause, the browsers connected
(`streams`) and the database pool (absent without a database). A
number that keeps growing between calls is a leak; `waits` growing
means the pool is short of connections (`Config.DBMaxConns`).

Dev mode also serves Go's profiler at `/_loom/dev/pprof/`:
`go tool pprof http://localhost:4000/_loom/dev/pprof/profile` for 30
seconds of CPU, and `heap`, `allocs`, `goroutine`, `block`, `mutex`
and `trace`. The inspect token reads `heap`, `allocs` and
`goroutine`; a CPU profile or trace costs while it runs, so those stay
in dev.

```json
{"started": "2026-10-13T14:00:00Z", "sessions": 3,
 "runtime": {"goroutines": 41, "heap_bytes": 9123840, "heap_objects": 51200, "sys_bytes": 24117248,
   "gc_runs": 18, "gc_pause_ms": 0.3, "streams": 3,
   "db": {"max": 20, "open": 4, "in_use": 1, "idle": 3, "waits": 0, "wait_ms": 0}},
 "events": [
  {"name": "page.event", "by": "route", "key": "/team/{key}", "errors": 0, "queries": 82, "count": 41, "last_minute": 12,
   "timed": 41, "total_ms": 73.8, "mean_ms": 1.8, "p95_ms": 4.6, "max_ms": 12.4}
]}
```

### loom queries

```
loom queries [--json]         GET /_loom/dev/queries
```

Every SQL statement the app's database ran since the app started, by
its text on one line (arguments are bound, so this is the statement's
shape, never its values), the busiest first by total time. Loom's own
statements are here too: the job runner's fetch and rescue, the node
heartbeat, sessions and rate limits. The table shows the top 25.

```json
{"started": "2026-10-13T14:00:00Z", "queries": [
  {"query": "UPDATE loom_jobs SET state = 'executing', ...", "rows": 3, "errors": 0, "count": 4210, "last_minute": 720,
   "timed": 4210, "total_ms": 9262.0, "mean_ms": 2.2, "p95_ms": 4.1, "max_ms": 31.0}
]}
```

`rows` is what queries returned and statements changed, in all (the job
store's queries count 0). `last_minute` is the rate: 720 is twelve a
second. A line near the top whose `mean_ms` is small is a query that
runs too often; one whose `rows` per call is large reads too much.
After 500 distinct statements, the rest count as `other`. Use `loom db
query` with `EXPLAIN` to see how one runs.

### loom mail

```
loom mail [--json]            GET /_loom/dev/mail
```

The last 50 mails the app sent, oldest first, when it runs in dev with
no mailer (`app.UseMailer`): in dev, `app.Mailer()` keeps mail here and
logs a line instead of sending it, so a sign-in or password-reset link
can be followed without a mail service. `devmail` shows the same in the
browser.

```json
[{
  "id": 3,
  "time": "2026-10-12T14:02:11Z",
  "to": "ada@example.com",
  "subject": "Sign in to Loom Issues",
  "text": "Sign in to Loom Issues by opening this link: ..."
}]
```

`id` counts up from 1 and is never reused, even after the mailbox is
cleared; `html` is there too when the mail has an HTML body.

### loom storage

```
loom storage [--json] [--after KEY] [--limit N] [PREFIX]
                              GET /_loom/dev/storage?prefix=&after=&limit=
```

The files in the app's bucket (`app.Storage()`, see `storage.md`) whose
keys start with PREFIX, in key order, up to `--limit` (100). When a page
is full the human output ends with the command for the next one.

```json
{"bucket": "dir .loom/storage", "objects": [
  {"key": "tunes/0f3a...", "size": 52144, "etag": "\"9e107d9d372bb6826bd81d3542a419d6\"", "modified": "2026-10-15T20:51:07Z"}
]}
```

`bucket` says where the files are (`s3 https://host/bucket`, `dir
PATH`, `memory`, or `none` with no storage outside dev); `objects` is
`[]` when nothing matches. An invalid prefix is SG006 (status 1), and a
bucket that fails is the error's code and fix.

## Background jobs

These ask the app about its jobs (see `jobs.md`), with the same `--url`,
`--json` and exit statuses as the commands above. Times are RFC 3339 in
UTC; `null` means not yet.

### loom jobs

```
loom jobs [--queue Q] [--state S] [--type T] [--search TEXT] [--before ID] [--limit N] [--json]
                              GET /_loom/dev/jobs?queue=&state=&type=&search=&before=&limit=
```

Jobs, newest first (default limit 100). `--search` keeps jobs whose args
contain the text, ignoring case. `--before` keeps ids below it: pass the
last id shown to get the next page (keyset pagination, so pages stay fast
on big tables). Each is a job record:

```json
[{
  "id": 7,
  "type": "todo.PruneCompleted",
  "queue": "default",
  "args": {},
  "state": "retryable",
  "attempt": 1,
  "max_attempts": 20,
  "inserted_at": "2026-10-10T03:00:00.001Z",
  "scheduled_at": "2026-10-10T03:00:15.912Z",
  "attempted_at": "2026-10-10T03:00:00.004Z",
  "finished_at": null,
  "attempted_by": "web-1-4123-a1b2c3",
  "lease_until": null,
  "errors": [{"attempt": 1, "at": "2026-10-10T03:00:00.010Z", "code": "JB505", "error": "Perform panicked: ...", "stack": "goroutine 12 ..."}],
  "unique_key": "5d41402abc4b2a76b9719d911017c592"
}]
```

`state` is one of `scheduled`, `available`, `executing`, `completed`,
`retryable`, `discarded`, `cancelled`. `attempt` counts attempts started
(snoozes don't count). For a retryable or scheduled job, `scheduled_at` is
when it runs next. `errors` has one entry per failed attempt; `code` is set
for panics (JB505), timeouts (JB506), unknown types (JB501) and rescues
(JB511), and `stack` for panics. `unique_key` is empty unless the job was
enqueued with `jobs.Unique`.

### loom jobs inspect

```
loom jobs inspect [--json] ID     GET /_loom/dev/jobs/ID
```

One job record, plus `wait_seconds` (from `scheduled_at` to the latest
attempt) and `run_seconds` (the finished job's latest attempt), each
`null` when not applicable. An unknown id is JB507.

### loom jobs retry, loom jobs cancel

```
loom jobs retry [--json] ID       POST /_loom/dev/jobs/ID/retry
loom jobs cancel [--json] ID      POST /_loom/dev/jobs/ID/cancel
```

Retry makes a scheduled, retryable, completed, discarded or cancelled job
available now, with one more attempt if it had none left. Cancel stops a
job that hasn't finished; a running job's `ctx` is cancelled if it runs
on the app answering. Both answer the job as `loom jobs inspect` does.
JB507 for an unknown id, JB508 when the state doesn't allow it.

### loom jobs delete, loom jobs retry-discarded

```
loom jobs delete [--json] ID      POST /_loom/dev/jobs/ID/delete
loom jobs retry-discarded [--queue Q] [--json]
                              POST /_loom/dev/jobs/retry-discarded?queue=Q
```

Delete removes a job that isn't running and answers `{"id": 7,
"deleted": true}`; JB507 for an unknown id, JB508 for a running job
(cancel it first). Retry-discarded retries every discarded job, in one
queue or all, and answers `{"retried": 3}`.

### loom jobs enqueue

```
loom jobs enqueue [--args FILE|-] [--in DURATION] [--json] TYPE
                              POST /_loom/dev/jobs?type=TYPE&in=DURATION (body: args)
```

Enqueues a job of a registered type (`PruneCompleted` or
`todo.PruneCompleted`) with JSON args by json tag name (default `{}`),
optionally after a delay. Answers `{"id": 8, "duplicate": false,
"skipped": false}`; `duplicate` is true, with the existing job's id, for a
unique job that already exists. JB501 for an unknown type, JB509 for args
that don't fit.

### loom queues

```
loom queues [--json]          GET /_loom/dev/queues
```

```json
[{
  "name": "default",
  "concurrency": 10,
  "running": 1,
  "states": {"scheduled": 0, "available": 4, "executing": 1, "completed": 120, "retryable": 2, "discarded": 0, "cancelled": 0},
  "completed_last_minute": 31,
  "failed_last_minute": 2,
  "oldest_available_seconds": 4
}]
```

`concurrency` and `running` are per node (the app answering); `states`
counts the store's jobs across the cluster. The last two count attempts
finished on this node in the last minute: its throughput.
`oldest_available_seconds` is how long the queue's oldest due job has
waited for a worker, cluster wide (0 when none waits): a number that keeps
growing means a stuck queue. Queues with jobs but no workers here are
listed with concurrency 0. `loom nodes` shows every node's workers.

### loom nodes

```
loom nodes [--json]           GET /_loom/dev/nodes
```

```json
[{
  "name": "web-1-4211-a3f09c",
  "queues": [{"name": "default", "concurrency": 10, "running": 3}, {"name": "mail", "concurrency": 5, "running": 0}],
  "leader": true,
  "started_at": "2026-10-13T09:12:04Z",
  "seen_at": "2026-10-13T10:40:31Z",
  "lease_until": "2026-10-13T10:41:01Z",
  "state": "up",
  "self": true
}]
```

The node registry (`jobs.md`): every node running jobs, by name, with
its queues' workers (`running` of `concurrency`) at its latest
heartbeat, which it sends every third of its lease. `state` is `up`, or
`stale` once `lease_until` has passed without a heartbeat: the node
crashed, hung or lost the database, and its jobs are rescued. `leader`
marks the node running cron and rescue; a stale node is never the
leader. `self` is the node answering. Enqueue-only nodes aren't listed;
a node that shuts down cleanly removes itself, and a stale one is
deleted an hour after its lease ended (`NodeRetention`).

### loom cron

```
loom cron [--json]            GET /_loom/dev/cron
```

```json
[{
  "index": 1,
  "expr": "0 3 * * *",
  "type": "todo.PruneCompleted",
  "args": {},
  "builtin": false,
  "next_run": "2026-10-11T03:00:00Z",
  "last_tick": "2026-10-10T03:00:00Z",
  "last_job": 7,
  "last_state": "completed"
}]
```

`builtin` marks the hourly `jobs.Prune` entry. `last_tick`, `last_job`
and `last_state` describe the last tick this node enqueued as leader;
they are `null` and `""` on other nodes and before the first tick.
`index` names the entry for `loom cron run`. Schedules are UTC; the
table prints the next and last runs in your local time, and the JSON
in UTC.

### loom cron run

```
loom cron run [--json] INDEX      POST /_loom/dev/cron/INDEX/run
```

Enqueues the entry's job now, as an ordinary job outside the schedule,
and answers as `loom jobs enqueue` does. JB515 for an unknown index.

## Database

These commands talk to Postgres directly; they don't need a running app.
They take `--db` (default `$DATABASE_URL`, the variable apps pass to
`loom.Config`), `--dir` (the migrations directory, default `migrations`)
and `--json`. See `data.md`.

Exit status: 0 on success; 1 for a data error (DB6xx), printed (with
`--json`, as `{"code", "message", "fix"}` on stdout), and for `migrate
status` with problems; 2 for usage errors, a missing database URL, and
other database errors (printed on stderr).

### loom db new

```
loom db new [--json] [--dir DIR] NAME
```

Creates `DIR/NNNN_NAME.sql`, numbered after the highest existing
migration, with a comment header. `NAME` is lowercase letters, digits and
underscores.

```json
{"file": "migrations/0002_add_due_dates.sql", "version": 2, "name": "add_due_dates"}
```

### loom db install

```
loom db install [--json] [--dir DIR] PACKAGE
```

Copies a sub-app package's migrations (its `migrations/` directory,
found with `go list`, so run it in the app's module) into `DIR`,
numbered after the app's own as `NNNN_<package>_<name>.sql` (a name
already starting with the package's keeps it), each starting with
`-- loom:from PACKAGE FILE`. Ones already installed are skipped, so
after upgrading the package it installs only the new ones.
`github.com/moomerman/loom` installs Loom's own tables.
Then run `loom migrate`, and commit the copies. Needs no database.

```json
{"package": "example.com/notes", "created": [{"file": "migrations/0004_notes_notes.sql", "version": 4, "name": "notes_notes"}]}
```

### loom migrate

```
loom migrate [--json] [--db URL] [--dir DIR]
```

Applies pending migrations in order, each in a transaction, under an
advisory lock (see `data.md`). Refuses to run if an applied migration was
edited (DB614).

```json
{"applied": [{"version": 1, "name": "create_todos", "file": "0001_create_todos.sql",
  "checksum": "9f2c…", "track": ["todos"], "ms": 18}]}
```

`applied` is empty when nothing was pending. If a migration fails, those
before it stay applied and are listed on stderr. An app's own binary
runs the same command on its embedded migrations, `myapp migrate
[status] [--json] [--db URL]` (`loom.MigrateCommand`; `deploy.md`),
with the same output and exit codes.

### loom migrate status

```
loom migrate status [--json] [--db URL] [--dir DIR]
```

```json
{
  "migrations": [
    {"version": 1, "name": "create_todos", "file": "0001_create_todos.sql", "checksum": "9f2c…",
     "track": ["todos"], "state": "applied", "applied_at": "2026-10-10T16:15:02Z"},
    {"version": 2, "name": "add_due_dates", "file": "0002_add_due_dates.sql", "checksum": "41ab…",
     "state": "pending"}
  ],
  "pending": 1,
  "problems": []
}
```

`state` is `applied`, `pending`, `changed` (edited since it was applied;
also listed in `problems` as DB614) or `missing` (applied, but the file is
gone; not a problem). `problems` are `{"code", "message", "fix"}`.

### loom db schema

```
loom db schema [--json] [--db URL]
```

The tables of the current schema, leaving out Loom's `loom_*` tables.

```json
{"tables": [{
  "name": "todos",
  "columns": [
    {"name": "id", "type": "bigint", "nullable": false, "default": "nextval('todos_id_seq'::regclass)"},
    {"name": "title", "type": "text", "nullable": false},
    {"name": "version", "type": "bigint", "nullable": false}
  ],
  "primary_key": ["id"],
  "tracked": true
}]}
```

`type` is as Postgres formats it (`timestamp with time zone`,
`character varying(80)`); `default` is a SQL expression, omitted if none.

### loom db query

```
loom db query [--json] [--db URL] [--limit 100] SQL
```

Runs `SQL` in a read-only transaction with a 10-second statement timeout,
then rolls it back; writes fail. For development.

```json
{"columns": ["id", "title", "done_at"], "rows": [[1, "Try Loom", null]], "truncated": false}
```

Values are JSON numbers, strings, booleans, null, or RFC 3339 times.
`truncated` is true when there were more than `--limit` rows.

### loom db stats

```
loom db stats [--json] [--url URL] [--token TOKEN]
                              GET /_loom/dev/db/stats
```

Postgres's own counts, asked of the running app (so a deployed app's
inspect token works where the CLI can't reach its database): each table
in the schema with its rows, size, sequential scans and the rows they
read, index scans, inserts, updates and deletes, the most rows read by
sequential scans first; and, when the `pg_stat_statements` extension is
installed, its 25 busiest statements across every node.

```json
{"since": "2026-10-13T09:00:00Z", "tables": [
  {"name": "loom_jobs", "rows": 52000, "bytes": 18874368, "seq_scans": 41000, "seq_rows": 2132000000,
   "index_scans": 12, "inserts": 52000, "updates": 104000, "deletes": 0,
   "warning": "41000 sequential scans read about 52000 rows each, ..."}],
 "statements": [], "statements_note": "pg_stat_statements isn't installed: ..."}
```

`warning` is set when a table's sequential scans outnumber its index
scans and read 1000 rows or more each: a query on it has no index it
can use. `loom doctor` fails its `scans` check then. Find the query in
`loom queries` and see its plan with `loom db query "EXPLAIN ..."`.
The counts are Postgres's since `since`, its last reset.

## Components

```
loom components [--json]
loom components add [--dir DIR] [--json] NAME ...
```

The starter kit (see `components.md`). `loom components` lists its
components; with `--json`, an array:

```json
[
  {
    "name": "button",
    "summary": "A button with variants, sizes, a busy state and a click event",
    "files": ["button.hx"],
    "components": [
      {
        "name": "Button",
        "params": [
          {"name": "label", "type": "string", "required": true},
          {"name": "variant", "type": ":primary | :secondary | :danger | :ghost", "default": ":primary", "required": false},
          {"name": "on:click", "type": "event", "required": false}
        ]
      }
    ],
    "behaviors": []
  }
]
```

`files` lists the files `add` copies: the `.hx` file, then the modules of
the behaviors it declares. `behaviors` lists those behaviors like
`components`, each with `emits`, the names of the events it sends.

`add` copies each named component into `DIR` (default `components`),
copies the kit's `tokens.toml` next to `DIR` if no `tokens.toml` applies
there, and builds `DIR`. The app then owns the copies; nothing records
where they came from. If any file it would write already exists, it
writes nothing and exits 2. It exits 1 if the build has errors.

With `--json`, `add` prints:

```json
{
  "results": [
    {"name": "tabs", "file": "components/tabs.hx", "files": ["components/tabs.hx", "components/tabs.js"], "status": "added"},
    {"name": "tokens.toml", "file": "tokens.toml", "files": ["tokens.toml"], "status": "tokens"}
  ],
  "written": ["components/tabs_hx.go"],
  "diagnostics": []
}
```

| Field | Meaning |
|---|---|
| `status` | `added`, or `tokens` (the kit's `tokens.toml` was copied) |
| `file`, `files` | the component's `.hx` file; every file written |
| `written`, `diagnostics` | as for `loom build` of `DIR` |

## Previews

```
loom preview [--json]          GET /_loom/dev/previews
loom preview screenshot [--json] [--variant NAME] [--theme NAME] [--out DIR] [COMPONENT]
```

Ask a running dev app (`--url`, as above) about the previews of its
components (see `components.md`). `loom preview` lists each component's
variants, or why it has none; with `--json`, an array sorted by name:

```json
[
  {"component": "components.Button", "variants": ["default", "variant=danger", "busy"], "themes": ["light", "dark"]},
  {"component": "templates.TodoApp", "variants": [], "themes": ["light", "dark"], "no_preview": "todos is []Todo, which @preview can't give a value; preview a component that builds one"}
]
```

`loom preview screenshot` renders previews in headless Chrome
(`$LOOM_CHROME`, or Chrome or Chromium on the `PATH`) and writes PNGs to
`DIR` (default `previews`): for `COMPONENT` (`Button` or
`components.Button`), or every component with previews; all variants in
one image, or only `--variant`; in light, dark and each theme of its
tokens, or only `--theme`. Files
are named `<component>[.<variant>].<theme>.png`. With `--json`:

```json
{"screenshots": [
  {"component": "components.Button", "variant": "", "theme": "light", "file": "previews/components.Button.light.png", "width": 412, "height": 960}
]}
```

An unknown component, variant or theme, or no Chrome, exits 2 with the reason.

## loom mcp

```
loom mcp [--url URL] [--db URL] [--migrations DIR]
```

Serves the tools below to an agent over the Model Context Protocol, as
JSON-RPC 2.0 messages, one per line, on stdin and stdout. Configure it in
an MCP client as the command `loom mcp`. Each tool returns, as text, the
same JSON as the matching command with `--json`; an error the app answers
is returned with `isError` and the `{"code", "message", "fix"}` JSON.

| Tool | Arguments | Same as |
|---|---|---|
| `check` | `path` | `loom check --json` (needs no app) |
| `docs` | `topic` | `loom docs` (needs no app) |
| `build` | `path` | `loom build --json` (needs no app) |
| `test` | `packages` (array), `run` | `loom test --json` (needs no app) |
| `dev_status` | `wait` (boolean) | `loom status` (asks `loom dev`) |
| `components` | | `loom components` (needs no app) |
| `components_add` | `names` (array), `dir` | `loom components add` (needs no app) |
| `manifest` | | `loom manifest` |
| `manifest_diff` | `range` | `loom diff` (needs git, no app) |
| `routes` | | `loom routes` |
| `sessions` | | `loom sessions` |
| `inspect_session` | `sid` | `loom inspect SID` |
| `render_component` | `component`, `assigns` (object) | `loom render` |
| `previews` | | `loom preview` |
| `preview_screenshot` | `component`, `variant`, `theme` | `loom preview screenshot`, returning the PNGs as images |
| `send_event` | `sid`, `event`, `payload` (object of strings) | `loom event` |
| `recent_errors` | | `loom errors` |
| `events` | `since`, `name`, `sid`, `trace`, `level` | `loom events` |
| `trace` | `id` | `loom trace` |
| `stats` | | `loom stats` |
| `queries` | | `loom queries` |
| `topics` | | `GET /_loom/dev/topics` |
| `doctor` | `path` | `loom doctor` (asks the app if it runs) |
| `mail` | | `loom mail` |
| `storage_list` | `prefix`, `after`, `limit` | `loom storage` |
| `jobs_list` | `queue`, `state`, `type`, `search`, `before`, `limit` | `loom jobs` |
| `jobs_inspect` | `id` | `loom jobs inspect` |
| `jobs_retry` | `id` | `loom jobs retry` |
| `jobs_cancel` | `id` | `loom jobs cancel` |
| `jobs_delete` | `id` | `loom jobs delete` |
| `jobs_retry_discarded` | `queue` | `loom jobs retry-discarded` |
| `jobs_enqueue` | `type`, `args` (object), `in` | `loom jobs enqueue` |
| `queues` | | `loom queues` |
| `nodes` | | `loom nodes` |
| `cron` | | `loom cron` |
| `cron_run` | `index` | `loom cron run` |
| `db_schema` | | `loom db schema` (needs no app) |
| `migrate` | | `loom migrate` (needs no app) |
| `migrate_status` | | `loom migrate status` (needs no app) |
| `db_query` | `sql`, `limit` | `loom db query` (needs no app) |
| `db_stats` | | `loom db stats` |

The database tools use `--db` (default `$DATABASE_URL`) and
`--migrations` (default `migrations`); a data error is returned with
`isError` and its `{"code", "message", "fix"}` JSON.
