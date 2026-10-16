# Events

A running app records what it does as one stream of named events: a
page mounting, a click handled, a commit, a job's attempt, a refused
event, an error. The names and attributes below are stable, like error
codes; tools and alerts match on them. A test checks this list against
the code (`trace.Names()`).

```
$ loom events --name 'page.*'
14:02:11.204 page.mount ms=3.1 outcome=ok route=/ sid=9f2c… trace=4be1…
14:02:13.880 page.event bytes=212 diff=true event=add ms=1.4 outcome=ok route=/ sid=9f2c… trace=1a7f3c9e02d4-3
```

## Where they go

Every event goes to the app's stream (`app.Stream()`), which:

- keeps the last 2000 for `loom events`, `loom trace` and the `events`
  and `trace` MCP tools (dev only, at `GET /_loom/dev/events`);
- counts them by name, with mean and max `ms`, for `loom stats`;
- logs them to `Config.Logger` at the level shown below. The default
  logger is Info, so the per-click and per-query Debug events stay out
  of production logs; set a Debug handler to see them;
- passes them all to `Config.OnEvent`, whatever the logger's level: the
  bridge to an error tracker, metrics or tracing. It runs on the
  goroutine that emitted the event, so keep it quick.

`loom errors` lists the `error` events. A sub-app or app can emit its
own with `app.Emit(ctx, "billing.charge_failed", "user", id)`: prefix the
name with your package's, and they are logged at Info.

## Traces

Each event carries the trace id of the action it belongs to. A page
load gets one; a browser event's is its tab's client id (hashed) and
sequence number, so an event the browser resends after a restart keeps
it. It travels in the context: `c.Context()` in a handler carries it,
so `db.Tx(c.Context(), ...)` reports its commit under it, and
`jobs.Enqueue` stores it with the job, whose start, finish and
`c.Broadcast` carry it too; the pages that handle that broadcast
re-render under it. So one id ties a click to its commit, its job, the
job's broadcast and the re-renders it caused:

```
$ loom trace 1a7f3c9e02d4-3
trace 1a7f3c9e02d4-3, from 14:02:13.879:
    +0.0ms job.enqueue duplicate=false id=41 queue=default type=todo.Notify
    +0.1ms tx.commit ms=0.9 statements=2
    +0.4ms page.event bytes=212 diff=true event=add ms=1.4 outcome=ok route=/ sid=9f2c…
   +31.2ms job.start attempt=1 id=41 queue=default type=todo.Notify
   +33.0ms pubsub.publish event=changed subscribers=2 topic=todos:ana
   +33.6ms page.info bytes=98 diff=true event=changed ms=0.5 outcome=ok route=/ sid=77d1… topic=todos:ana
   +34.1ms job.finish attempt=1 id=41 ms=2.9 queue=default state=completed type=todo.Notify
```

`send_event` (and `loom event --json`) returns the event's trace id and
the events it emitted while it ran. In Go, `trace.ID(ctx)` reads it.

## Testing

`loomtest` pages emit the same `page.*` events. `p.Events()` is the
latest action's events, including those of jobs a `q.Drain` ran for
it; `p.AssertEvent(name, key, value, ...)` and `p.RefuteEvent(name)`
check them, and `q.AssertEvent(t, name, ...)` checks a job queue's:

```go
p.Submit("form", loom.Payload{"title": "Milk"})
p.AssertEvent("tx.commit", "statements", 1)
p.AssertEvent("job.enqueue", "type", "todo.Notify")
q.Drain(t)
p.AssertEvent("job.finish", "type", "todo.Notify", "state", "completed")
```

Values compare as text. `ms` is always milliseconds, to a tenth.

## The catalogue

Common attributes: `sid` (a page session), `route` (its route pattern),
`ms` (how long), `code` (an error code, docs/errors.md).

### app.listen
Info. The server is listening: `addr`.

### app.shutdown
Info. Shutdown began.

### app.drain_timeout
Warn. Shutdown's grace period ended with page events still queued;
browsers resend them after the restart.

### error
Error. A runtime error: `msg`, `code`, `where`, `fix`, and what it
concerns: `sid` and `route` for a page, `job` and `type` for a job,
`source` ("browser", with `url`) for a page's script. `loom errors`
lists these.

### page.mount
Debug. A page load: `sid`, `route`, `ms`, `outcome` (ok, redirect,
denied, limited, full or failed).

### page.event
Debug. A browser event handled: `sid`, `route`, `event`, `ms`, `diff`
(whether an update was sent), `bytes` (its size), `outcome` (ok,
refused, denied, limited or failed) and `code` if it didn't succeed.

### page.info
Debug. A PubSub message handled in HandleInfo: as page.event, with
`topic` and `event` (the message's).

### page.watch
Debug. A watched table changed and the page re-rendered: as page.event
without `event`.

### page.timer
Debug. Due `c.SendAfter` timers ran: as page.watch.

### page.patch
Debug. The URL changed within the route (HandleParams): as page.watch,
with `url`.

### page.restart
Warn. A page failed and the supervisor mounted it afresh: `sid`,
`route`, `restarts` (in the last minute; over 5 the session ends).

### page.close
Debug. A session ended: `sid`, `route`, `reason` (idle: no browser for
SessionTimeout; navigated: live navigation left it; auth: an auth check
refused it; failed: it failed too often).

### stream.open
Debug. A browser opened a page's update stream: `sid`, `full` (it was
behind and gets the whole tree).

### stream.close
Debug. A stream closed: `sid`, `reason` (browser, dropped, reload or
shutdown).

### stream.slow
Warn. A browser couldn't keep up with its page's updates and its
stream was dropped; it reconnects and resyncs: `sid`.

### event.refused
Info. A browser event that never reached its page: `sid`, `event`,
`code` (RT424 restarting, RT405 malformed, RT404 no session, RT407 too
many waiting) and `status`. A click that did nothing shows here.

### event.duplicate
Debug. A resent event that had already run, answered as handled:
`sid`, `event`, `seq`.

### limit.refused
Info. A rate limit refused an attempt (RT436): `route`, `event` ("" for
a page load or request), `limit`, `retry_after` (ms).

### watch.notify
Debug. A tracked table changed: `table`, `sessions` told.

### watch.listen_lost
Warn. The table watch's LISTEN connection dropped: `err`. It
reconnects and re-renders every watching page.

### pubsub.publish
Debug. `app.Broadcast` or a job's `c.Broadcast`: `topic`, `event`,
`subscribers` on this node (-1 if the PubSub can't tell).

### pubsub.listen_lost
Warn. The PubSub's LISTEN connection, which brings other nodes'
broadcasts, dropped: `err`. It reconnects; broadcasts sent meanwhile
are missed.

### user.signin
Info. A sign-in session started (App.SignIn, or Ctx.SignIn's
redirect): `user`.

### user.signout
Info. A sign-in session ended: `user`.

### auth.signin_failed
Info. The auth sub-app refused a password: `method`, `reason`. No
email: a wrong one and a wrong password look the same.

### auth.two_factor_failed
Warn. A wrong two-factor code: `user`, `locked` (the account's codes are
locked after 10 in a row).

### auth.reset
Info. A password was reset with an emailed link: `user`.

### tx.commit
Debug. A transaction committed: `ms` (from begin), `statements`.

### tx.rollback
Debug. A transaction rolled back: `ms`, `statements`, `reason` (error,
panic or "commit failed").

### db.slow
Warn. A statement ran for `Config.SlowQuery` (default 500ms) or longer:
`ms`, `query` (on one line, cut at 200 bytes; never its arguments).

### job.enqueue
Debug. `jobs.Enqueue` inserted a job: `id`, `type`, `queue`,
`duplicate` (a unique job that already existed). Emitted before the
transaction commits; a rollback removes the job.

### job.start
Debug. An attempt began: `id`, `type`, `queue`, `attempt`.

### job.finish
Debug. An attempt ended: `id`, `type`, `queue`, `attempt`, `ms`,
`state` (completed, retryable, discarded, cancelled or snoozed) and
`code` if it failed. A failure also emits `error`.

### cron.tick
Debug. The leader enqueued a cron entry's tick: `cron`, `type`, `id`,
`duplicate` (another node or tick already had). Each tick starts a
trace.

### jobs.leader
Info. This node became, or stopped being, the cluster's leader (cron
and rescue): `node`, `leader`.

### jobs.rescue
Warn. Jobs whose node stopped renewing their lease were made available
again: `count`.

### jobs.listen_lost
Warn. The Postgres job store's LISTEN connection dropped: `err`.
Workers poll meanwhile.

### mail.sent
Info. The dev outbox kept a mail (no mailer in dev): `to`, `subject`.
