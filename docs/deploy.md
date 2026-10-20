# Deploying

A Loom app is one Go binary. In production it needs Postgres (for
events that survive a restart, sign-in sessions, jobs, table watch and
rate limits shared by every node), a reverse proxy or load balancer in
front for TLS, and its migrations applied before it starts.

## The binary

`loom new` writes what a deploy needs:

- `migrations/`, starting with Loom's own tables (`0001_loom_clients.sql`
  to `0005_loom_nodes.sql`), embedded in the binary by `app.go`;
- a `main` that runs `myapp migrate [status] [--json]`, the same as
  `loom migrate` with the same output and exit codes, so production
  needs neither the `loom` command nor the migrations on disk;
- a `Dockerfile`: a static binary on a distroless image, listening on
  `$PORT` (4000).

Give every node the same `LOOM_SECRET` (`openssl rand -base64 32`),
passed as `loom.Config{Secret: os.Getenv("LOOM_SECRET")}` as a new
app's `main` does: auth encrypts two-factor secrets with it (AU909
without one), and changing it makes them unreadable (AU908).

An app made before this adds the same: `loom db install
github.com/moomerman/loom` copies Loom's migrations into `migrations/`
(only the ones it lacks, so run it again after upgrading Loom: 0005
adds the node registry),
and `loom.MigrateCommand(fsys, os.Args[2:], os.Stdout, os.Stderr)` runs
them (see `cmd/<app>/main.go` in a new app). The runtime never creates
tables: `Run` refuses to start with a database that lacks Loom's or a
mounted sub-app's migrations (RT433).

## A rollout

1. Build the image. Generated `*_hx.go` files are committed, so the
   build needs only Go.
2. Run `myapp migrate` once, before the new version takes traffic. It
   takes an advisory lock, so two at once take turns. Migrations are
   forward-only: the old version keeps running against the new schema
   until it is replaced, so add columns and tables first and drop them
   in a later release.
3. Start the new version. `Run` checks the database, the migrations,
   watched tables and the pooler (DB623) before it listens.
4. Stop the old one with SIGTERM. It answers `/_loom/health` 503 at
   once, waits `Config.DrainDelay` for the balancer to notice, answers
   new events 503 (the browser resends them to the new version), runs
   the ones queued within `ShutdownGrace` (15s by default), then gives
   running jobs as long again and returns the rest to the queue. Give
   the process a stop timeout of `DrainDelay` plus twice `ShutdownGrace`
   plus a few seconds.

Open tabs reconnect to the new version with a jittered backoff (1s
doubling to 30s), remount their page, keeping form input, and resend
unacknowledged events, which run once. A tab whose page needs a newer
`loom.js` (its URL is content-hashed) or wire protocol reloads instead.

## Several nodes

Live pages are held in the memory of the node that rendered them, so
the load balancer must send a browser back to the same node: **sticky
sessions** by a cookie the balancer sets. Without them, a page's update
stream and events land on other nodes, which don't have it, and the
page remounts again and again. Everything else is shared through
Postgres:

- `app.Broadcast` and jobs' `c.Broadcast` reach the pages on every node
  (NOTIFY on `loom_pubsub`; the manifest says "broadcasts reach every
  node's pages"). A broadcast over 8000 bytes reaches only its own node
  (RT447): send an id, not the data.
- `loom.Watch` re-renders watching pages on every node.
- Events resent to another node are recognised as handled
  (`loom_clients`), sign-in sessions and rate limits are in tables, and
  jobs run once, on any node, with cron on the leader alone.

Every node runs jobs by default. To keep slow jobs off the nodes that
serve pages, run web nodes with `Config.Jobs` `"enqueue-only"` and
worker nodes, which need no balancer traffic, with `"jobs-only"`
(docs/jobs.md). `loom queues --json` and jobsweb show each queue's
`oldest_available_seconds`: if it keeps growing, no node is running that
queue.

`/_loom/health` reports `watch` and `pubsub`: "reconnecting" while the
node's LISTEN connection is down, during which it misses other nodes'
broadcasts (`pubsub.listen_lost`, `watch.listen_lost` in `loom events`).
"idle" is not a fault: the connection opens when a page first needs
it. "none" means there is nothing to listen to (no database, or, for
`pubsub`, `Config.PubSub`).

Each node running jobs reports itself in the node registry: `loom nodes`
and jobsweb's overview list every node, up or stale, which one leads
(runs cron and rescue), and its workers busy per queue. Health's `node`
is the answering node's entry, from memory (`null` on enqueue-only
nodes): its `name` is what the registry and a rescued job's history call
it, so set `Runner.Node` (`app.JobRunner().Node`) to the instance's name
(a hostname, a pod name) if you want them to match your platform's. A
node listed as stale crashed, hung or lost the database: its jobs are
rescued when its lease ends, and its entry is deleted an hour later.

## Behind a proxy

- Terminate TLS at the proxy (or give `Config.Server` a `TLSConfig`),
  set `Config.BaseURL` to the public `https://` address and
  `Config.TrustedProxies` to the proxy's addresses. Forwarded headers count only from those (and loopback);
  an https `BaseURL` reached over plain http from anyone else is RT442.
- Don't buffer `/_loom/stream`: it is Server-Sent Events. Loom sends
  `X-Accel-Buffering: no` for nginx; give the proxy a read timeout over
  25s, the stream's ping interval.
- Loom gzips its own responses (`live.md`), so the proxy needn't
  compress; one that does leaves an encoded response alone. Don't let
  it compress or buffer the event stream.
- Point the balancer's health check at `GET /_loom/health`, and set
  `DrainDelay` to its interval.
- Loom's rate limits (`loom.RateLimit`) see the client's address
  through `TrustedProxies`; a limit at the proxy as well is fine.

## Files

Give the app a bucket on an S3-compatible service in
`Config.Storage` (`storage.md`), from the environment, the same on
every node. `storage.Dir` suits one server only: other nodes can't read
its disk. `Run` checks the bucket before serving (SG002 to SG005).

## Looking inside

Set `LOOM_INSPECT_TOKEN` (`openssl rand -hex 32`) on the app, and the
CLI's read-only commands work against it from anywhere with the same
token: `loom stats`, `loom queries`, `loom events`, `loom errors`,
`loom sessions`, `loom jobs`, `loom queues` and `loom nodes`, with
`--url https://app.example.com` (`cli.md`, "Asking a running app").
The counts and the last 2000 events are each node's own, since the app
started; ask each node by its own address to see them all.

## Postgres

- Postgres 14 or later, reached directly or through a pooler in
  **session** mode. Transaction pooling breaks LISTEN, advisory locks
  and prepared statements; `Run` detects it (DB623).
- Each node holds up to `Config.DBMaxConns` connections (20), among
  them one each for the table watch's, the PubSub's and the jobs'
  LISTEN and the cron leader's lock while they are in use.
- One primary. Read replicas are out of scope: reads that must see a
  commit (a page re-rendering after a NOTIFY) go to the primary.
