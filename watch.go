package loom

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/trace"
)

// Table watch (docs/live.md). A route that watches a table re-renders
// every page on it when the table changes:
//
//	app.Live("/", &TodoList{}, templates.TodoApp, loom.Watch("todos"))
//
// A tracked table (`-- loom:track todos` in a migration) sends a NOTIFY
// on channel loom_sync, naming itself, when a transaction that changed
// it commits. Every node LISTENs, so a change committed anywhere (on
// this node or another, by a job, in psql) reaches the pages of all.
// Each page then re-runs Assigns and sends the diff, at most a few times
// a second however often the table changes.

// RouteOption is an option of one route, given to Live: Watch.
type RouteOption interface{ applyRoute(*route) }

type watchOption []string

func (o watchOption) applyRoute(rt *route) { rt.watch = append(rt.watch, o...) }

// Watch is a route option naming tracked tables the page reads. When one
// of them changes (a commit anywhere, on any node), every page on the
// route re-runs Assigns and the browser gets the diff, as after an event.
// Run checks that each table exists and is tracked (DB622).
//
//	app.Live("/", &TodoList{}, templates.TodoApp, loom.Watch("todos"))
//
// A watch is per table: every page on the route re-renders on any change
// to it. For finer grain, publish on a topic in tx.AfterCommit and
// c.Subscribe to it.
func Watch(tables ...string) RouteOption { return watchOption(tables) }

// watchInterval is the shortest time between two re-renders of a page
// for its watched tables: changes in between are coalesced into one.
const watchInterval = 200 * time.Millisecond

// checkWatches checks every watched table exists and is tracked, for Run
// and Validate.
func (a *App) checkWatches(ctx context.Context) error {
	var watching []*route
	for _, rt := range a.routes {
		if len(rt.watch) > 0 {
			watching = append(watching, rt)
		}
	}
	if len(watching) == 0 {
		return nil
	}
	if a.db == nil || (a.db.SQL() == nil && !a.db.IsInTx()) {
		rt := watching[0]
		return &data.Error{Code: data.DB622, Msg: fmt.Sprintf("route %s watches %s, but the app has no database to watch", rt.path, strings.Join(rt.watch, ", ")),
			Fix: "give the app a Postgres database (loom.Config{DB: ...}, or app.UseDB in tests), or drop loom.Watch and publish changes on a topic instead"}
	}
	tracked, err := data.Tracked(ctx, a.db)
	if err != nil {
		return err
	}
	for _, rt := range watching {
		for _, t := range rt.watch {
			if slices.Contains(tracked, t) {
				continue
			}
			var exists bool
			if err := a.db.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return &data.Error{Code: data.DB622, Msg: fmt.Sprintf("route %s watches table %s, which doesn't exist (tracked tables: %s)", rt.path, t, orNone(tracked)),
					Fix: "fix the name in loom.Watch, or create the table in a migration and track it with `-- loom:track " + t + "`"}
			}
			return &data.Error{Code: data.DB622, Msg: fmt.Sprintf("route %s watches table %s, which isn't tracked, so its changes aren't announced (tracked tables: %s)", rt.path, t, orNone(tracked)),
				Fix: fmt.Sprintf("add `-- loom:track %s` to a new migration (`loom db new track_%s`) and run `loom migrate`", t, t)}
		}
	}
	return nil
}

func orNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// watchHub fans table notifications out to the sessions of this node
// whose routes watch the table.
type watchHub struct {
	mu        sync.Mutex
	sessions  map[*session]bool
	listening bool
	stop      context.CancelFunc
	done      chan struct{}
	lost      atomic.Bool // the LISTEN connection dropped and isn't back yet
}

// watch adds a session to the hub, starting the LISTEN connection for
// the first. It returns once the connection listens, so a change
// committed after the page loaded isn't missed. Without a Postgres
// connection (a test's rolled-back database) nothing is announced.
func (a *App) watch(s *session) {
	sdb := a.DB().SQL()
	if sdb == nil {
		return
	}
	h := &a.watches
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil {
		h.sessions = map[*session]bool{}
	}
	h.sessions[s] = true
	if h.listening {
		return
	}
	h.listening = true
	ctx, cancel := context.WithCancel(context.Background())
	h.stop, h.done = cancel, make(chan struct{})
	ready := make(chan struct{})
	go a.listenTables(ctx, sdb, ready, h.done)
	h.mu.Unlock()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
	}
	h.mu.Lock()
}

// unwatch removes a session, and stops listening after the last.
func (a *App) unwatch(s *session) {
	h := &a.watches
	h.mu.Lock()
	delete(h.sessions, s)
	var stop context.CancelFunc
	var done chan struct{}
	if len(h.sessions) == 0 && h.listening {
		stop, done = h.stop, h.done
		h.listening, h.stop = false, nil
	}
	h.mu.Unlock()
	if stop != nil {
		stop()
		<-done
	}
}

// changed tells the sessions watching table, or every watching session
// for "" (after the LISTEN connection was lost and changes may have been
// missed). It returns how many it told.
func (h *watchHub) changed(table string) int {
	h.mu.Lock()
	var out []*session
	for s := range h.sessions {
		if table == "" || slices.Contains(s.route.watch, table) {
			out = append(out, s)
		}
	}
	h.mu.Unlock()
	for _, s := range out {
		s.tableChanged()
	}
	return len(out)
}

// listenTables holds a LISTEN connection on the tracking channel,
// reconnecting after errors; after a reconnect it re-renders every
// watching page, since changes may have been missed meanwhile.
func (a *App) listenTables(ctx context.Context, sdb *sql.DB, ready, done chan struct{}) {
	defer close(done)
	var once sync.Once
	first := true
	listening := func() {
		a.watches.lost.Store(false)
		once.Do(func() { close(ready) })
		if !first {
			a.watches.changed("")
		}
		first = false
	}
	for ctx.Err() == nil {
		err := listenOnce(ctx, sdb, data.ChangeChannel, listening, func(table string) {
			n := a.watches.changed(table)
			a.stream.Emit(ctx, trace.WatchNotify, "table", table, "sessions", n)
		})
		if ctx.Err() != nil {
			return
		}
		a.watches.lost.Store(true)
		a.stream.Emit(ctx, trace.WatchListenLost, "msg", "loom: table watch LISTEN connection lost; reconnecting", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// listenOnce LISTENs on channel until the connection fails or ctx ends,
// calling f with each notification's payload.
func listenOnce(ctx context.Context, sdb *sql.DB, channel string, listening func(), f func(string)) error {
	conn, err := sdb.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		sc, ok := dc.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("table watch needs the pgx driver (sql.Open(\"pgx\", url)), got %T", dc)
		}
		pc := sc.Conn()
		if _, err := pc.Exec(ctx, "LISTEN "+channel); err != nil {
			return err
		}
		listening()
		for {
			n, err := pc.WaitForNotification(ctx)
			if err != nil {
				return err
			}
			f(n.Payload)
		}
	})
}

// sessionWatch is a session's side of table watch: whether a re-render
// is pending, and when the last one ran.
type sessionWatch struct {
	pending atomic.Bool
	last    atomic.Int64 // unix nanoseconds
}

// tableChanged queues a re-render of the page, unless one is pending,
// no sooner than watchInterval after the last.
func (s *session) tableChanged() {
	if !s.watching.pending.CompareAndSwap(false, true) {
		return
	}
	wait := time.Until(time.Unix(0, s.watching.last.Load()).Add(watchInterval))
	time.AfterFunc(max(wait, 0), func() {
		select {
		case s.events <- pageEvent{watch: true}:
		case <-s.quit:
		}
	})
}

// Watches returns the tables the page's route watches (see Watch).
func (v *View) Watches() []string { return append([]string(nil), v.watch...) }

// Refresh re-runs the Assigns of the page and its live layout, and
// re-renders, as the runtime does
// when a table the route watches changes. It returns the diff to send to
// the client, or nil if nothing changed. Like Event, it runs the route's
// auth checks first.
func (v *View) Refresh() (map[string]any, error) {
	if v.lent {
		return nil, nil
	}
	v.begin("")
	if err := authorize(v.auth, v.ctx); err != nil {
		return nil, err
	}
	assigns, lassign, err := v.call(v.page, "Assigns", func() {})
	return v.update(assigns, lassign, "Assigns", err)
}

// state is the LISTEN connection's state, for /_loom/health.
func (h *watchHub) state() string {
	h.mu.Lock()
	listening := h.listening
	h.mu.Unlock()
	switch {
	case !listening:
		return "idle"
	case h.lost.Load():
		return "reconnecting"
	}
	return "ok"
}
