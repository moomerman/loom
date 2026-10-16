package loom

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/pubsub"
	"github.com/moomerman/loom/storage"
	"github.com/moomerman/loom/trace"
)

// Config configures an App; pass it to New. It is the one place an app
// is configured: every field has a default, so the zero Config is an
// in-memory app for development.
//
//	app := loom.New(loom.Config{
//		DB:      os.Getenv("DATABASE_URL"),
//		BaseURL: os.Getenv("BASE_URL"),
//		Title:   "Tally",
//	})
//
// New checks the values (RT441), and Run checks them against the
// environment it serves in (RT439, RT440, RT443).
type Config struct {
	// DB is the URL of the app's Postgres database, such as
	// postgres://user:password@localhost:5432/myapp. Run checks the
	// connection (DB601), and jobs are stored in it (pgjobs) unless
	// app.JobStore says otherwise. Empty means no database: app.DB() is
	// a local DB, which can't run SQL (DB602), and jobs are kept in
	// memory.
	DB string
	// DBMaxConns caps the database connections the app opens. Table
	// watch and the jobs runner each hold one for LISTEN, and the jobs
	// leader one for its lock. Defaults to 20.
	DBMaxConns int
	// StatementTimeout ends any statement that runs longer, so a hung
	// query fails instead of holding a page or a job runner forever.
	// A job with a slow query raises it for its transaction with
	// SET LOCAL statement_timeout. Defaults to 30 seconds.
	StatementTimeout time.Duration

	// Title is the document title of pages that don't set their own, in
	// the default root layout. Defaults to "Loom".
	Title string
	// BaseURL is the app's public address, such as
	// https://app.example.com: where links in mail point and OAuth
	// providers send the browser back. Outside dev, auth needs it
	// (AU902).
	BaseURL string
	// TrustedProxies are the reverse proxies whose X-Forwarded-For and
	// X-Forwarded-Proto headers the app believes: the client's address
	// and whether it used https. Loopback addresses are always trusted,
	// for a proxy on the same machine; any other sender's forwarded
	// headers are removed before a handler sees them.
	TrustedProxies []netip.Prefix
	// Secret encrypts values the app keeps but can't hash, such as
	// two-factor secrets (App.Seal). Give it at least 32 random
	// characters from the environment, the same on every node, e.g.
	// os.Getenv("LOOM_SECRET") made with `openssl rand -base64 32`.
	// Changing it makes what was sealed with the old one unreadable.
	Secret string
	// CSP is the Content-Security-Policy every response carries;
	// DefaultCSP unless set. The default allows only the app's own
	// scripts, styles and connections, so injected markup can't run
	// script. Loosen it for what the app needs, e.g. DefaultCSP +
	// "; img-src 'self' data: https://images.example.com" (the last
	// directive of a name wins over none: write the whole directive).
	CSP string
	// Logger receives the app's events (docs/events.md) at the levels
	// it enables: errors, warnings and lifecycle events at Info; the
	// per-click and per-query events at Debug. Defaults to text on
	// stderr at Info.
	Logger *slog.Logger
	// OnEvent, if set, receives every event, whatever Logger enables: the
	// bridge to an error tracker, metrics or tracing system. It runs on
	// the goroutine that emitted the event, so it must be quick and
	// must not block; hand slow work to a channel.
	OnEvent func(trace.Event)
	// SlowQuery is how long a SQL statement runs before it is reported
	// as db.slow. Defaults to 500ms.
	SlowQuery time.Duration
	// PubSub delivers messages to pages that call Ctx.Subscribe, and from
	// jobs' Broadcast. By default messages reach this node's pages at once
	// and, with a Postgres database, every other node's by NOTIFY
	// (docs/deploy.md); without one they stay in-process.
	PubSub pubsub.PubSub
	// Storage is the bucket the app keeps files in (docs/storage.md):
	// storage.S3(...) for an S3-compatible service such as AWS S3,
	// Cloudflare R2 or MinIO; storage.Dir(path) for a directory on one
	// server; storage.Memory() in tests. Run checks it can be used
	// (SG002 to SG005). Nil means none: app.Storage() keeps files in
	// .loom/storage under `loom dev`, and outside dev fails with SG001.
	Storage storage.Bucket

	// SessionTimeout is how long a page's session lives without a
	// connected browser before it is stopped. Defaults to two minutes.
	SessionTimeout time.Duration
	// SignInFor is how long a sign-in lasts (users.go): the session
	// cookie's lifetime. Defaults to 30 days.
	SignInFor time.Duration
	// MaxSessions caps the live pages open at once. A page load beyond
	// it answers 503 (RT444), so a crawler can't exhaust memory.
	// Defaults to 10,000.
	MaxSessions int

	// ShutdownGrace is how long shutdown waits for pages' queued events
	// and running jobs. Defaults to jobs.DefaultShutdownGrace.
	ShutdownGrace time.Duration
	// DrainDelay is how long shutdown keeps serving after /_loom/health
	// starts answering 503, so a load balancer stops sending requests
	// before the server stops taking them. Zero (the default) suits one
	// node with no balancer.
	DrainDelay time.Duration

	// Dev turns dev mode on or off: the /_loom/dev/ endpoints the loom
	// CLI uses, the preview gallery, DevOnly groups and detailed error
	// pages. DevFromEnv (the default) means on when LOOM_DEV is "1", as
	// under `loom dev`, which also sets LOOM_DEV_TOKEN: then dev
	// requests must carry that token, which loom dev's proxy adds
	// (dev.go). DevOn is for tests and tools; never use it in
	// production.
	Dev DevMode
	// Jobs is what this node does with background jobs, so web and
	// worker nodes can be deployed apart: JobsRun (the default) serves
	// pages and runs jobs and cron; JobsEnqueueOnly serves pages and
	// only enqueues, for web nodes when other nodes run the jobs;
	// JobsOnly runs jobs and cron and answers only /_loom/health, for
	// worker nodes. It is a string, so a deploy can set it from the
	// environment: loom.JobsMode(os.Getenv("LOOM_JOBS")).
	Jobs JobsMode
	// Server, if set, adjusts the http.Server Run serves with: TLS, or
	// other limits. Run sets ReadHeaderTimeout (10s), IdleTimeout (120s)
	// and MaxHeaderBytes (1 MB) first. Leave WriteTimeout at zero: it
	// would cut the pages' event streams.
	Server func(*http.Server)
}

// DevMode is Config.Dev.
type DevMode int

const (
	DevFromEnv DevMode = iota // on when LOOM_DEV=1, as under loom dev
	DevOn                     // on, with no token: tests and tools
	DevOff                    // off, whatever the environment says
)

// JobsMode is Config.Jobs.
type JobsMode string

const (
	JobsRun         JobsMode = "run"          // pages, jobs and cron; "" means this too
	JobsEnqueueOnly JobsMode = "enqueue-only" // pages; jobs are enqueued for other nodes to run
	JobsOnly        JobsMode = "jobs-only"    // jobs and cron, and /_loom/health
)

// Defaults for Config's zero values.
const (
	DefaultDBMaxConns       = 20
	DefaultStatementTimeout = 30 * time.Second
	DefaultSessionTimeout   = 2 * time.Minute
	DefaultSignInFor        = 30 * 24 * time.Hour
	DefaultMaxSessions      = 10_000
	DefaultSlowQuery        = 500 * time.Millisecond
)

// withDefaults checks cfg (RT441) and fills in its defaults.
func (cfg Config) withDefaults() (Config, error) {
	invalid := func(field, why, fix string) error {
		return &Error{Code: RT441, Msg: fmt.Sprintf("loom.Config.%s %s", field, why), Where: callerOutsideLoom(), Fix: fix}
	}
	for _, d := range []struct {
		name string
		v    time.Duration
	}{{"StatementTimeout", cfg.StatementTimeout}, {"SessionTimeout", cfg.SessionTimeout}, {"SignInFor", cfg.SignInFor}, {"ShutdownGrace", cfg.ShutdownGrace}, {"DrainDelay", cfg.DrainDelay}, {"SlowQuery", cfg.SlowQuery}} {
		if d.v < 0 {
			return cfg, invalid(d.name, fmt.Sprintf("is negative (%v)", d.v), "leave it zero for the default, or give a positive duration")
		}
	}
	if cfg.Dev < DevFromEnv || cfg.Dev > DevOff {
		return cfg, invalid("Dev", fmt.Sprintf("is %d, not a DevMode", cfg.Dev), "use loom.DevFromEnv, loom.DevOn or loom.DevOff")
	}
	switch cfg.Jobs {
	case "":
		cfg.Jobs = JobsRun
	case JobsRun, JobsEnqueueOnly, JobsOnly:
	default:
		return cfg, invalid("Jobs", fmt.Sprintf("is %q, not a JobsMode", cfg.Jobs), `use "run" (the default), "enqueue-only" or "jobs-only"`)
	}
	if cfg.DBMaxConns < 0 || cfg.MaxSessions < 0 {
		return cfg, invalid("DBMaxConns or MaxSessions", "is negative", "leave it zero for the default, or give a positive number")
	}
	if cfg.BaseURL != "" {
		u, err := url.Parse(cfg.BaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/") {
			return cfg, invalid("BaseURL", fmt.Sprintf("%q isn't an http(s) address with no path", cfg.BaseURL), `give the scheme and host, e.g. "https://app.example.com"`)
		}
	}
	for _, p := range cfg.TrustedProxies {
		if !p.IsValid() {
			return cfg, invalid("TrustedProxies", "has an invalid prefix", `parse each with netip.MustParsePrefix("10.0.0.0/8")`)
		}
	}
	if cfg.Secret != "" && len(cfg.Secret) < MinSecretLen {
		return cfg, invalid("Secret", fmt.Sprintf("is %d characters, fewer than %d", len(cfg.Secret), MinSecretLen), "make one with `openssl rand -base64 32` and pass it from the environment, e.g. os.Getenv(\"LOOM_SECRET\")")
	}
	if strings.ContainsAny(cfg.CSP, "\r\n") {
		return cfg, invalid("CSP", "has a line break", "write the policy on one line, directives separated by semicolons")
	}
	if cfg.CSP == "" {
		cfg.CSP = DefaultCSP
	}
	if cfg.DBMaxConns == 0 {
		cfg.DBMaxConns = DefaultDBMaxConns
	}
	if cfg.StatementTimeout == 0 {
		cfg.StatementTimeout = DefaultStatementTimeout
	}
	if cfg.Title == "" {
		cfg.Title = "Loom"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if cfg.SessionTimeout == 0 {
		cfg.SessionTimeout = DefaultSessionTimeout
	}
	if cfg.SignInFor == 0 {
		cfg.SignInFor = DefaultSignInFor
	}
	if cfg.MaxSessions == 0 {
		cfg.MaxSessions = DefaultMaxSessions
	}
	if cfg.SlowQuery == 0 {
		cfg.SlowQuery = DefaultSlowQuery
	}
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = jobs.DefaultShutdownGrace
	}
	return cfg, nil
}

// Logger is where the app logs (Config.Logger), for sub-apps and plain
// handlers that log alongside it.
func (a *App) Logger() *slog.Logger { return a.cfg.Logger }

// PubSub is the app's PubSub (Config.PubSub): publish on it to reach
// pages subscribed with Ctx.Subscribe.
func (a *App) PubSub() pubsub.PubSub { return a.cfg.PubSub }

// Dev reports whether the app runs in dev mode (Config.Dev).
func (a *App) Dev() bool { return a.dev }

// Title is the app's default document title (Config.Title).
func (a *App) Title() string { return a.cfg.Title }

// BaseURL is the app's public address (Config.BaseURL), without a
// trailing slash; "" if not set.
func (a *App) BaseURL() string {
	if n := len(a.cfg.BaseURL); n > 0 && a.cfg.BaseURL[n-1] == '/' {
		return a.cfg.BaseURL[:n-1]
	}
	return a.cfg.BaseURL
}

// URL makes a path ("/c64/monty") an absolute URL, on BaseURL or, without
// one, the origin r came to, as link previews, sitemaps and mail need.
// A URL with a scheme, or "", is returned as it is. Pages use c.URL,
// which also resolves the path against the page's mount.
func (a *App) URL(r *http.Request, p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return p
	}
	base := a.BaseURL()
	if base == "" {
		scheme := "http"
		if secureRequest(r) { // here or at a trusted proxy (Config.TrustedProxies)
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + p
}

// checkEnv refuses LOOM_ADDR (given as loomAddr) outside dev mode
// (RT443): only loom dev sets it, and it replaces the address the app
// gives Run, so in production it would move the app silently.
func (a *App) checkEnv(loomAddr string) error {
	if loomAddr != "" && !a.dev {
		return &Error{Code: RT443, Msg: fmt.Sprintf("LOOM_ADDR is set (%q) but the app isn't in dev mode, so it would replace the address given to Run", loomAddr),
			Fix: "unset LOOM_ADDR; only `loom dev` sets it, together with LOOM_DEV=1"}
	}
	return nil
}

// trustForwarded applies Config.TrustedProxies to a request as it
// arrives. From a trusted proxy, the client's address in X-Forwarded-For
// becomes r.RemoteAddr; from anyone else, the forwarded headers are
// removed, so a client can't claim https (secure cookies) or a loopback
// address (dev endpoints) by sending them.
func (a *App) trustForwarded(r *http.Request) {
	if !a.trusted(r.RemoteAddr) {
		for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "Forwarded"} {
			r.Header.Del(h)
		}
		return
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, ip := range strings.Split(v, ",") {
			if ip = strings.TrimSpace(ip); ip != "" {
				hops = append(hops, ip)
			}
		}
	}
	// The nearest untrusted hop is the client: proxies append, so
	// anything left of it may be the client's own invention.
	for i := len(hops) - 1; i >= 0; i-- {
		if !a.trusted(hops[i]) {
			r.RemoteAddr = net.JoinHostPort(hops[i], "0")
			return
		}
	}
}

// trusted reports whether addr (an IP, with or without a port) is a
// trusted proxy: loopback, or in Config.TrustedProxies.
func (a *App) trusted(addr string) bool {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return true
	}
	for _, p := range a.cfg.TrustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// checkHTTPS reports RT442, once, when the app's BaseURL is https but a
// request arrives that isn't, as far as the app can tell: no TLS, and no
// X-Forwarded-Proto from a trusted proxy. Cookies are then set without
// Secure, so the usual cause is a proxy missing from TrustedProxies or
// not sending X-Forwarded-Proto. The request is served regardless.
func (a *App) checkHTTPS(r *http.Request) {
	if a.dev || !strings.HasPrefix(a.cfg.BaseURL, "https:") || secureRequest(r) || a.plainSeen.Load() {
		return
	}
	if a.plainSeen.CompareAndSwap(false, true) {
		a.logError("", &Error{Code: RT442, Msg: fmt.Sprintf("BaseURL is %s, but a request for %s came over plain http from %s, with no X-Forwarded-Proto from a trusted proxy", a.cfg.BaseURL, r.URL.Path, r.RemoteAddr),
			Fix: "add the reverse proxy's address to loom.Config.TrustedProxies and have it send X-Forwarded-Proto, or serve TLS with Config.Server; until then cookies are set without Secure"})
	}
}
