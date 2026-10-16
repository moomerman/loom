# Design

The choices that shape Loom, each in a line or two. The rules that
keep them are in `AGENTS.md`.

- **One page model, the live page.** Server-rendered, diffed over SSE
  down and POST up, one goroutine per page. Events are restart-safe: the
  browser resends unacknowledged ones and the server runs each once
  (`loom_clients`).
- **Templates** are `.hx` files with a small pure expression language,
  compiled to Go statics plus dynamic slots. There is one way to write
  each thing, and the others are compile errors with a fix.
- **Configuration** is `loom.Config`, every field with a default; a bad
  value is RT441 from `New`. Per-deploy settings come from the
  environment: `DB`, `BaseURL`, `Secret`, `Jobs`.
- **Data** is SQL first on Postgres through pgx, the one exception to a
  stdlib runtime: `Select`/`Get`, `db.Tx` with `AfterCommit`, plain SQL
  migrations, forward-only, with a lock timeout. Loom's own tables are
  migrations too, installed into the app; the runtime creates none.
- **Jobs** are durable, typed and enqueued in the data change's
  transaction; at least once, with leases, rescue with backoff and a
  crash cap, cron on one leader. Nodes can run pages, jobs or both,
  and every node running jobs heartbeats into a node registry.
- **Files** go in one bucket, `Config.Storage`: S3-compatible services
  through a stdlib SigV4 client, a directory, or memory, all passing
  one conformance suite.
- **Several nodes** share Postgres: NOTIFY carries broadcasts and table
  changes, sessions and rate limits are tables, and live pages need
  sticky sessions. See `deploy.md`.
- **Few dependencies.** The runtime is the standard library plus pgx.
  Passwords are hashed with PBKDF2-SHA256 from `crypto/pbkdf2` at
  OWASP's 600,000 iterations, since bcrypt and argon2 would need
  `golang.org/x/crypto`; a hash records its scheme and iterations, so a
  stronger one can follow. The browser runtime, `client/loom.js`, is
  hand-written with no build step and stays under 8 KB minified.
- **Security by default:** CSRF checks, signed-in pages bound to their
  browser, rate limits on auth, a strict Content-Security-Policy with
  no inline script, HSTS behind https, and secrets that can't be hashed
  sealed with `Config.Secret`.
- **Sign-in** is a sub-app (`auth`) built on core's sessions and typed
  user, mounted like any other; sub-apps use only the public API, and
  what they needed became public.
- **Agents first:** every error has a code, a fix and a doc; every
  human-readable output has `--json` and an MCP tool; `loom.manifest`
  states the app's behaviour for review; the event stream records what a
  running app did.
