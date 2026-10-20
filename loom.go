// Package loom is a live-first web framework: pages render on the server
// from compiled .hx templates, and every change to a page's state is sent
// to the browser as a small diff over Server-Sent Events.
//
//	type TodoPage struct{ todos []templates.Todo }
//
//	func (p *TodoPage) Mount(c *loom.Ctx) {}
//
//	func (p *TodoPage) Assigns(c *loom.Ctx) templates.TodoListAssigns {
//		return templates.TodoListAssigns{Todos: p.todos}
//	}
//
//	func (p *TodoPage) Events() loom.Events {
//		return loom.Events{"add": p.add}
//	}
//
//	func (p *TodoPage) add(c *loom.Ctx, payload loom.Payload) {
//		p.todos = append(p.todos, templates.Todo{Title: payload["title"]})
//	}
//
//	func main() {
//		app := loom.New()
//		app.Live("/", &TodoPage{}, templates.TodoList)
//		app.Run(":4000")
//	}
//
// Each page load gets its own copy of the page value, owned by one
// goroutine, so Mount and the event handlers never run concurrently for
// a page.
package loom

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/pubsub"
	"github.com/moomerman/loom/render"
	"github.com/moomerman/loom/storage"
	"github.com/moomerman/loom/trace"
)

// Page is a live page. Its fields hold the page's state. Besides Mount,
// a page has an Assigns method returning its template's assigns type,
// and usually Events and HandleInfo:
//
//	func (p *TodoPage) Assigns(c *loom.Ctx) templates.TodoListAssigns
//	func (p *TodoPage) Events() loom.Events                    // see Events
//	func (p *TodoPage) HandleInfo(c *loom.Ctx, msg loom.Message) // see InfoHandler
//
// Assigns is checked against the template when the route is registered
// (RT406), so a page and its template always agree; a wrong field or
// type in Assigns is a go build error.
type Page interface {
	// Mount is called once per page load, before the first render, to
	// load the page's state.
	Mount(c *Ctx)
}

// Events maps the events a page's template sends to the funcs that
// handle them. Each func changes the page's state; the runtime then
// calls Assigns, renders and sends the difference. Every event the
// page's template, the components it calls and its app layout send must
// have a handler, checked when the route is registered (RT422).
//
//	func (p *TodoPage) Events() loom.Events {
//		return loom.Events{"add": p.add, "toggle": p.toggle, "delete": p.delete}
//	}
//
// Events is called on each page load's copy of the page, so method
// values like p.add act on that copy. It must only build the map.
type Events map[string]func(c *Ctx, payload Payload)

// Assigns are the values a page renders its template with: the
// template's generated assigns struct, e.g. templates.TodoListAssigns.
type Assigns = any

// Payload is an event's data: the val-* attributes of the element that
// fired it (without the prefix), plus "value" for input, select and
// textarea elements, "checked" ("true" or "false") for checkboxes and
// radio buttons, "key" for keyboard events, and the named fields of a
// submitted form. See docs/live.md.
type Payload map[string]string

// Ctx is the context of a page call.
type Ctx struct {
	SID     string        // the page's session id
	Request *http.Request // the request that loaded the page

	app   *App
	scope *render.Scope   // where the page is mounted
	ctx   context.Context // lives as long as the page
	user  signedIn        // who is signed in, as last looked up (users.go)

	topics    []string        // subscribed topics, in order
	newTopics []string        // subscribed since the runtime last looked
	inLayout  bool            // the live layout's code is running, not the page's
	layoutSub map[string]bool // topics the live layout subscribed to
	pageSub   map[string]bool // topics the page subscribed to
	layout    Page            // the live layout's state, for LayoutOf; nil if none

	title    string
	meta     Meta
	ld       string // meta's StructuredData, encoded
	loaded   bool   // the page's document has been rendered, so Meta is too late
	values   map[string]any
	changed  bool    // the title or a value was set since the last render
	navigate string  // where Navigate sends the browser, until the runtime looks
	notFound bool    // Mount called NotFound
	patch    *patch  // the URL Patch changes to, until the runtime looks
	failed   []error // reported with Fail, until the runtime looks

	hasEvent   func(name string) bool // whether the page handles an event, for SendAfter
	eventNames func() []string
	timerOps   []timerOp // SendAfter and CancelTimer calls, until the View settles them
}

// InfoHandler is a page that receives messages published on topics it
// subscribed to with Ctx.Subscribe: from background jobs (jobs.Ctx's
// Broadcast) or other code that publishes on App.PubSub.
type InfoHandler interface {
	// HandleInfo runs for each message, on the page's goroutine like an
	// event handler, and changes the page's state; the runtime then calls
	// Assigns and renders.
	HandleInfo(c *Ctx, msg Message)
}

// Message is a message published on a topic: an event name and a string
// payload, like a browser event.
type Message = pubsub.Message

// Subscribe makes the page receive messages published on topic, in
// HandleInfo, for as long as the page is open. The page must implement
// InfoHandler (RT410). Subscribing twice to a topic does nothing.
//
//	func (p *ExportPage) Mount(c *loom.Ctx) {
//		c.Subscribe("exports:" + p.userID)
//		...
//	}
//
// A live layout subscribes the same way and gets the messages on its
// topics in its own HandleInfo; a topic both subscribe to reaches both.
func (c *Ctx) Subscribe(topic string) {
	subs := &c.pageSub
	if c.inLayout {
		subs = &c.layoutSub
	}
	if *subs == nil {
		*subs = map[string]bool{}
	}
	(*subs)[topic] = true
	for _, t := range c.topics {
		if t == topic {
			return
		}
	}
	c.topics = append(c.topics, topic)
	c.newTopics = append(c.newTopics, topic)
}

// Fail reports an error the page handled without panicking, such as a
// database write that failed in an event handler. The error is logged and listed
// by `loom errors` and the recent_errors MCP tool (RT419, or the error's
// own code, such as a DB6xx), and loomtest's Mount, Event and Info fail
// the test. The page keeps running with the assigns it returns, so
// return assigns that tell the user what happened:
//
//	if err := p.store.Add(ctx, title); err != nil {
//		c.Fail(err)
//		return p.assigns(c, "Couldn't save the todo; try again.")
//	}
//
// For a bug the page can't recover from, panic instead: the page is
// restarted with a fresh Mount (RT402).
func (c *Ctx) Fail(err error) {
	if err != nil {
		c.failed = append(c.failed, failError(err, callerOutsideLoom()))
	}
}

// Topics returns the topics the page has subscribed to.
func (c *Ctx) Topics() []string { return append([]string(nil), c.topics...) }

// Title sets the page's title, which the root layout's <.live_title>
// shows. Set it in Mount or a handler; when it changes, the browser's title
// is updated along with the next diff.
func (c *Ctx) Title(title string) {
	if title != c.title {
		c.title, c.changed = title, true
	}
}

// Meta is what a page says about itself to link previews and search
// engines, rendered into the document's <head> by the root layout's
// <.live_meta />. Image and Canonical may be paths, such as
// c.Asset("cards/c64.png") or c.Path("/c64/" + slug); they become
// absolute URLs on Config.BaseURL (or, without one, the request's host).
type Meta struct {
	Description string // a sentence or two: what the page is
	Image       string // a picture for link previews, ideally 1200×630
	Canonical   string // the page's one URL, when it can be reached by several
	// StructuredData is schema.org JSON-LD for search engines: any value
	// encoding/json marshals, usually a map with "@context" and "@type".
	// Its URLs aren't rewritten, so make them absolute with c.URL.
	StructuredData any
	// NoIndex asks search engines not to index the page (robots
	// noindex), for pages such as search results that only repeat
	// others. Its links are still followed.
	NoIndex bool
}

// Meta sets the page's metadata. Call it in Mount (or the HandleParams
// that runs with Mount): it is rendered with the document, on a page
// load or a live navigation, for link previews and search engines,
// which never see a later change. Changing it after the page has loaded
// is RT449, and StructuredData that encoding/json can't marshal is
// RT450; both are reported like c.Fail.
//
//	c.Meta(loom.Meta{Description: tune.Title + " by " + tune.Credit, Image: c.Asset("cards/c64.png")})
func (c *Ctx) Meta(m Meta) {
	ld, err := jsonLD(m.StructuredData)
	if err != nil {
		c.failed = append(c.failed, &Error{Code: RT450, Msg: "c.Meta's StructuredData can't be encoded as JSON: " + err.Error(),
			Fix: "build it from maps, slices, strings, numbers and structs; encoding/json can't marshal channels, functions or cycles"})
		m.StructuredData, ld = nil, ""
	}
	if c.loaded && (m.Description != c.meta.Description || m.Image != c.meta.Image || m.Canonical != c.meta.Canonical || m.NoIndex != c.meta.NoIndex || ld != c.ld) {
		c.failed = append(c.failed, &Error{Code: RT449, Msg: "c.Meta was called after the page loaded, where nothing reads it",
			Fix: "set the page's metadata in Mount (or the HandleParams that runs with it); link previews and search engines only read the first load"})
		return
	}
	c.meta, c.ld = m, ld
}

// jsonLD encodes structured data for a <script type="application/ld+json">:
// encoding/json escapes <, > and &, so it can't end the script early.
func jsonLD(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	return string(b), err
}

// TitleOr returns the page's title, or def if it hasn't set one.
func (c *Ctx) TitleOr(def string) string {
	if c.title == "" {
		return def
	}
	return c.title
}

// Set stores a value for the page's app layout, whose assigns func reads
// it with Get. Layout assigns are recomputed on every render, including
// after a handler that only sets a value.
//
//	c.Set("unread", 3) // in a handler; the layout func reads c.Get("unread")
func (c *Ctx) Set(key string, value any) {
	if c.values == nil {
		c.values = map[string]any{}
	}
	c.values[key], c.changed = value, true
}

// Get returns the value stored with Set, or nil.
func (c *Ctx) Get(key string) any { return c.values[key] }

// Context returns a context that lives as long as the page: it is
// cancelled when the page's session ends or the page restarts. Use it,
// not c.Request.Context(), for work in handlers and HandleInfo: the
// request that loaded the page has finished by then, and its context
// is cancelled. It carries the values of the request's context.
func (c *Ctx) Context() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// Path returns p, a path within the page's mount, as a path from the
// site root: the Go twin of the path template builtin. In a sub-app
// mounted at /admin/jobs, c.Path("/jobs/1") is "/admin/jobs/jobs/1".
// Outside any mount it returns p.
func (c *Ctx) Path(p string) string { return render.Path(c.scope, p) }

// URL is c.Path(p) as an absolute URL, on Config.BaseURL or the
// request's origin, for what is read away from the page: structured
// data, feeds, mail.
func (c *Ctx) URL(p string) string { return c.app.URL(c.Request, c.Path(p)) }

// Asset returns the URL of a file, or of a directory when name ends in
// "/", embedded by the page's group with loom.Assets: the Go twin of
// the asset template builtin. An unknown name panics.
func (c *Ctx) Asset(name string) string { return render.Asset(c.scope, name) }

// App is a Loom application.
type App struct {
	cfg      Config // with defaults filled in (config.go)
	dev      bool   // dev mode: Config.Dev, or LOOM_DEV
	devEnv   bool   // dev mode came from LOOM_DEV, not the app's code
	devToken string // LOOM_DEV_TOKEN: required on dev requests when set

	db       *DB
	jobStore jobs.Store
	jobs     *jobs.Runner
	jobsOnce sync.Once
	jobsErr  error
	closing  chan struct{} // closed when Run begins shutting down

	clientOnce         sync.Once
	loomJS, navigateJS string // content-hashed URLs (clientScripts)

	stream     *trace.Stream // the event stream (events.go)
	started    time.Time     // when New made the app, for `loom stats`
	errors     recentErrors
	devOnce    sync.Once
	devHandler *http.ServeMux
	keepOnce   sync.Once
	keepKey    string // seals kept state in tests, with no secret (keep.go)
	mux        *http.ServeMux
	mu         sync.Mutex
	sessions   map[string]*session
	reaper     sync.Once

	watches   watchHub
	users     users // signed-in users' sessions and the user loader (users.go)
	mailMu    sync.Mutex
	mailer    Mailer      // the app's mailer (mail.go)
	outbox    outbox      // in dev without a mailer, the mail sent, for `loom mail`
	clients   clients     // events handled per browser tab (outbox.go)
	limiter   limiter     // rate limit counts (ratelimit.go)
	limitSeq  int         // rate limits registered, to name each one's counts
	draining  atomic.Bool // shutting down: events answer 503 (outbox.go)
	full      atomic.Bool // RT444 was logged in the last 10 seconds (session.go)
	plainSeen atomic.Bool // RT442 was logged (config.go)
	unready   atomic.Bool // shutdown began: /_loom/health answers 503 (health.go)
	health    healthCache

	storage     storage.Bucket // Config.Storage or its default (storage.go)
	storageOnce sync.Once

	top             *Group // routes outside any group
	styles          stylesheets
	scripts         scripts
	routes          []*route
	handlers        []handler                     // plain HTTP handlers, for the manifest
	subMigrations   []HasMigrations               // mounted sub-apps with migrations (migrations.go)
	startupChecks   []func(context.Context) error // added with StartupCheck (mail.go)
	errorPage       *errorPage                    // set with ErrorPage (errorpage.go)
	routesByPattern map[string]*route
	assetPrefixes   map[string]bool // URL prefixes of groups' embedded assets
}

type route struct {
	path      string // the full path, with any mount prefix
	prototype Page
	tmpl      render.Template
	group     *Group
	watch     []string      // tables whose changes re-render the page (Watch)
	auth      []AuthFunc    // every check from the group chain, outer first
	limits    []*limit      // every rate limit from the group chain, then the route's own
	scope     *render.Scope // the mount prefix and assets, for path and asset

	styleOnce sync.Once
	styled    *render.Scope // scope plus the page's stylesheet and script; see styledScope
}

// New returns an App configured by cfg, at most one; see Config. It
// panics with RT441 on an invalid value.
//
//	app := loom.New(loom.Config{DB: os.Getenv("DATABASE_URL")})
func New(cfg ...Config) *App {
	if len(cfg) > 1 {
		panic("loom.New takes at most one Config")
	}
	var c Config
	if len(cfg) == 1 {
		c = cfg[0]
	}
	c, err := c.withDefaults()
	if err != nil {
		panic(err)
	}
	a := &App{
		cfg:             c,
		stream:          newStream(c),
		started:         time.Now(),
		devToken:        os.Getenv("LOOM_DEV_TOKEN"),
		closing:         make(chan struct{}),
		mux:             http.NewServeMux(),
		sessions:        map[string]*session{},
		routesByPattern: map[string]*route{},
		assetPrefixes:   map[string]bool{},
	}
	if a.cfg.PubSub == nil {
		a.cfg.PubSub = newNodePubSub(a) // NOTIFY across nodes with a database (notify.go)
	}
	switch c.Dev {
	case DevOn:
		a.dev = true
	case DevFromEnv:
		a.dev = os.Getenv("LOOM_DEV") == "1"
		a.devEnv = a.dev
	}
	if c.DB != "" {
		a.openConfigDB(c.DB)
	}
	a.stream.AddSink(trace.SinkFunc(a.recordError))
	a.top = &Group{app: a, root: a.defaultRoot()}
	a.mux.HandleFunc("GET /_loom/loom.js", a.serveClient)
	a.mux.HandleFunc("GET /_loom/navigate.js", a.serveNavigateJS)
	a.mux.HandleFunc("GET /_loom/stream", a.serveStream)
	a.mux.HandleFunc("POST /_loom/event", a.serveEvent)
	a.mux.HandleFunc("GET /_loom/signin/{token}", a.serveSignIn)
	a.mux.HandleFunc("GET /_loom/signout", a.serveSignOut)
	a.mux.HandleFunc("GET "+stylesheetPrefix+"{file}", a.serveStylesheet)
	a.mux.HandleFunc("GET "+scriptPrefix+"{file}", a.serveScript)
	return a
}

// Live serves a live page at path, in the root layout and with no app
// layout (see Group for app layouts). Each page load gets a shallow copy
// of page, so page should be a pointer to a struct, and any fields set on
// it (a database handle, say) are shared by every copy.
func (a *App) Live(path string, page Page, tmpl render.Template, opts ...RouteOption) {
	a.top.Live(path, page, tmpl, opts...)
}

// ServeHTTP serves the app's pages and the /_loom/ endpoints, gzipped
// when worth it (see compress.go).
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cw := compress(w, r)
	a.serve(cw, r)
	cw.close() // not deferred: a panicking handler's response is dropped, as net/http would
}

func (a *App) serve(w http.ResponseWriter, r *http.Request) {
	a.reaper.Do(func() { go a.reap() })
	a.trustForwarded(r)
	a.securityHeaders(w, r)
	if r.URL.Path == HealthPath && r.Method == http.MethodGet {
		a.serveHealth(w, r)
		return
	}
	if a.cfg.Jobs == JobsOnly && !strings.HasPrefix(r.URL.Path, "/_loom/dev/") {
		http.Error(w, "this node runs background jobs only (loom.Config.Jobs is jobs-only); pages are served by the other nodes", http.StatusNotFound)
		return
	}
	a.checkHTTPS(r)
	if err := a.startJobs(); err != nil {
		a.serveError(w, r, nil, http.StatusInternalServerError, err) // startJobs logged it
		return
	}
	if crossOrigin(r) {
		a.refuseCrossOrigin(w, r) // see origin.go
		return
	}
	if r.Method == http.MethodGet && r.Header.Get(navigateHeader) != "" {
		if s := a.patchable(r); s != nil {
			a.servePatch(w, r, s) // the URL changes within the page; see patch.go
			return
		}
	}
	if r.Method == http.MethodGet && r.Header.Get(navigateHeader) != "" && !a.navigable(r) {
		serveNavigateReload(w) // before any handler runs; see navigate.go
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_loom/dev/") || r.URL.Path == "/_loom/preview" || strings.HasPrefix(r.URL.Path, "/_loom/preview/") {
		a.serveDev(w, r)
		return
	}
	if _, pattern := a.mux.Handler(r); pattern == "" && r.Method == http.MethodGet {
		// No route: the mux answers 404, or 405 for a path routed for
		// other methods. Its 404 becomes the error page.
		nf := &notFoundWriter{ResponseWriter: w}
		a.mux.ServeHTTP(nf, r)
		if nf.notFound {
			a.serveError(w, r, nil, http.StatusNotFound, nil)
		}
		return
	}
	a.mux.ServeHTTP(w, r)
}

// notFoundWriter swallows the mux's own 404 answer, so the error page can
// replace it, and passes any other answer through.
type notFoundWriter struct {
	http.ResponseWriter
	notFound bool
}

func (w *notFoundWriter) WriteHeader(status int) {
	if status == http.StatusNotFound {
		w.notFound = true
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *notFoundWriter) Write(b []byte) (int, error) {
	if w.notFound {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

// Run serves the app on addr, e.g. ":4000", and runs its background jobs.
// Under `loom dev`, it serves on the address loom dev gives in LOOM_ADDR
// instead, behind the dev server. On SIGINT or SIGTERM it shuts down
// gracefully, losing no user's work: /_loom/health answers 503 at once,
// and it keeps serving for Config.DrainDelay while load balancers notice;
// then it stops accepting events (browsers
// resend them to the restarted server), lets every page finish the
// events already queued and running jobs finish, up to the job runner's
// ShutdownGrace, releases unfinished jobs to their queue, then closes
// the pages' streams, and browsers remount the pages when the server is
// back.
func (a *App) Run(addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return a.run(ctx, addr, nil)
}

// run is Run until ctx ends. If ready isn't nil it receives the address
// the server listens on.
func (a *App) run(ctx context.Context, addr string, ready chan<- string) error {
	if err := a.checkEnv(os.Getenv("LOOM_ADDR")); err != nil {
		return err
	}
	if dev := os.Getenv("LOOM_ADDR"); dev != "" {
		addr = dev
	}
	if err := a.checkDev(addr); err != nil {
		return err
	}
	if err := a.checkKeep(); err != nil {
		return err
	}
	if err := a.checkDB(context.Background()); err != nil {
		return err
	}
	if err := a.checkSessionMode(context.Background()); err != nil {
		return err
	}
	if err := a.checkWatches(context.Background()); err != nil {
		return err
	}
	if err := a.checkMigrations(context.Background()); err != nil {
		return err
	}
	if err := a.checkStorage(context.Background()); err != nil {
		return err
	}
	if err := a.runStartupChecks(context.Background()); err != nil {
		return err
	}
	if err := a.startJobs(); err != nil {
		return err
	}
	a.storeClients(context.Background())
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		a.shutdownJobs()
		return err
	}
	srv := a.server()
	errc := make(chan error, 1)
	go func() {
		a.stream.Emit(ctx, trace.AppListen, "msg", "loom: listening", "addr", ln.Addr().String())
		if srv.TLSConfig != nil {
			errc <- srv.ServeTLS(ln, "", "") // certificates from TLSConfig
			return
		}
		errc <- srv.Serve(ln)
	}()
	if ready != nil {
		ready <- ln.Addr().String()
	}
	select {
	case err := <-errc:
		a.shutdownJobs()
		return err
	case <-ctx.Done():
	}
	a.stream.Emit(context.Background(), trace.AppShutdown, "msg", "loom: shutting down")
	a.unready.Store(true) // load balancers stop sending requests
	if d := a.cfg.DrainDelay; d > 0 {
		time.Sleep(d) // still serving everything meanwhile
	}
	a.drain(a.shutdownGrace())
	close(a.closing) // ends SSE streams, so the server can shut down
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx) // waits for event requests to be answered
	a.shutdownJobs()
	return nil
}

// server is the http.Server Run serves with: limits that keep a slow or
// idle client from holding a connection, then Config.Server's changes.
// WriteTimeout stays zero, since it would cut the pages' event streams.
func (a *App) server() *http.Server {
	srv := &http.Server{
		Handler:           a,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	if a.cfg.Server != nil {
		a.cfg.Server(srv)
	}
	return srv
}

// Validate runs the checks Run makes before it serves, without serving
// or starting anything: the jobs configuration (JB510, JB501), the
// tables routes watch (DB622), sub-apps' migrations (RT433), the
// storage bucket (SG002 to SG005) and the checks added with
// StartupCheck. loomtest.Mount runs it, so a test
// fails where Run would.
func (a *App) Validate(ctx context.Context) error {
	if a.jobs != nil {
		if err := a.jobs.Validate(); err != nil {
			return err
		}
	}
	if err := a.checkWatches(ctx); err != nil {
		return err
	}
	if err := a.checkMigrations(ctx); err != nil {
		return err
	}
	if err := a.checkStorage(ctx); err != nil {
		return err
	}
	return a.runStartupChecks(ctx)
}

// ReportError logs err and keeps it for `loom errors`, for a plain
// handler that answers the browser itself but must not hide what went
// wrong: auth's OAuth callback when a provider refuses its code, say. A
// *Error keeps its code and fix.
func (a *App) ReportError(err error) { a.logError("", err) }

// logError reports an error with no trace: one outside any page's or
// job's action.
func (a *App) logError(sid string, err error) { a.reportError(context.Background(), sid, err) }

// shortSID is the part of a session id the log shows: enough to tell
// sessions apart, too little to name one. The whole id is in `loom
// errors`, which only dev serves.
func shortSID(sid string) string {
	if len(sid) > 8 {
		return sid[:8]
	}
	return sid
}
