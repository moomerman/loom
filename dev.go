package loom

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/render"
	"github.com/moomerman/loom/trace"
)

// Dev mode serves introspection endpoints under /_loom/dev/ for the loom
// CLI (`loom routes`, `loom sessions`, `loom inspect`, `loom render`) and
// agents. It is off unless App.Dev is set, which `loom dev` does through
// the LOOM_DEV environment variable, and it only answers requests from
// the local machine. The JSON shapes are documented in docs/cli.md.

// RouteInfo describes a live route.
type RouteInfo struct {
	Path       string   `json:"path"`
	Page       string   `json:"page"`        // Go type, e.g. *todo.TodoPage
	Component  string   `json:"component"`   // e.g. templates.TodoApp
	RootLayout string   `json:"root_layout"` // e.g. templates.Root, or loom.DefaultRoot
	Layout     string   `json:"layout"`      // the app layout, e.g. templates.App; "" for none
	Prefix     string   `json:"prefix"`      // the mount prefix, e.g. /admin/jobs; "" outside mounts
	Mount      string   `json:"mount"`       // the mounted sub-app's Go type, e.g. *jobsweb.App; "" outside mounts
	Auth       []string `json:"auth"`        // the auth checks, outer first, e.g. todo.requireAdmin
	Middleware int      `json:"middleware"`  // how many middleware wrap the route
	Watch      []string `json:"watch"`       // tables whose changes re-render the page (loom.Watch)
	RateLimit  []string `json:"rate_limit"`  // rate limits, outer first, e.g. "5 per minute by email on sign_in"
}

// SessionInfo describes a running page session.
type SessionInfo struct {
	SID         string    `json:"sid"`
	Path        string    `json:"path"`
	URL         string    `json:"url"` // the page's current URL, as patched (Ctx.Patch)
	Page        string    `json:"page"`
	Version     int       `json:"version"`
	Connected   int       `json:"connected"`    // open streams
	IdleSeconds float64   `json:"idle_seconds"` // since the last stream closed; 0 while connected
	Restarts    int       `json:"restarts"`     // in the last minute
	BusySeconds float64   `json:"busy_seconds"` // how long the running handler has run; 0 when idle
	StartedAt   time.Time `json:"started_at"`
}

// SessionDetail is a session with its current state.
type SessionDetail struct {
	SessionInfo
	Component string `json:"component"`
	Assigns   any    `json:"assigns"` // with template field names
	// LayoutAssigns are the live layout's assigns (see LiveLayout), or
	// absent if the page has none.
	LayoutAssigns any         `json:"layout_assigns,omitempty"`
	HTML          string      `json:"html"`
	Timers        []TimerInfo `json:"timers"` // pending c.SendAfter timers, soonest first
	Topics        []string    `json:"topics"` // PubSub topics the page and its live layout subscribe to
	Watch         []string    `json:"watch"`  // tables whose changes re-render the page (loom.Watch)
}

// TimerInfo is a page's pending timer, set with Ctx.SendAfter.
type TimerInfo struct {
	Event   string  `json:"event"`
	Payload Payload `json:"payload"`
	InMS    int64   `json:"in_ms"` // milliseconds until it runs
}

// ErrorInfo is a runtime error recorded for dev tools.
type ErrorInfo struct {
	Time    time.Time `json:"time"`
	Source  string    `json:"source"` // "server", or "browser" for one a page's scripts reported
	SID     string    `json:"sid,omitempty"`
	URL     string    `json:"url,omitempty"` // for a browser error, the page's URL
	Code    string    `json:"code,omitempty"`
	Message string    `json:"message"`
	Where   string    `json:"where,omitempty"`
	Fix     string    `json:"fix,omitempty"`
	Route   string    `json:"route,omitempty"` // the page's route, for a server error in a page
	Trace   string    `json:"trace,omitempty"` // the action that caused it: `loom trace ID`
}

// EventResult is the page after an event sent with App.SendEvent: its
// assigns and HTML, the errors the event caused, and what it did.
type EventResult struct {
	SessionDetail
	Errors []ErrorInfo `json:"errors"` // logged while handling the event, e.g. RT402 or RT419
	Ended  bool        `json:"ended"`  // the session ended (an auth check failed, or it failed too often)
	// Trace is the event's trace id: `loom trace ID` shows what it
	// caused later, such as its jobs running.
	Trace string `json:"trace"`
	// Events are what the event did while it ran: the handler's
	// page.event (ms, diff bytes), its commits, jobs enqueued and
	// broadcasts, oldest first.
	Events []trace.Event `json:"events"`
}

// ValidateResult is whether app.Validate passes, for `loom doctor`.
type ValidateResult struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// RenderResult is the result of rendering a component from JSON.
type RenderResult struct {
	Component string `json:"component"`
	HTML      string `json:"html"`
}

const maxRecentErrors = 50

// recentErrors keeps the last runtime errors for /_loom/dev/errors.
type recentErrors struct {
	mu    sync.Mutex
	list  []ErrorInfo
	total int // errors ever recorded
}

func (r *recentErrors) record(info ErrorInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, info)
	r.total++
	if len(r.list) > maxRecentErrors {
		r.list = r.list[len(r.list)-maxRecentErrors:]
	}
}

// count returns how many errors were ever recorded, for since.
func (r *recentErrors) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}

// since returns the errors recorded for sid after the first n of all
// errors ever recorded (as far as the list still holds them).
func (r *recentErrors) since(n int, sid string) []ErrorInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []ErrorInfo{}
	start := len(r.list) - (r.total - n)
	for _, e := range r.list[max(start, 0):] {
		if e.SID == sid {
			out = append(out, e)
		}
	}
	return out
}

func (r *recentErrors) all() []ErrorInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ErrorInfo{}, r.list...)
}

func (a *App) devMux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("GET /_loom/dev/routes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.Routes())
	})
	m.HandleFunc("GET /_loom/dev/manifest", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.Manifest())
	})
	m.HandleFunc("GET /_loom/dev/sessions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.Sessions())
	})
	m.HandleFunc("GET /_loom/dev/sessions/{sid}", func(w http.ResponseWriter, r *http.Request) {
		d, err := a.Inspect(r.PathValue("sid"))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, d)
	})
	m.HandleFunc("POST /_loom/dev/sessions/{sid}/event", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Event   string  `json:"event"`
			Payload Payload `json:"payload"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || req.Event == "" {
			writeError(w, http.StatusBadRequest, &Error{Code: RT405, Msg: "malformed event request", Fix: `POST {"event": "name", "payload": {"key": "value"}}`})
			return
		}
		res, err := a.SendEvent(r.PathValue("sid"), req.Event, req.Payload)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, res)
	})
	m.HandleFunc("POST /_loom/dev/render", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, &Error{Code: RT409, Msg: err.Error()})
			return
		}
		res, e := RenderComponent(r.URL.Query().Get("component"), body)
		if e != nil {
			writeError(w, http.StatusBadRequest, e)
			return
		}
		writeJSON(w, res)
	})
	m.HandleFunc("GET /_loom/dev/previews", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, Previews())
	})
	m.HandleFunc("GET /_loom/preview", a.serveGallery)
	m.HandleFunc("GET /_loom/preview/{component}", a.servePreview)
	m.HandleFunc("GET /_loom/dev/errors", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.errors.all())
	})
	m.HandleFunc("POST /_loom/dev/errors", a.serveBrowserError)
	m.HandleFunc("GET /_loom/dev/events", a.serveEvents)
	m.HandleFunc("GET /_loom/dev/validate", func(w http.ResponseWriter, r *http.Request) {
		res := ValidateResult{OK: true}
		if err := a.Validate(r.Context()); err != nil {
			info := errorInfo("", err)
			res = ValidateResult{Code: info.Code, Message: info.Message, Fix: info.Fix}
		}
		writeJSON(w, res)
	})
	m.HandleFunc("GET /_loom/dev/stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.Stats())
	})
	m.HandleFunc("GET /_loom/dev/queries", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.Queries())
	})
	m.HandleFunc("GET /_loom/dev/db/stats", func(w http.ResponseWriter, r *http.Request) {
		db := a.db
		if db == nil {
			db = data.NewLocal()
		}
		st, err := db.Stats(r.Context())
		if err != nil {
			e := &Error{Code: data.DB601, Msg: "reading the database's statistics: " + err.Error(), Fix: "check the database is reachable; the app's log has details"}
			if de := (*data.Error)(nil); errors.As(err, &de) {
				e = &Error{Code: de.Code, Msg: de.Msg, Fix: de.Fix}
			}
			writeError(w, http.StatusInternalServerError, e)
			return
		}
		writeJSON(w, st)
	})
	// The Go profiler, for `go tool pprof http://localhost:4000/_loom/dev/pprof/profile`.
	m.HandleFunc("GET /_loom/dev/pprof/", func(w http.ResponseWriter, r *http.Request) {
		if name := strings.TrimPrefix(r.URL.Path, "/_loom/dev/pprof/"); name != "" {
			pprof.Handler(name).ServeHTTP(w, r) // heap, goroutine, allocs, block, mutex, threadcreate
			return
		}
		pprof.Index(w, r)
	})
	m.HandleFunc("GET /_loom/dev/pprof/cmdline", pprof.Cmdline)
	m.HandleFunc("GET /_loom/dev/pprof/profile", pprof.Profile)
	m.HandleFunc("GET /_loom/dev/pprof/symbol", pprof.Symbol)
	m.HandleFunc("GET /_loom/dev/pprof/trace", pprof.Trace)
	m.HandleFunc("GET /_loom/dev/topics", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.Topics())
	})
	m.HandleFunc("GET /_loom/dev/mail", a.serveOutbox)
	m.HandleFunc("GET /_loom/dev/storage", a.serveStorage)
	a.devJobs(m)
	return m
}

// serveDev serves /_loom/dev/ and the preview gallery, /_loom/preview, in
// dev mode, to the local machine only.
// With Config.InspectToken, a request carrying it may also read the
// endpoints inspectable allows, in any mode and from anywhere.
func (a *App) serveDev(w http.ResponseWriter, r *http.Request) {
	switch {
	case a.dev && a.devRequest(r):
	case a.inspectRequest(r):
		if !inspectable(r) {
			http.Error(w, "the inspect token reads what the app does, not a page's state, and changes nothing; this endpoint needs dev mode (loom dev)", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
	case !a.dev:
		http.NotFound(w, r)
		return
	default:
		http.Error(w, "loom dev endpoints only answer local requests through loom dev", http.StatusForbidden)
		return
	}
	a.devOnce.Do(func() { a.devHandler = a.devMux() })
	a.devHandler.ServeHTTP(w, r)
}

// inspectRequest reports whether r carries the app's InspectToken.
func (a *App) inspectRequest(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return a.cfg.InspectToken != "" && ok && subtle.ConstantTimeCompare([]byte(token), []byte(a.cfg.InspectToken)) == 1
}

// inspected are the dev endpoints the inspect token may read: what the
// app does and how it performs. A session's assigns and HTML, rendering,
// events, the mailbox, storage and every POST stay dev-only, since they
// show users' data or change things.
var inspected = map[string]bool{
	"routes": true, "manifest": true, "validate": true, "sessions": true, "errors": true, "events": true,
	"stats": true, "queries": true, "db/stats": true, "topics": true,
	"pprof/heap": true, "pprof/allocs": true, "pprof/goroutine": true, // CPU profiles and traces stay dev-only: they cost while they run "jobs": true, "queues": true, "nodes": true, "cron": true,
}

var jobPath = regexp.MustCompile(`^jobs/[0-9]+$`)

func inspectable(r *http.Request) bool {
	p, ok := strings.CutPrefix(r.URL.Path, "/_loom/dev/")
	return ok && r.Method == http.MethodGet && (inspected[p] || jobPath.MatchString(p))
}

// DevOnly is a group option for dev tools served as ordinary pages, such
// as the devmail mailbox: the group's page loads, assets and handlers
// answer 404 unless the app runs in dev (App.Dev), and 403 to requests
// from other machines, like /_loom/dev/. It is checked on each request,
// so it holds however the app sets Dev. Live events don't pass through
// it, but a page that never loads has no session to send them to.
//
//	app.Mount("/dev/mail", devmail.New()) // devmail adds DevOnly itself
func DevOnly() Option { return devOnlyOption{} }

type devOnlyOption struct{}

func (devOnlyOption) apply(g *Group) {
	a := g.app
	g.dev = true
	g.middleware = append(g.middleware, func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case !a.dev:
				http.NotFound(w, r)
			case !a.devRequest(r):
				http.Error(w, "dev tools only answer local requests through loom dev", http.StatusForbidden)
			default:
				h.ServeHTTP(w, r)
			}
		})
	})
}

// devOnly reports whether g or a parent is DevOnly, for the manifest.
func (g *Group) devOnly() bool {
	for ; g != nil; g = g.parent {
		if g.dev {
			return true
		}
	}
	return false
}

// DevTokenHeader carries LOOM_DEV_TOKEN on requests through loom dev's
// proxy, which adds it to every request it forwards.
const DevTokenHeader = "X-Loom-Dev-Token"

// devRequest reports whether r may use dev mode's endpoints and pages:
// it comes from this machine and, under loom dev, through its proxy. A
// reverse proxy on the same machine that forwards other people's
// requests without X-Forwarded-For looks local, so the token is what
// keeps them out if dev mode is ever left on.
func (a *App) devRequest(r *http.Request) bool {
	if !isLocal(r) {
		return false
	}
	return a.devToken == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get(DevTokenHeader)), []byte(a.devToken)) == 1
}

// checkDev refuses to serve in dev mode where dev mode isn't safe:
// LOOM_DEV=1 without loom dev's token (RT439), say left in a production
// environment, or an address other machines can reach (RT440).
func (a *App) checkDev(addr string) error {
	if !a.dev {
		return nil
	}
	if a.devEnv && a.devToken == "" {
		return &Error{Code: RT439, Msg: "LOOM_DEV=1 is set without LOOM_DEV_TOKEN, so dev mode would answer anyone a local proxy forwards",
			Fix: "run the app with `loom dev`, which sets both; in production, unset LOOM_DEV"}
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil // net.Listen reports it
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return &Error{Code: RT440, Msg: fmt.Sprintf("dev mode is on and the app would listen on %q, which other machines can reach", addr),
			Fix: `listen on "localhost:4000" in dev, or turn dev mode off (unset LOOM_DEV, or App.Dev = false)`}
	}
	return nil
}

// isLocal reports whether a request comes from this machine, directly or
// through local proxies such as loom dev's (which set X-Forwarded-For).
func isLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return false
	}
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, ip := range strings.Split(v, ",") {
			if !net.ParseIP(strings.TrimSpace(ip)).IsLoopback() {
				return false
			}
		}
	}
	return true
}

// errorCode matches a Loom error code, such as RT417 or SY808.
var errorCode = regexp.MustCompile(`^[A-Z]{2}[0-9]{3}$`)

// serveBrowserError records an error a page's scripts reported: loom.js
// does in dev, for uncaught errors, rejected promises and console.error,
// which is how RT417 and RT418 reach
// `loom errors`. An error without a Loom code is RT420.
func (a *App) serveBrowserError(w http.ResponseWriter, r *http.Request) {
	var e struct {
		SID     string `json:"sid"`
		URL     string `json:"url"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Where   string `json:"where"`
		Fix     string `json:"fix"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&e); err != nil || e.Message == "" {
		writeError(w, http.StatusBadRequest, &Error{Code: RT405, Msg: "malformed error report", Fix: `POST {"message": "...", "code": "RT417", "url": "...", "sid": "..."}`})
		return
	}
	if !errorCode.MatchString(e.Code) {
		e.Code = RT420
		if e.Fix == "" {
			e.Fix = "a script on the page failed; fix it at the location shown, or see the browser's console"
		}
	}
	a.stream.Emit(r.Context(), trace.Error, "msg", e.Message, "source", "browser", "code", e.Code, "where", e.Where, "fix", e.Fix, "sid", e.SID, "url", e.URL)
	w.WriteHeader(http.StatusNoContent)
}

// Routes lists the app's live routes.
func (a *App) Routes() []RouteInfo {
	out := []RouteInfo{}
	for _, rt := range a.routes {
		out = append(out, RouteInfo{
			Path:       rt.path,
			Page:       fmt.Sprintf("%T", rt.prototype),
			Component:  qualified(rt.tmpl),
			RootLayout: rt.group.rootLayout().name(),
			Layout:     rt.group.appLayout().name(),
			Prefix:     rt.group.prefix,
			Mount:      rt.group.mountType(),
			Auth:       authNames(rt.auth),
			Middleware: rt.group.middlewareCount(),
			Watch:      append([]string{}, rt.watch...),
			RateLimit:  limitNames(rt.limits),
		})
	}
	return out
}

func authNames(checks []AuthFunc) []string {
	out := []string{}
	for _, c := range checks {
		name := funcName(c)
		if strings.HasPrefix(name, "loom.SignedIn.") {
			name = "loom.SignedIn" // the check's closure, loom.SignedIn.func1
		}
		out = append(out, name)
	}
	return out
}

// Sessions lists the running page sessions, oldest first.
func (a *App) Sessions() []SessionInfo {
	a.mu.Lock()
	list := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		list = append(list, s)
	}
	a.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].start.Before(list[j].start) })
	out := []SessionInfo{}
	for _, s := range list {
		if info, ok := s.info(); ok {
			out = append(out, info)
		}
	}
	return out
}

// Inspect returns a session's current assigns and HTML.
func (a *App) Inspect(sid string) (*SessionDetail, *Error) {
	notFound := &Error{Code: RT404, Msg: "no session " + sid, Fix: "list sessions with `loom sessions`"}
	s := a.session(sid)
	if s == nil {
		return nil, notFound
	}
	info, ok := s.info()
	if !ok {
		return nil, notFound
	}
	d := &SessionDetail{SessionInfo: info, Component: qualified(s.route.tmpl)}
	if !s.call(func() {
		d.Assigns = render.AssignsJSON(s.view.Assigns())
		if la := s.view.LayoutAssigns(); la != nil {
			d.LayoutAssigns = render.AssignsJSON(la)
		}
		d.HTML = s.view.HTML()
		d.Topics = append([]string{}, s.view.Topics()...)
		d.Watch = s.view.Watches()
		if d.Watch == nil {
			d.Watch = []string{}
		}
		d.Timers = []TimerInfo{}
		for _, t := range s.view.Timers() {
			p := t.Payload
			if p == nil {
				p = Payload{}
			}
			d.Timers = append(d.Timers, TimerInfo{Event: t.Event, Payload: p, InMS: max(time.Until(t.Due).Milliseconds(), 0)})
		}
	}) {
		return nil, notFound
	}
	return d, nil
}

// SendEvent sends an event to a session, as its browser would, and
// returns the page afterwards with the errors the event caused. Browsers
// showing the page get the diff as usual. It backs `loom event` and the
// send_event MCP tool.
func (a *App) SendEvent(sid, event string, payload Payload) (*EventResult, *Error) {
	notFound := &Error{Code: RT404, Msg: "no session " + sid, Fix: "list sessions with `loom sessions`, or open the page to start one"}
	s := a.session(sid)
	if s == nil {
		return nil, notFound
	}
	if payload == nil {
		payload = Payload{}
	}
	n, seq, id := a.errors.count(), a.stream.Last(), trace.NewID()
	if !s.call(func() { s.handle(pageEvent{name: event, payload: payload, trace: id}) }) {
		return nil, notFound
	}
	res := &EventResult{Errors: a.errors.since(n, sid), Trace: id, Events: a.stream.Since(seq, trace.Filter{Trace: id})}
	d, err := a.Inspect(sid)
	if err != nil {
		res.Ended = true
		res.SID = sid
		return res, nil
	}
	res.SessionDetail = *d
	return res, nil
}

// info describes the session, from its goroutine.
func (s *session) info() (SessionInfo, bool) {
	select {
	case <-s.quit:
		return SessionInfo{}, false
	default:
	}
	// Read without the session goroutine, so a page stuck in a handler
	// still shows, as busy.
	info := SessionInfo{
		SID:       s.id,
		Path:      s.route.path,
		Page:      fmt.Sprintf("%T", s.route.prototype),
		StartedAt: s.start.UTC(),
	}
	if b := s.busySince.Load(); b != 0 {
		info.BusySeconds = time.Since(time.Unix(0, b)).Seconds()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info.URL, info.Restarts = s.url, s.recent
	info.Version, info.Connected = s.version, len(s.subs)
	if !s.idleSince.IsZero() {
		info.IdleSeconds = time.Since(s.idleSince).Seconds()
	}
	return info, true
}

// RenderComponent renders any compiled component by name ("TodoApp" or
// "templates.TodoApp") with assigns given as JSON with template field
// names.
func RenderComponent(name string, assigns []byte) (*RenderResult, *Error) {
	t, err := render.Lookup(name)
	if err != nil {
		return nil, &Error{Code: RT408, Msg: err.Error(), Fix: "use a component name from the error, as in `loom render templates.TodoApp`"}
	}
	if len(assigns) == 0 {
		assigns = []byte("{}")
	}
	var r *render.Rendered
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = panicError("rendering "+name, p)
			}
		}()
		r, err = t.RenderJSON(assigns)
	}()
	var e *Error
	if errors.As(err, &e) {
		return nil, e
	}
	if err != nil {
		return nil, &Error{Code: RT409, Msg: err.Error(), Fix: "give assigns as a JSON object with the component's parameters, using template (snake_case) names"}
	}
	return &RenderResult{Component: qualified(t), HTML: r.HTML()}, nil
}

func qualified(t render.Template) string { return t.PackageName() + "." + t.ComponentName() }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // readable HTML in CLI output
	enc.Encode(v)
}
