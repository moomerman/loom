package trace

import "log/slog"

// Event names. Each is documented, with its attributes, in
// docs/events.md; a test checks the two lists match, as for error codes.
// Names are stable: tools and alerts match on them.
const (
	// The app.
	AppListen       = "app.listen"        // serving: addr
	AppShutdown     = "app.shutdown"      // shutting down
	AppDrainTimeout = "app.drain_timeout" // shutdown's grace ended with events queued
	Error           = "error"             // a runtime error: msg, code, where, fix, sid, source

	// Live pages, one per session goroutine's turn.
	PageMount   = "page.mount"   // a page load: sid, route, ms, user, outcome
	PageEvent   = "page.event"   // a browser event handled: sid, route, event, ms, diff, bytes, outcome
	PageInfo    = "page.info"    // a PubSub message handled: sid, route, topic, event, ms, diff, bytes, outcome
	PageWatch   = "page.watch"   // a watched table re-rendered: sid, route, ms, diff, bytes, outcome
	PageTimer   = "page.timer"   // timers ran: sid, route, ms, diff, bytes, outcome
	PagePatch   = "page.patch"   // the URL changed within the route: sid, route, url, ms, diff, bytes, outcome
	PageRestart = "page.restart" // the supervisor remounted a failed page: sid, route, restarts
	PageClose   = "page.close"   // a session ended: sid, route, reason

	// Browsers' update streams.
	StreamOpen  = "stream.open"  // sid, full
	StreamClose = "stream.close" // sid, reason
	StreamSlow  = "stream.slow"  // a browser too slow to keep up was dropped: sid

	// Events that didn't run.
	EventRefused   = "event.refused"   // sid, event, code, status
	EventDuplicate = "event.duplicate" // a resent event already handled: sid, event, seq
	LimitRefused   = "limit.refused"   // a rate limit refused an attempt: route, event, limit, retry_after

	// Change and messages.
	WatchNotify      = "watch.notify"       // a tracked table changed: table, sessions
	WatchListenLost  = "watch.listen_lost"  // the LISTEN connection dropped: err
	PubSubPublish    = "pubsub.publish"     // topic, event, subscribers
	PubSubListenLost = "pubsub.listen_lost" // the PubSub's LISTEN connection dropped: err

	// Users.
	UserSignIn  = "user.signin"  // a session started: user
	UserSignOut = "user.signout" // a session ended: user

	// The auth sub-app.
	AuthSignInFailed    = "auth.signin_failed"     // method, reason, user
	AuthTwoFactorFailed = "auth.two_factor_failed" // user, locked
	AuthReset           = "auth.reset"             // a password was reset: user

	// The data layer.
	TxCommit   = "tx.commit"   // ms, statements
	TxRollback = "tx.rollback" // ms, statements, reason
	DBSlow     = "db.slow"     // a statement ran longer than Config.SlowQuery: ms, query

	// Jobs.
	JobEnqueue     = "job.enqueue"      // id, type, queue, duplicate
	JobStart       = "job.start"        // id, type, queue, attempt
	JobFinish      = "job.finish"       // id, type, queue, attempt, ms, state, code
	CronTick       = "cron.tick"        // cron, type, id, duplicate
	JobsLeader     = "jobs.leader"      // node, leader
	JobsRescue     = "jobs.rescue"      // count
	JobsListenLost = "jobs.listen_lost" // err

	// Mail.
	MailSent = "mail.sent" // the dev outbox kept a mail: to, subject
)

// levels gives each name its level. Debug events happen on every click
// or query and stay out of a default logger; Info and above are the
// app's log.
var levels = map[string]slog.Level{
	AppListen: slog.LevelInfo, AppShutdown: slog.LevelInfo, AppDrainTimeout: slog.LevelWarn, Error: slog.LevelError,
	PageMount: slog.LevelDebug, PageEvent: slog.LevelDebug, PageInfo: slog.LevelDebug, PageWatch: slog.LevelDebug,
	PageTimer: slog.LevelDebug, PagePatch: slog.LevelDebug, PageRestart: slog.LevelWarn, PageClose: slog.LevelDebug,
	StreamOpen: slog.LevelDebug, StreamClose: slog.LevelDebug, StreamSlow: slog.LevelWarn,
	EventRefused: slog.LevelInfo, EventDuplicate: slog.LevelDebug, LimitRefused: slog.LevelInfo,
	WatchNotify: slog.LevelDebug, WatchListenLost: slog.LevelWarn, PubSubPublish: slog.LevelDebug, PubSubListenLost: slog.LevelWarn,
	UserSignIn: slog.LevelInfo, UserSignOut: slog.LevelInfo,
	AuthSignInFailed: slog.LevelInfo, AuthTwoFactorFailed: slog.LevelWarn, AuthReset: slog.LevelInfo,
	TxCommit: slog.LevelDebug, TxRollback: slog.LevelDebug, DBSlow: slog.LevelWarn,
	JobEnqueue: slog.LevelDebug, JobStart: slog.LevelDebug, JobFinish: slog.LevelDebug, CronTick: slog.LevelDebug,
	JobsLeader: slog.LevelInfo, JobsRescue: slog.LevelWarn, JobsListenLost: slog.LevelWarn,
	MailSent: slog.LevelInfo,
}

// Names lists every event name Loom emits.
func Names() []string {
	out := make([]string, 0, len(levels))
	for n := range levels {
		out = append(out, n)
	}
	return out
}

// Level is the level events of name are emitted at: the one
// docs/events.md gives it, or Info for a name Loom doesn't define (a
// sub-app's own).
func Level(name string) slog.Level {
	if l, ok := levels[name]; ok {
		return l
	}
	return slog.LevelInfo
}
