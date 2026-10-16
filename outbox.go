package loom

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/trace"
)

// Events are restart-safe (docs/live.md, docs/wire.md). Each browser tab
// is a client with an id, numbering its events 1, 2, 3; it keeps each
// event in an outbox until the server acks it, and resends it after a
// network failure, a 5xx or a restart. The server acks only after the
// handler has run, and runs each (client, sequence) once: it remembers
// the last sequence handled per client, in memory and, with a database,
// in loom_clients, so a resend to a restarted or different node is
// answered as already handled.
//
// The limit: the handler's own writes and the record in loom_clients are
// separate commits, so a crash between them runs the event again on
// resend. Handlers that write should be idempotent where it matters, as
// jobs are.

// clientRetention is how long a client's last sequence is kept after it
// was last seen. A tab's outbox gives up long before.
const clientRetention = 7 * 24 * time.Hour

// clients remembers the events each client has had handled.
type clients struct {
	mu     sync.Mutex
	m      map[string]*tab
	once   sync.Once
	stored bool // the app has a database: events are recorded in loom_clients
}

type tab struct {
	seen    time.Time               // when the client last sent an event
	loaded  bool                    // last was read from loom_clients
	last    int64                   // the highest sequence handled
	running map[int64]chan struct{} // sequences queued or running, closed when done
}

// claim reserves (id, seq) for running. It reports false if the event
// was already handled, waiting first if it is running now.
func (a *App) claim(ctx context.Context, id string, seq int64) (bool, error) {
	cs := &a.clients
	cs.mu.Lock()
	c := cs.m[id]
	if c == nil {
		if cs.m == nil {
			cs.m = map[string]*tab{}
		}
		c = &tab{running: map[int64]chan struct{}{}}
		cs.m[id] = c
	}
	c.seen = time.Now()
	if !c.loaded && a.storeClients(ctx) {
		cs.mu.Unlock()
		last, err := data.Get[int64](ctx, a.db, `SELECT last_seq FROM loom_clients WHERE client_id = $1`, id)
		if err != nil && !errors.Is(err, data.ErrNotFound) {
			return false, err
		}
		cs.mu.Lock()
		c.loaded, c.last = true, max(c.last, last)
	}
	if seq <= c.last {
		cs.mu.Unlock()
		return false, nil
	}
	if done := c.running[seq]; done != nil {
		cs.mu.Unlock()
		<-done
		return a.claim(ctx, id, seq) // handled now, or released to run again
	}
	c.running[seq] = make(chan struct{})
	cs.mu.Unlock()
	return true, nil
}

// clientMemory is how long a client's entry stays in memory after its
// last event. A resend after that, from a tab left open for longer, is
// still deduplicated with a database (loom_clients); without one it may
// run again.
const clientMemory = time.Hour

// forgetClients drops the in-memory entries of clients last seen before
// cutoff with nothing running, so the map doesn't grow with every tab
// that ever sent an event. reap calls it.
func (a *App) forgetClients(cutoff time.Time) {
	cs := &a.clients
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for id, c := range cs.m {
		if len(c.running) == 0 && c.seen.Before(cutoff) {
			delete(cs.m, id)
		}
	}
}

// release ends a claim. If the event was handled it is recorded, so a
// resend is answered as handled.
func (a *App) release(ctx context.Context, id string, seq int64, handled bool) {
	if handled && a.storeClients(ctx) {
		_, err := a.db.Exec(ctx, `INSERT INTO loom_clients (client_id, last_seq) VALUES ($1, $2)
ON CONFLICT (client_id) DO UPDATE SET last_seq = greatest(loom_clients.last_seq, excluded.last_seq), seen_at = now()`, id, seq)
		if err != nil {
			a.logError("", fmt.Errorf("recording event %d of client %s as handled: %w", seq, id, err))
		}
	}
	cs := &a.clients
	cs.mu.Lock()
	defer cs.mu.Unlock()
	c := cs.m[id]
	if handled {
		c.last = max(c.last, seq)
	}
	if done := c.running[seq]; done != nil {
		close(done)
		delete(c.running, seq)
	}
}

// storeClients reports whether events are recorded in loom_clients: the
// app has a Postgres database. The table is one of Loom's migrations,
// checked at startup (RT433). The first time, it prunes clients not
// seen for clientRetention.
func (a *App) storeClients(ctx context.Context) bool {
	cs := &a.clients
	cs.once.Do(func() {
		if a.db == nil || a.db.SQL() == nil {
			return
		}
		cs.stored = true
		a.pruneClients(ctx, time.Now().Add(-clientRetention))
	})
	return cs.stored
}

// pruneClients deletes the clients not seen since before. The jobs
// runner's built-in prune runs it too.
func (a *App) pruneClients(ctx context.Context, before time.Time) error {
	_, err := a.db.Exec(ctx, `DELETE FROM loom_clients WHERE seen_at < $1`, before)
	return err
}

// eventResult is what a session answers an event with.
type eventResult struct {
	version int    // the page's version after the event, which the ack carries
	err     *Error // RT423 or an auth failure: the event is dropped
	status  int
	url     string // a patch's URL, after HandleParams (patch.go)
	retry   string // RT436: the Retry-After seconds
}

// serveEvent runs an event and acks it once its handler has run:
// 200 {"v": version}. See docs/wire.md.
func (a *App) serveEvent(w http.ResponseWriter, r *http.Request) {
	var req eventRequest
	// refuse answers an event that won't run, and records why, so a click
	// that did nothing leaves evidence (event.refused).
	refuse := func(status int, e *Error) {
		a.stream.Emit(r.Context(), trace.EventRefused, "sid", req.SID, "event", req.Event, "code", e.Code, "status", status)
		writeError(w, status, e)
	}
	if a.draining.Load() {
		w.Header().Set("Retry-After", "1")
		decodeJSON(w, r, &req) // for the event.refused record, if it reads
		refuse(http.StatusServiceUnavailable, &Error{Code: RT424, Msg: "the server is restarting; the event wasn't run", Fix: "nothing: the browser resends it to the restarted server"})
		return
	}
	if err := decodeJSON(w, r, &req); err != nil || req.Event == "" || (req.Client != "" && req.Seq < 1) {
		refuse(http.StatusBadRequest, &Error{Code: RT405, Msg: "malformed event request", Fix: `POST {"sid": "...", "client": "tab id", "seq": 1, "event": "name", "payload": {"key": "value"}}`})
		return
	}
	s := a.pageSession(r, req.SID)
	if s == nil {
		refuse(http.StatusNotFound, &Error{Code: RT404, Msg: "unknown or expired session, or one another browser loaded", Fix: "the browser remounts the page and resends the event"})
		return
	}
	ctx := context.WithoutCancel(r.Context()) // the bookkeeping finishes if the browser gives up
	var id string                             // the event's trace: its client and sequence, the same when it is resent
	if req.Client != "" {
		id = eventTrace(req.Client, req.Seq)
		ctx = trace.WithTrace(ctx, id)
		run, err := a.claim(ctx, req.Client, req.Seq)
		if err != nil {
			a.reportError(ctx, s.id, err)
			refuse(http.StatusServiceUnavailable, &Error{Code: RT424, Msg: "can't tell whether the event was handled: " + err.Error(), Fix: "nothing: the browser resends it"})
			return
		}
		if !run {
			a.stream.Emit(ctx, trace.EventDuplicate, "sid", s.id, "event", req.Event, "seq", req.Seq)
			writeAck(w, s.currentVersion(), true)
			return
		}
	}
	done := make(chan eventResult, 1)
	select {
	case s.events <- pageEvent{name: req.Event, payload: req.Payload, done: done, trace: id}:
	default:
		if req.Client != "" {
			a.release(ctx, req.Client, req.Seq, false)
		}
		refuse(http.StatusServiceUnavailable, &Error{Code: RT407, Msg: "too many events are waiting for this page", Fix: "make the page's event handlers return faster, or send fewer events (e.g. on:change instead of on:input)"})
		return
	}
	var res eventResult
	select {
	case res = <-done:
	case <-s.quit:
		select {
		case res = <-done:
		default: // the session ended before running it
			if req.Client != "" {
				a.release(ctx, req.Client, req.Seq, false)
			}
			refuse(http.StatusNotFound, &Error{Code: RT404, Msg: "the session ended before the event ran", Fix: "the browser remounts the page and resends the event"})
			return
		}
	}
	if req.Client != "" {
		a.release(ctx, req.Client, req.Seq, true)
	}
	if res.err != nil {
		if res.retry != "" {
			w.Header().Set("Retry-After", res.retry)
		}
		writeError(w, res.status, res.err)
		return
	}
	writeAck(w, res.version, false)
}

// eventTrace is the trace id of a browser event: a hash of its tab's
// client id with its sequence. A resent event has the same one, and
// the id, which logs show, doesn't reveal the client id that names the
// tab's events.
func eventTrace(client string, seq int64) string {
	h := sha256.Sum256([]byte(client))
	return fmt.Sprintf("%x-%d", h[:6], seq)
}

func writeAck(w http.ResponseWriter, version int, handled bool) {
	w.Header().Set("Content-Type", "application/json")
	if handled {
		fmt.Fprintf(w, `{"v":%s,"handled":true}`+"\n", strconv.Itoa(version))
		return
	}
	fmt.Fprintf(w, `{"v":%s}`+"\n", strconv.Itoa(version))
}

// currentVersion returns the version of the page's latest update.
func (s *session) currentVersion() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// shutdownGrace is how long shutdown waits for pages' queued events:
// Config.ShutdownGrace, which the app's job runner shares.
func (a *App) shutdownGrace() time.Duration { return a.cfg.ShutdownGrace }

// drain stops accepting events (they answer 503, RT424, and browsers
// resend them after the restart) and waits, up to the grace period, for
// every session to finish the events already queued.
func (a *App) drain(grace time.Duration) {
	a.draining.Store(true)
	a.mu.Lock()
	sessions := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.mu.Unlock()
	deadline := time.After(grace)
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := make(chan eventResult, 1)
			select { // queued after every event already waiting
			case s.events <- pageEvent{barrier: true, done: done}:
			case <-s.quit:
				return
			}
			select {
			case <-done:
			case <-s.quit:
			}
		}()
	}
	all := make(chan struct{})
	go func() { wg.Wait(); close(all) }()
	select {
	case <-all:
	case <-deadline:
		a.stream.Emit(context.Background(), trace.AppDrainTimeout, "msg", "loom: shutdown grace period ended with events still queued; browsers resend them after the restart")
	}
}
