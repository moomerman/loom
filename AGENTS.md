# Working on Loom

Loom is a live-first web framework in Go: HEEx-like `.hx` templates
compiled to Go, and live server rendering over SSE with sparse diffs,
where a server restart never loses a user's work. Start with
`llms.txt`; `docs/design.md` has the choices that shape it.

This file is the guide for anyone changing Loom, agent or person.
`CONTRIBUTING.md` covers the process around a change.

## The pillar

Loom is an agent-first framework. Don't rebuild what exists; make a
framework that works well for agents, gives strong guarantees and
first-class tooling, and lets agents and humans collaborate on features
rather than humans having to think in code. Before building something,
ask:

1. **What guarantee or tool does this add for agents?** A mistake an
   agent can make should be caught by `loom check`, `go build`, startup
   or `loomtest`, with a code and a fix, never fail silently. The seams
   between template, Go, SQL, jobs and routes need this as much as each
   language does.
2. **Does a library already do it?** Build only what carries a guarantee
   Loom can't get otherwise (the template language, codegen, the job
   registry). Otherwise use what exists.
3. **Can a human review it as behaviour, not code?** Routes, events,
   watched tables, jobs and migrations should show up where a human can
   judge them (`loom.manifest`, previews, the dashboards) without reading
   Go or `.hx`.

## Commands

- `go test ./...`: all tests. `go test ./compiler ./compiler/parser ./compiler/check ./internal/... -update`
  rewrites golden files and snapshots; review the diff before committing.
- `go run ./cmd/loom check [--json] [path]`: check templates.
- `go run ./cmd/loom build [--json] [path]`: generate `*_hx.go` files.
  After changing the generator, run `go run ./cmd/loom build ./internal/hxtest`
  and review the generated diff.
- `go test -fuzz=FuzzParse ./compiler/parser`: fuzz the parser.
- `LOOM_TEST_DATABASE_URL="postgres://postgres:postgres@localhost:5432/loom_test?sslmode=disable" go test ./...`:
  everything, including the Postgres tests (data layer, jobs conformance
  suite, examples). The user needs CREATEDB: `loomtest.DB` creates
  `loomtest_*` databases. Without the variable the Postgres tests skip.
- `LOOM_TEST_S3=http://minioadmin:minioadmin@localhost:9000/loom-test go test ./storage`:
  the storage conformance suite on MinIO (or any S3-compatible service);
  the bucket is created if missing. Without the variable the S3 tests skip.
- `cd e2e && LOOM_CHROME=/path/to/chrome go test ./...`: headless browser
  tests (a separate module, so Loom needs no chromedp).
- `DATABASE_URL=postgres://... go run ./cmd/loom migrate --dir examples/todo/migrations`,
  then `DATABASE_URL=... go run ./examples/todo/cmd/todo`: the example app on :4000.
- `DATABASE_URL=... go run ./cmd/loom dev --migrations examples/todo/migrations ./examples/todo/cmd/todo`:
  the same, migrated, rebuilt and reloaded on every change; then
  `go run ./cmd/loom routes|sessions|inspect|render|errors|db schema`
  ask it about itself.

Before pushing, run what CI runs (`.github/workflows/ci.yml`): `go vet
./...`, `gofmt -l .`, staticcheck, the tests with Postgres, S3 and
`-race`, and the e2e tests.

## Layout

```
*.go (root)        package loom: the live runtime (App, Page, sessions, SSE)
client/            loom.js, the browser runtime, embedded in the binary; navigate.js, behaviors.js, dev.js
cmd/loom/          CLI
compiler/          Check, Build: find, parse, check and generate templates
  source/          files and positions
  diag/            errors: codes, excerpts, fixes, JSON
  lexer/ parser/   .hx -> AST
  ast/             target-neutral syntax tree
  goinfo/          reads structs and helpers from the Go files next to templates
  check/           type checker
  style/           tokens.toml, scoped CSS, raw-value and contrast checks
  gogen/           AST -> Go code
render/            render tree (statics + dynamics) used by generated code
diff/              wire encoding and sparse diffs of render trees (docs/wire.md)
loomtest/          browserless test helpers: Render, Snapshot, Mount, Jobs, DB, SignIn
data/              loom.DB, loom.Tx, Select/Get, migrations, change tracking (pgx)
form/              form.Form[T]: a typed form's input, validation and per-field errors (docs/forms.md)
pubsub/            in-process topic PubSub for pages and jobs
storage/           file storage: storage.Bucket on S3 (SigV4, stdlib), a directory or memory; Serve (docs/storage.md)
  storagetest/     conformance suite every storage.Bucket must pass
trace/             the event stream: named events, traces, ring, sinks (docs/events.md)
jobs/              background jobs: runner, memory store, cron (docs/jobs.md)
  jobstest/        conformance suite every jobs.Store must pass
pgjobs/            Postgres jobs store
jobsweb/           the jobs dashboard, a mountable sub-app; the reference example
auth/              the sign-in sub-app: passwords, email links, resets, Google, GitHub (docs/auth.md)
devmail/           the dev mailbox, a sub-app served only in dev: mail kept instead of sent, live
examples/todo/     the example app
examples/issues/   a Linear-style issue tracker using everything (docs/example-issues.md)
examples/tally/    a shared-expenses app built from the docs alone (docs/example-tally.md)
e2e/               headless Chrome tests (own module)
internal/hxtest/   templates covering every feature; codegen golden files
internal/difftest/ random render trees for the diff property tests
migrations/        Loom's own tables, installed into apps by `loom db install github.com/moomerman/loom`
kit/               the starter components, copied into apps by `loom components add`
preview/           the dev component gallery and screenshots
docs/              the reference docs, embedded for `loom docs`; llms.txt lists them in reading order
```

## Design rules (do not break)

1. Templates use a small, pure expression language, never arbitrary Go.
   The AST stays target-neutral.
2. Templates compile to statics plus dynamic slots; statics are sent once.
3. Transport is SSE down, POST up. No websockets.
4. One goroutine per live page owns its assigns; panics restart it.
5. Runtime is stdlib only, except pgx (the Postgres driver); test-only
   deps are fine. Go 1.25+.
6. `client/loom.js` is hand-written, no build step, < 8 KB minified.
7. Compile errors are excellent: file, line, column, excerpt, plain English.
8. Sub-apps (`auth`, `jobsweb`, `devmail`) use only Loom's public API;
   anything they need becomes public API.

## Agent ergonomics

Most Loom code will be written by AI agents and reviewed by humans. Loom
is a new language with no training data, so the tooling must make up for
it. Treat agent ergonomics as a first-class feature, not polish.

1. **Stay close to HEEx.** Every deviation is a place agents will guess
   wrong. Deviate only where the design rules force it, list it under
   "Differences from HEEx" in `docs/syntax.md`, and make the HEEx
   spelling a compile error with the fix.
2. **One way to do things.** No aliases or alternative syntaxes. If two
   approaches work, pick one and make the other a compile error with a fix.
3. **Every error teaches.** Each compile and runtime error says what is
   wrong, where (file:line:col with excerpt) and the fix. Each kind has a
   stable code (e.g. `LX104`), documented in `docs/errors.md`.
4. **Everything human-readable has a JSON twin.** CLI output for people is
   also available with `--json`, with a stable schema in `docs/cli.md`,
   and `loom mcp` serves it as MCP tools.
5. **Docs fit in context.** `docs/syntax.md` stays complete and under ~400
   lines. `llms.txt` and `docs/docs.go` list the docs in reading order,
   and a test keeps them the same.

A feature is done when its errors have codes, fixes and docs, its CLI
output has `--json`, and its behaviour is testable with `loomtest`.

## Subsystem rules (do not break)

Jobs (`docs/jobs.md`):

- **Transactional enqueue.** `jobs.Enqueue` takes the same `loom.Tx` as
  the data change; a job exists if and only if its transaction commits.
- **At least once.** `Perform` must be safe to run twice.
- **One semantics, every store.** The memory and Postgres stores pass
  the same conformance suite, `jobs/jobstest`.
- A panic fails the attempt, never the process. Shutdown stops fetching,
  waits for running jobs up to a grace period, then releases the rest.
  Jobs held by a dead node come back by lease expiry. Cron runs once per
  cluster under a leader lock and enqueues ordinary unique jobs.

Data (`docs/data.md`):

- **SQL is the query language.** Helpers only remove boilerplate; no
  query builder.
- **One `loom.Tx` everywhere.** `loom.DB` and `loom.Tx` share a
  `Querier` with no Postgres-specific types.
- **`db.Tx` commits or rolls back.** An error or panic rolls back;
  `AfterCommit` callbacks run only after a successful commit.
- **Migrations** are plain SQL, forward-only, checksummed in
  `loom_migrations`, applied under an advisory lock. A `-- loom:track`
  annotation makes a table NOTIFY on commit, so `loom.Watch` re-renders.

Layouts (`docs/layouts.md`): the root layout is the HTML document,
rendered once per page load with no per-user data, so live navigation
can keep it; user chrome goes in the app layout, inside the live tree.
Layouts never stack implicitly.

## Working style

- Small commits, each with passing tests.
- Clear code over clever code; this is a framework others will read.
- When a design question has no answer here or in `docs/design.md`,
  pick the simplest option consistent with the rules, and add it to
  `docs/design.md` if it shapes how Loom is used.
- Adding an error: add the code to `compiler/diag/codes.go` and `Codes`,
  document it in `docs/errors.md`, and add a case named after the code to
  `compiler/parser/testdata/errors.txt` (syntax) or
  `compiler/check/testdata/errors.txt` (types). Runtime errors (`RT4xx`)
  are constants in the root `errors.go`, documented the same way; jobs
  errors (`JB5xx`) likewise in `jobs/errors.go`, data errors (`DB6xx`)
  in `data/data.go`, style errors (`ST7xx`) in `compiler/diag/codes.go`,
  form errors (`FM8xx`) in `form/errors.go`, auth errors (`AU9xx`) in
  `auth/errors.go`, storage errors (`SG0xx`) in `storage/storage.go`.
- `jobsweb` is the reference example for idiomatic Loom: one file per
  page (`overview.go` + `overview.hx`), shared components in
  `components.hx`, doc comments explaining why. Keep it that way.
