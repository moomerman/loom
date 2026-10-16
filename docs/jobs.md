# Background jobs

Loom runs durable background jobs: typed, retried with backoff,
scheduled, deduplicated, run on cron, and visible to `loom jobs`. This is
the one way to run background work in a Loom app.

**Jobs run at least once.** A job can run twice: its node may crash after
the work but before recording it, or lose its lease (JB511). Write
`Perform` so a second run is harmless: check before acting, use upserts,
and pass `c.ID()` (stable across attempts) to external APIs as an
idempotency key.

## A job

```go
type SendWelcome struct {
	UserID string `json:"user_id"`          // args: exported fields, as JSON
	Mailer *Mailer `json:"-"`               // a dependency, from the prototype
}

// Options is optional; zero fields take the defaults.
func (SendWelcome) Options() jobs.Options {
	return jobs.Options{Queue: "mail", MaxAttempts: 5, Timeout: 30 * time.Second}
}

func (j SendWelcome) Perform(ctx context.Context, c *jobs.Ctx) error {
	return j.Mailer.Send(ctx, j.UserID, "Welcome!", c.ID())
}
```

- A job type is a struct with a value-receiver `Perform`. Its exported
  fields are its args, stored as JSON (JB503 if they can't be).
- Defaults: queue `default`, 20 attempts, one-minute timeout.
- `Perform` returns `nil` (done), an error (retry with backoff),
  `jobs.Cancel(err)` (stop, no retry) or `jobs.Snooze(d)` (run again
  after `d`, not counted as an attempt).
- `ctx` is cancelled at the timeout (JB506) and at shutdown. A panic fails
  the attempt with its stack trace (JB505); the process keeps running.
- `c.ID()`, `c.Attempt()`, `c.MaxAttempts()`; `c.Now()` (the runner's
  clock: use it, not `time.Now`, so tests control time); `c.DB()` to
  enqueue follow-up jobs in `c.DB().Tx`; `c.Broadcast(topic, event, payload)` to notify
  live pages.

## The app

```go
app := loom.New()
db, err := loom.OpenDB(ctx, os.Getenv("DATABASE_URL"))
app.UseDB(db)                                  // optional; default in memory
app.JobStore(pgjobs.New(db, pgjobs.Options{})) // jobs in the same database
app.Jobs(SendWelcome{Mailer: mailer}, PruneSessions{DB: db})
app.Queues(jobs.Queue{Name: "mail", Concurrency: 10})
app.Cron("0 3 * * *", PruneSessions{})
app.Run(":4000")
```

- `app.Jobs` registers job types. The value is a prototype: each job is
  decoded into a copy of it, so `json:"-"` fields (database handles,
  clients) carry over. An unexported field must be tagged `json:"-"`
  too (JB516): JSON drops it, so it can't be an arg. A stored job of an unregistered type fails
  (JB501) and is retried, so a newer node can run it.
- `app.Queues` sets per-node concurrency. Every registered type's queue
  must be configured (JB510); `default` always exists, with 10 workers.
- `app.Run` starts the workers. On SIGINT or SIGTERM it stops fetching,
  waits for running jobs up to `ShutdownGrace` (15s), then releases the
  rest back to their queue, uncounted.
- `loom.Config{Jobs: ...}` splits web and worker nodes: `"run"` (the
  default) serves pages and runs jobs and cron; `"enqueue-only"` serves
  pages and leaves the jobs to other nodes; `"jobs-only"` runs jobs and
  cron and answers only `/_loom/health`. Set it per deploy:
  `Jobs: loom.JobsMode(os.Getenv("LOOM_JOBS"))`. An unknown mode is
  RT441; the manifest and `/_loom/health` show the node's.
- `app.JobRunner()` returns the `*jobs.Runner`, for its `Store()` and
  settings (`Node`, `PollInterval`, `LeaseDuration`, `ShutdownGrace`,
  `Retention`, `NodeRetention`, `CronCatchUp`, `Backoff`), set before
  `Run`.

## Enqueueing

A job is enqueued in the same transaction as the data change that causes
it: **it exists if and only if the transaction commits.**

```go
err := app.DB().Tx(ctx, func(tx loom.Tx) error {
	if _, err := tx.Exec(ctx, "INSERT INTO users ...", id); err != nil {
		return err
	}
	_, err := jobs.Enqueue(tx, SendWelcome{UserID: id})
	return err
})

jobs.Enqueue(tx, SendWelcome{UserID: id}, jobs.In(10*time.Minute))
jobs.Enqueue(tx, SendWelcome{UserID: id}, jobs.At(t))
jobs.Enqueue(tx, SendWelcome{UserID: id}, jobs.Unique(time.Hour))
```

- `Enqueue` takes a `loom.Tx` (see `data.md`): returning an error or
  panicking rolls the job back with the data. The job goes to the store
  bound to the Tx's DB (JB514 if none). When the app's runner uses that
  DB, a type it doesn't run fails the enqueue (JB501) rather than
  being retried until discarded.
- An in-memory app has a local DB (`app.DB()` without `app.UseDB`): its
  `Tx` runs no SQL but still commits or drops the jobs enqueued in it.
  Live page events should enqueue slow work this way rather than block
  the page.
- `jobs.Unique(period)` does nothing if a job of the same type and args
  was enqueued within `period` and wasn't discarded or cancelled. The
  result says so: `Result{ID: existing, Duplicate: true}`.

## States

```
scheduled → available → executing → completed | retryable | discarded | cancelled
```

A failed attempt is `retryable` until its backoff ends (15s, doubling per
attempt up to a day, plus up to 10% jitter), then runs again; the last
allowed failure is `discarded`. A snoozed job returns to `scheduled`.
Each executing job holds a lease (30s, renewed every 10s); if its node
dies, the leader rescues it, recording JB511: it is `retryable` for 15s
(30s the second time), or discarded if it has no attempts left. A job
rescued three times (`jobs.MaxRescues`) is discarded with JB517, since it
may be what crashes its node. A store error recording an outcome is
retried a few times before the job is left to its lease. Completed,
discarded and cancelled jobs are deleted after `Retention` (7 days), in
batches, by the built-in hourly `jobs.Prune` cron job.

## Nodes

Every node that runs jobs records itself in the node registry
(`loom_nodes` on Postgres): its name (`Runner.Node`, default
host-pid-random), its queues with their concurrency and running jobs,
whether it leads, and when it started and last reported. It reports on
start, with every lease renewal (a third of `LeaseDuration`: 10s), and
at once when it gains or loses leadership, and removes itself on a
graceful shutdown. Enqueue-only nodes run no jobs and aren't listed.

A node that stops reporting is **stale** once its lease ends: the same
rule, and the same moment, as the jobs it held being rescued. A stale
node never shows as leader. The prune job deletes nodes stale for
longer than `NodeRetention` (1h). A rescued job's JB511 error names the
node that died holding it (`AttemptError.Node`, and its history).

`runner.Nodes(ctx)` lists the registry (`[]jobs.NodeInfo`: each `Node`
with `State` "up" or "stale" and `Self`); `runner.Self()` is this node
from memory. Each report is announced on `jobs.NodesTopic` (event
`jobs.NodesEvent`, `Payload["node"]` its name), separate from
`jobs.Topic` so pages showing jobs don't re-read them on heartbeats.
`loom nodes`, the `nodes` MCP tool, jobsweb's overview and the `node`
field of `/_loom/health` show it.

## Cron

`app.Cron(expr, job)` takes five fields in UTC: minute hour day-of-month
month day-of-week, with `*`, numbers, ranges, lists and steps (JB504).
One node, the leader (a Postgres advisory lock), enqueues each tick as an
ordinary unique job keyed by entry and tick time, so a tick seen twice
runs once. A tick missed by up to `CronCatchUp` (5 minutes, e.g. during a
leader change) is enqueued late; older missed ticks are skipped.

## Live pages

A page subscribes to a topic and receives messages in `HandleInfo`:

```go
func (p *ExportPage) Mount(c *loom.Ctx) {
	c.Subscribe("exports:" + p.userID)
}

func (p *ExportPage) HandleInfo(c *loom.Ctx, msg loom.Message) {
	if msg.Event == "export_ready" {
		p.url = msg.Payload["url"] // Assigns runs next and shows it
	}
}

// in the job:
c.Broadcast("exports:"+j.UserID, "export_ready", map[string]string{"url": url})
```

Messages travel on `app.PubSub()` (in-process by default; `Config.PubSub`). See `live.md`.

The runner also announces its own changes on `jobs.Topic`: a
`jobs.ChangedEvent` whenever jobs are enqueued, start, finish, or are
retried, cancelled or deleted, with the changed ids comma-separated in
`Payload["ids"]` (empty when unknown or more than 100). Announcements are
coalesced to one per `runner.ChangeInterval` (250ms), so a busy queue
can't flood pages. Dashboards subscribe to it instead of polling, and
`jobs.Changed(msg.Payload, id)` tells a page about one job whether to
re-read it. On Postgres, enqueues by any node are announced (through
`LISTEN/NOTIFY`); other changes only by the node that made them.

## Stores

- `jobs.NewMemoryStore(jobs.MemoryOptions{})`: the default, on the app's
  local DB. For development, tests and apps that keep their data in
  memory. Jobs are lost on exit.
- `pgjobs.New(db, pgjobs.Options{})`: Postgres, on a `*loom.DB` from
  `loom.OpenDB`. Its tables, `loom_jobs` and `loom_nodes`, are Loom's
  own migrations: `loom db install github.com/moomerman/loom`, then
  `loom migrate`; startup refuses a database without them (RT433).
  Fetches use `FOR UPDATE SKIP LOCKED`; `LISTEN/NOTIFY` wakes workers
  the moment a job commits.

Both behave identically: one conformance suite (`jobs/jobstest`) runs
against each.

## Testing

```go
func TestSignUp(t *testing.T) {
	q := loomtest.Jobs(t)                  // in-memory store, fake clock
	q.Jobs(SendWelcome{Mailer: fakeMailer})
	signUp(q.DB(), "u1")                   // code that enqueues in q.DB().Tx
	q.AssertEnqueued(t, SendWelcome{UserID: "u1"})
	q.Advance(10 * time.Minute)            // scheduled jobs and cron ticks come due
	q.Drain(t)                             // run due jobs, synchronously
	q.AssertCompleted(t, SendWelcome{})    // zero fields match anything
}
```

- `q.Cron(expr, job)` adds a cron entry; `Advance` enqueues each tick it
  passes. Only your entries run, not the built-in prune.
- `q.AssertState(t, job, jobs.Discarded)`, `q.RefuteEnqueued`,
  `q.Matching(job)`, `q.Enqueue(job)`, `q.AssertBroadcast(t, topic,
  event)`. Failed jobs retry after one minute per attempt of fake time.
- `p.Listen(q.PubSub())` connects a `loomtest.Mount` page, so jobs'
  broadcasts reach its `HandleInfo` during `Drain`.
- The queue's runner is `Manual`, so it doesn't report itself:
  `q.Runner().Heartbeat(ctx)` records it in the node registry (node
  "loomtest") and announces it, and `q.Store().Heartbeat(ctx,
  jobs.Node{...})` adds another node, as `jobsweb`'s tests do.
- `jobs.PerformNow(t, SendWelcome{...})` runs one `Perform` directly and
  returns its error, after a JSON round trip of the args.
- `loomtest.Jobs(t, loomtest.WithStore(func(c jobs.Clock) jobs.Store {
  ... }))` runs the queue on another store, such as `pgjobs.New` on a
  `loomtest.DB(t, loomtest.Committed())`, to run one test on every store.
  `loomtest.UseDB(db)` keeps the memory store but takes jobs enqueued in
  `db`'s transactions.
- To test a whole app whose setup registers jobs, attach the queue
  after `UseDB` and before setup: `app.UseDB(db); q.Attach(app)`. The
  app's job runner, and its PubSub, are then the queue's: the jobs the
  app and its sub-apps register (auth's mail, say) run on `q.Drain`,
  with no `q.Jobs` of your own. They run only there: serving the app
  (`httptest.NewServer(app)`) starts no workers or cron of its own
  (the queue's runner is `Manual`), so nothing touches the test's
  transaction behind its back.

## Inspecting

With the app under `loom dev`: `loom jobs [--queue Q] [--state S]
[--search TEXT]`, `loom jobs inspect ID` (args, attempts, errors with
stacks, timings), `loom jobs retry|cancel|delete ID`, `loom jobs
retry-discarded`, `loom jobs enqueue TYPE --args file.json`, `loom
queues`, `loom nodes`, `loom cron`, `loom cron run INDEX`, all with
`--json`, and the
same as MCP tools through `loom mcp`. Failed attempts also appear in
`loom errors`. See `cli.md`.

A job keeps the trace id of the action that enqueued it (`Record.Trace`;
a cron tick starts its own), and its `job.enqueue`, `job.start`,
`job.finish` and broadcasts are events under it, so `loom trace ID`
shows a click's jobs with it (`events.md`). The app's runner reports to
the app's stream; a runner of your own logs to `Runner.Logger` unless
its `Events` is set.

In the browser, mount the `jobsweb` dashboard (queues, nodes, jobs, a
job's attempts and history, cron, with the same actions):

```go
app.Group(loom.Auth(requireAdmin)).Mount("/admin/jobs", jobsweb.New(jobsweb.Config{}))
```

The CLI, MCP tools and dashboard all call the same `Runner` methods
(`Jobs`, `Job`, `Retry`, `Cancel`, `Delete`, `RetryDiscarded`,
`RunCron`, `QueueInfo`, `Nodes`, `CronInfo`, `RecentFailures`), so they
agree.
See `example-jobsweb.md`.
