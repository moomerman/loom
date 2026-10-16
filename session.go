package loom

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moomerman/loom/diff"
	"github.com/moomerman/loom/render"
	"github.com/moomerman/loom/trace"
)

// A session is one page load: a View owned by one goroutine, plus the
// browsers streaming its updates (normally one; more during a reconnect).
type session struct {
	id      string
	app     *App
	route   *route
	req     *http.Request
	browser string // the hash of the loading browser's BrowserCookie (browser.go)
	events  chan pageEvent
	calls   chan func() // run on the session goroutine, for dev tools
	quit    chan struct{}
	start   time.Time
	stop    sync.Once

	view          *View       // owned by the session goroutine
	handlesParams bool        // the page implements ParamsHandler (patch.go)
	pathValues    [][2]string // the URL's path values, which a patch keeps (patch.go)
	restarts      []time.Time // recent restarts, for the supervisor's limit
	unsubs        []func()    // PubSub subscriptions of the current view
	watching      sessionWatch
	timer         *time.Timer // fires when the view's next timer is due (armTimer)

	busySince atomic.Int64 // when the running handler started, in unix nanoseconds; 0 when idle

	mu        sync.Mutex // guards the fields below
	tree      *render.Rendered
	title     string                  // the page title as of version
	version   int                     // increases with every update sent
	subs      map[chan message]uint64 // open streams, numbered in the order they opened
	nextSub   uint64
	idleSince time.Time // when the last browser disconnected; zero while connected
	url       string    // the page's current URL, for loom sessions
	recent    int       // restarts in the last minute, for loom sessions
}

type pageEvent struct {
	name    string
	payload Payload
	info    *Message         // a published message instead of a browser event
	watch   bool             // a table the route watches changed (watch.go)
	timer   bool             // the page's next timer may be due (timer.go)
	patch   string           // the browser navigated to this URL of the route (patch.go)
	barrier bool             // nothing to run: shutdown waits for the events before it
	done    chan eventResult // answers a browser event, or a barrier, once run
	trace   string           // the trace id to handle it under; "" for a new one
}

// message is a Server-Sent Event.
type message struct {
	event string // "diff", "full", "reload", "navigate" or "patch"
	data  []byte
}

const (
	maxRestarts   = 5
	restartWindow = time.Minute
	eventBuffer   = 64
	maxStreams    = 2 // per session: one, and its replacement while reconnecting

)

func newSID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// startSession mounts a page and starts its goroutine. It returns the
// page's first render as a whole HTML document.
func (a *App) startSession(rt *route, r *http.Request, browser string, carry *carried) (s *session, doc string, err error) {
	start := time.Now()
	r = r.WithContext(trace.WithTrace(r.Context(), trace.NewID()))
	defer func() {
		sid := ""
		if s != nil {
			sid = s.id
		}
		a.stream.Emit(r.Context(), trace.PageMount, "sid", sid, "route", rt.path, "ms", time.Since(start), "outcome", outcome(err))
	}()
	if err := a.roomForSession(); err != nil {
		return nil, "", err
	}
	s = &session{
		id:        newSID(),
		app:       a,
		route:     rt,
		req:       r,
		browser:   browser,
		events:    make(chan pageEvent, eventBuffer),
		calls:     make(chan func()),
		start:     time.Now(),
		quit:      make(chan struct{}),
		subs:      map[chan message]uint64{},
		idleSince: time.Now(),
		url:       r.URL.RequestURI(),
	}
	v, err := newView(rt, r, s.id, true, carry)
	if err != nil {
		if !errors.As(err, new(*authError)) && !errors.As(err, new(*LimitError)) && !errors.Is(err, ErrPageNotFound) {
			a.reportError(r.Context(), s.id, err, "route", rt.path)
		}
		return nil, "", err
	}
	if url := v.Navigation(); url != "" {
		v.Close()
		return nil, "", &RedirectError{URL: url}
	}
	doc, err = v.Document()
	if err != nil {
		a.reportError(r.Context(), s.id, err, "route", rt.path)
		return nil, "", err
	}
	s.view = v
	for _, f := range v.Failures() {
		s.logError(f)
	}
	s.tree, s.title, s.version = v.tree, v.Title(), 1
	s.handlesParams = v.HandlesParams()
	s.pathValues = pathValues(rt.path, r.URL.Path)
	s.subscribeTopics()
	a.mu.Lock()
	a.sessions[s.id] = s
	a.mu.Unlock()
	if len(rt.watch) > 0 {
		a.watch(s)
	}
	s.armTimer()
	go s.run()
	return s, doc, nil
}

// slowHandler is how long a handler runs before it is RT445; a variable
// so tests can shorten it.
var slowHandler = 10 * time.Second

// roomForSession refuses a new page when Config.MaxSessions are open
// (RT444): each holds a goroutine and its page's state until its browser
// has been gone for SessionTimeout, so without a cap a crawler following
// links could exhaust memory. The count is checked before mounting, so
// pages loading at the same moment can pass it together; the cap is a
// bound, not an exact limit.
func (a *App) roomForSession() error {
	a.mu.Lock()
	n := len(a.sessions)
	a.mu.Unlock()
	if n < a.cfg.MaxSessions {
		return nil
	}
	err := &Error{Code: RT444, Msg: fmt.Sprintf("%d live pages are open, Config.MaxSessions, so a page load was refused", n),
		Fix: "raise loom.Config.MaxSessions if the server has the memory, or lower SessionTimeout so pages left open close sooner; browsers retry after Retry-After"}
	if a.full.CompareAndSwap(false, true) { // log once while full, not per refusal
		a.logError("", err)
		time.AfterFunc(10*time.Second, func() { a.full.Store(false) })
	}
	return err
}

func (a *App) session(sid string) *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[sid]
}

// run is the session goroutine: the only code that touches s.view.
func (s *session) run() {
	for {
		select {
		case ev := <-s.events:
			s.handle(ev)
		case f := <-s.calls:
			f()
		case <-s.quit:
			if s.timer != nil {
				s.timer.Stop()
			}
			s.unsubscribeTopics()
			if len(s.route.watch) > 0 {
				s.app.unwatch(s)
			}
			s.view.Close()
			return
		}
	}
}

func (s *session) handle(ev pageEvent) {
	defer s.armTimer() // the handler, or a restart, may have changed the timers
	stopWatch := s.watchdog(ev)
	res := eventResult{status: http.StatusOK}
	// end stops the session, but only after the event's answer is sent:
	// serveEvent answers 404 if the session ends with no answer waiting.
	var end string
	defer func() {
		stopWatch() // before the answer, so whoever got it sees the page idle
		if ev.done != nil {
			if res.err == nil {
				res.version = s.currentVersion()
			}
			ev.done <- res
		}
		if end != "" {
			s.close(end)
		}
	}()
	if ev.barrier {
		return
	}
	// Emitted before the answer is sent, so a caller that waits for it
	// (SendEvent) finds the event in the stream.
	start, sent := time.Now(), 0
	var err error
	defer func() { s.handled(ev, start, sent, res, err) }()
	s.view.setTrace(ev.trace)
	var d map[string]any
	switch {
	case ev.watch:
		s.watching.pending.Store(false) // a change from now on renders again
		s.watching.last.Store(time.Now().UnixNano())
		d, err = s.view.Refresh()
	case ev.info != nil:
		d, err = s.view.Info(*ev.info)
	case ev.timer:
		d, err = s.view.RunTimers()
	case ev.patch != "":
		d, err = s.view.Patch(ev.patch)
		s.view.Patched() // the browser pushes the URL it gets in the answer
		res.url = s.view.URL()
	default:
		d, err = s.view.Event(ev.name, ev.payload)
	}
	for _, f := range s.view.Failures() {
		s.logError(f)
	}
	var denied *authError
	if errors.As(err, &denied) {
		res.status, res.err = http.StatusForbidden, &Error{Code: RT404, Msg: "an auth check refused the event, so the session ended", Fix: "the browser reloads the page, which answers 403 or redirects"}
		end = "auth" // the browser reloads, and the page load answers 403 or redirects
		return
	}
	var over *LimitError
	if errors.As(err, &over) {
		res.status, res.err, res.retry = http.StatusTooManyRequests, over.Err, over.retryAfter()
		return
	}
	var e *Error
	if errors.As(err, &e) && (e.Code == RT423 || e.Code == RT438) {
		s.logError(err) // nothing ran, so the page is fine
		res.status, res.err = http.StatusBadRequest, e
		return
	}
	if err != nil {
		s.logError(err)
		if ev.patch != "" {
			// The browser loads the URL afresh instead.
			res.status, res.err = http.StatusInternalServerError, &Error{Code: RT402, Msg: "the page failed handling the new URL"}
		}
		if !s.restart() {
			end = "failed"
		}
		return
	}
	s.req = s.view.ctx.Request // a restart mounts the page at its current URL
	s.mu.Lock()
	s.url = s.req.URL.RequestURI()
	s.mu.Unlock()
	s.subscribeTopics()
	if d != nil || s.view.Title() != s.title {
		sent = s.publish("diff", "diff", d, s.view.tree)
	}
	if url, replace := s.view.Patched(); url != "" {
		s.patched(url, replace)
	}
	if url := s.view.Navigation(); url != "" {
		s.navigate(url)
	}
}

// watchdog marks the session busy while it handles ev, and reports a
// handler that runs past slowHandler (RT445): until it returns, the page
// answers nothing else, and its events queue up and then fail (RT407).
// It returns the func that ends the watch.
func (s *session) watchdog(ev pageEvent) func() {
	start := time.Now()
	s.busySince.Store(start.UnixNano())
	ctx := s.view.ctx.Context() // the trace of the event before ev's; ev's is set once it starts
	slow := time.AfterFunc(slowHandler, func() {
		s.app.reportError(ctx, s.id, &Error{Code: RT445, Msg: fmt.Sprintf("%s has been running for %v, and the page answers nothing else until it returns", ev.what(), slowHandler),
			Fix: "pass c.Context() to database calls so they end with the page, and enqueue slow work as a job (docs/jobs.md); `loom sessions` shows the page as busy"}, "route", s.route.path)
	})
	return func() {
		slow.Stop()
		s.busySince.Store(0)
	}
}

// what names an event for messages: the browser event, or the kind of
// work.
func (ev pageEvent) what() string {
	switch {
	case ev.watch:
		return "re-rendering for a watched table"
	case ev.info != nil:
		return fmt.Sprintf("HandleInfo for %q on %q", ev.info.Event, ev.info.Topic)
	case ev.timer:
		return "a timer's handler"
	case ev.patch != "":
		return "HandleParams"
	case ev.barrier:
		return "shutdown's wait"
	}
	return fmt.Sprintf("the handler of event %q", ev.name)
}

// armTimer sets the session's timer for the view's soonest timer, or
// stops it if there is none. It runs on the session goroutine. A firing
// that finds nothing due (the timer was replaced in the meantime) runs
// nothing, and arms again.
func (s *session) armTimer() {
	select {
	case <-s.quit:
		return
	default:
	}
	at, ok := s.view.NextTimer()
	if !ok {
		if s.timer != nil {
			s.timer.Stop()
		}
		return
	}
	d := time.Until(at)
	if s.timer == nil {
		s.timer = time.AfterFunc(d, s.fireTimer)
		return
	}
	s.timer.Reset(d)
}

// fireTimer queues the page's due timers to run on its goroutine.
func (s *session) fireTimer() {
	select {
	case s.events <- pageEvent{timer: true}:
	case <-s.quit:
	}
}

// restart is the supervisor: after a failure it replaces the page with a
// freshly mounted copy and resyncs the browser. A page that keeps failing
// is stopped, and the browser reloads: restart reports false, and the
// caller closes the session.
func (s *session) restart() bool {
	now := time.Now()
	recent := s.restarts[:0]
	for _, t := range s.restarts {
		if now.Sub(t) < restartWindow {
			recent = append(recent, t)
		}
	}
	s.restarts = append(recent, now)
	s.mu.Lock()
	s.recent = len(s.restarts)
	s.mu.Unlock()
	if len(s.restarts) > maxRestarts {
		s.logError(errors.New("page failed too often; stopping its session"))
		return false
	}
	ctx := s.view.ctx.Context() // the failed action's trace
	s.app.stream.Emit(ctx, trace.PageRestart, "msg", "loom: the page failed, so it was mounted afresh", "sid", s.id, "route", s.route.path, "restarts", len(s.restarts))
	s.unsubscribeTopics()
	s.view.Close()
	v, err := newView(s.route, s.req.WithContext(trace.WithTrace(s.req.Context(), trace.ID(ctx))), s.id, true, nil)
	if err != nil {
		if !errors.As(err, new(*authError)) {
			s.app.reportError(ctx, s.id, err, "route", s.route.path)
		}
		return false
	}
	v.Navigation() // a restart stays on the page
	s.view = v
	for _, f := range v.Failures() {
		s.logError(f)
	}
	s.subscribeTopics()
	s.publish("full", "tree", v.Tree(), v.tree)
	return true
}

// subscribeTopics subscribes to the topics the view added since the last
// call. Messages are queued as events for the session goroutine.
func (s *session) subscribeTopics() {
	for _, topic := range s.view.NewTopics() {
		s.unsubs = append(s.unsubs, s.app.cfg.PubSub.Subscribe(topic, func(m Message) {
			select {
			case s.events <- pageEvent{info: &m}:
			case <-s.quit:
			default:
				s.app.reportError(context.Background(), s.id, &Error{Code: RT407, Msg: fmt.Sprintf("too many events are waiting for this page; dropped a %q message on %q", m.Event, m.Topic), Fix: "make HandleInfo and the event handlers return faster, or publish fewer messages"}, "route", s.route.path)
			}
		}))
	}
}

// unsubscribeTopics drops the current view's subscriptions.
func (s *session) unsubscribeTopics() {
	for _, u := range s.unsubs {
		u()
	}
	s.unsubs = nil
}

// publish records the latest tree and sends an update to every browser:
// payload (a diff or full tree, if any) in field, and the page's title
// if it changed or the tree is full. It returns the update's size.
func (s *session) publish(event, field string, payload map[string]any, tree *render.Rendered) int {
	title := s.view.Title()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	m := map[string]any{"v": s.version}
	if payload != nil {
		m[field] = payload
	}
	if title != s.title || event == "full" {
		m["title"] = title
	}
	s.tree, s.title = tree, title
	data, err := diff.Marshal(m)
	if err != nil {
		s.app.logError(s.id, err)
		return 0
	}
	s.broadcast(message{event: event, data: data})
	return len(data)
}

// broadcast sends m to every subscriber. A browser too slow to keep up is
// disconnected; it reconnects and resyncs. Must be called with s.mu held.
func (s *session) broadcast(m message) {
	for ch := range s.subs {
		select {
		case ch <- m:
		default:
			delete(s.subs, ch)
			close(ch)
			s.app.stream.Emit(context.Background(), trace.StreamSlow, "msg", "loom: a browser couldn't keep up with its page's updates, so its stream was closed; it reconnects and resyncs", "sid", s.id)
		}
	}
}

// subscribe registers a browser that has version v. If v is not current,
// the first message it receives is the full tree. A session keeps at
// most maxStreams: a new one beyond that closes the oldest, so a client
// opening streams in a loop holds no more than two.
func (s *session) subscribe(v int) chan message {
	ch := make(chan message, eventBuffer)
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.subs) >= maxStreams {
		var oldest chan message
		for c, n := range s.subs {
			if oldest == nil || n < s.subs[oldest] {
				oldest = c
			}
		}
		delete(s.subs, oldest)
		close(oldest)
	}
	if v != s.version {
		data, err := diff.Marshal(map[string]any{"v": s.version, "tree": diff.Full(s.tree), "title": s.title})
		if err == nil {
			ch <- message{event: "full", data: data}
		}
	}
	s.nextSub++
	s.subs[ch] = s.nextSub
	s.idleSince = time.Time{}
	return ch
}

func (s *session) unsubscribe(ch chan message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subs, ch)
	if len(s.subs) == 0 {
		s.idleSince = time.Now()
	}
}

// call runs f on the session goroutine, so f may read s.view. It reports
// false if the session stopped first.
func (s *session) call(f func()) bool {
	done := make(chan struct{})
	select {
	case s.calls <- func() { f(); close(done) }:
		<-done
		return true
	case <-s.quit:
		return false
	}
}

// close stops the session and tells its browsers to reload. reason is
// why, for page.close: "idle", "navigated", "auth" or "failed".
func (s *session) close(reason string) {
	s.stop.Do(func() {
		s.app.stream.Emit(context.Background(), trace.PageClose, "sid", s.id, "route", s.route.path, "reason", reason)
		close(s.quit)
		s.app.mu.Lock()
		delete(s.app.sessions, s.id)
		s.app.mu.Unlock()
		s.mu.Lock()
		s.broadcast(message{event: "reload", data: []byte("{}")})
		s.mu.Unlock()
	})
}

// reap stops sessions that have had no browser for SessionTimeout.
func (a *App) reap() {
	tick := max(a.cfg.SessionTimeout/4, 10*time.Millisecond)
	for range time.Tick(tick) {
		a.mu.Lock()
		var idle []*session
		for _, s := range a.sessions {
			s.mu.Lock()
			if !s.idleSince.IsZero() && time.Since(s.idleSince) > a.cfg.SessionTimeout {
				idle = append(idle, s)
			}
			s.mu.Unlock()
		}
		a.mu.Unlock()
		for _, s := range idle {
			s.close("idle")
		}
		a.forgetClients(time.Now().Add(-clientMemory))
		a.limiter.prune(a, a.limitNow())
	}
}

// logError reports err as the page's, with its current action's trace
// and its route.
func (s *session) logError(err error) {
	s.app.reportError(s.view.ctx.Context(), s.id, err, "route", s.route.path)
}

// handled emits the page.* event for an event, message, re-render, timer
// or patch the session handled: how long it took, whether it changed the
// page, the update's size, and how it ended.
func (s *session) handled(ev pageEvent, start time.Time, sent int, res eventResult, err error) {
	name, attrs := trace.PageEvent, []any{"sid", s.id, "route", s.route.path}
	switch {
	case ev.watch:
		name = trace.PageWatch
	case ev.info != nil:
		name, attrs = trace.PageInfo, append(attrs, "topic", ev.info.Topic, "event", ev.info.Event)
	case ev.timer:
		name = trace.PageTimer
	case ev.patch != "":
		name, attrs = trace.PagePatch, append(attrs, "url", ev.patch)
	default:
		attrs = append(attrs, "event", ev.name)
	}
	attrs = append(attrs, "ms", time.Since(start), "diff", sent > 0, "bytes", sent, "outcome", outcome(err))
	if res.err != nil {
		attrs = append(attrs, "code", res.err.Code)
	} else if e := (*Error)(nil); errors.As(err, &e) {
		attrs = append(attrs, "code", e.Code)
	}
	s.app.stream.Emit(s.view.ctx.Context(), name, attrs...)
}

// outcome names how a mount or handler ended, for page.* events.
func outcome(err error) string {
	var e *Error
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, new(*authError)):
		return "denied"
	case errors.As(err, new(*LimitError)):
		return "limited"
	case errors.As(err, new(*RedirectError)):
		return "redirect"
	case errors.Is(err, ErrPageNotFound):
		return "not_found"
	case errors.As(err, &e) && (e.Code == RT423 || e.Code == RT438):
		return "refused"
	case errors.As(err, &e) && e.Code == RT444:
		return "full"
	}
	return "failed"
}
