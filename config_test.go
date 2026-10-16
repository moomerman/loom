package loom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/jobs"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestConfigDefaults(t *testing.T) {
	a := New()
	c := a.cfg
	if c.Title != "Loom" || c.SessionTimeout != DefaultSessionTimeout || c.MaxSessions != DefaultMaxSessions ||
		c.StatementTimeout != DefaultStatementTimeout || c.DBMaxConns != DefaultDBMaxConns || c.Logger == nil || c.PubSub == nil {
		t.Errorf("defaults: %+v", c)
	}
	if New(Config{BaseURL: "https://app.test/"}).BaseURL() != "https://app.test" {
		t.Error("BaseURL keeps its trailing slash")
	}
}

func TestConfigInvalid(t *testing.T) {
	for name, cfg := range map[string]Config{
		"negative timeout": {SessionTimeout: -time.Second},
		"negative limit":   {MaxSessions: -1},
		"base URL no host": {BaseURL: "app.example.com"},
		"base URL path":    {BaseURL: "https://app.example.com/app"},
		"base URL scheme":  {BaseURL: "ftp://app.example.com"},
		"bad proxy":        {TrustedProxies: []netip.Prefix{{}}},
		"bad dev mode":     {Dev: 7},
		"bad jobs mode":    {Jobs: "worker"},
	} {
		func() {
			defer func() {
				if err, _ := recover().(error); code(err) != RT441 {
					t.Errorf("%s: %v, want RT441", name, err)
				}
			}()
			New(cfg)
		}()
	}
}

func TestLoomAddrOutsideDev(t *testing.T) {
	if err := New(Config{Dev: DevOff}).checkEnv("127.0.0.1:5000"); code(err) != RT443 {
		t.Errorf("LOOM_ADDR outside dev: %v, want RT443", err)
	}
	if err := New(Config{Dev: DevOn}).checkEnv("127.0.0.1:5000"); err != nil {
		t.Errorf("LOOM_ADDR in dev: %v", err)
	}
	if err := New(Config{Dev: DevOff}).checkEnv(""); err != nil {
		t.Errorf("no LOOM_ADDR: %v", err)
	}
}

func TestServerLimits(t *testing.T) {
	a := New(Config{Server: func(s *http.Server) { s.ReadTimeout = time.Minute }})
	s := a.server()
	if s.ReadHeaderTimeout != 10*time.Second || s.IdleTimeout != 120*time.Second || s.MaxHeaderBytes != 1<<20 || s.WriteTimeout != 0 || s.ReadTimeout != time.Minute {
		t.Errorf("server: header %v, idle %v, max %d, write %v, read %v", s.ReadHeaderTimeout, s.IdleTimeout, s.MaxHeaderBytes, s.WriteTimeout, s.ReadTimeout)
	}
}

// Forwarded headers count only from a trusted proxy: loopback, or one in
// TrustedProxies. Anyone else's are removed before a handler sees them.
func TestTrustedProxies(t *testing.T) {
	a := New(Config{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}})
	for _, tc := range []struct {
		name, remote, forwardedFor, proto string
		wantRemote                        string
		wantSecure                        bool
	}{
		{"a client claiming https", "203.0.113.9:5000", "", "https", "203.0.113.9:5000", false},
		{"a client claiming to be local", "203.0.113.9:5000", "127.0.0.1", "", "203.0.113.9:5000", false},
		{"a trusted proxy", "10.1.2.3:5000", "198.51.100.7", "https", "198.51.100.7:0", true},
		{"a proxy on this machine", "127.0.0.1:5000", "198.51.100.7", "https", "198.51.100.7:0", true},
		{"a client's invention before the proxy's", "10.1.2.3:5000", "127.0.0.1, 198.51.100.7", "", "198.51.100.7:0", false},
		{"two trusted proxies", "127.0.0.1:5000", "198.51.100.7, 10.9.9.9", "", "198.51.100.7:0", false},
		{"a local request", "127.0.0.1:5000", "", "", "127.0.0.1:5000", false},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		if tc.forwardedFor != "" {
			r.Header.Set("X-Forwarded-For", tc.forwardedFor)
		}
		if tc.proto != "" {
			r.Header.Set("X-Forwarded-Proto", tc.proto)
		}
		a.trustForwarded(r)
		if r.RemoteAddr != tc.wantRemote || secureRequest(r) != tc.wantSecure {
			t.Errorf("%s: remote %s, secure %v; want %s, %v", tc.name, r.RemoteAddr, secureRequest(r), tc.wantRemote, tc.wantSecure)
		}
		if tc.wantRemote == tc.remote && tc.remote != "127.0.0.1:5000" && (r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Forwarded-Proto") != "") {
			t.Errorf("%s: forwarded headers kept: %v", tc.name, r.Header)
		}
	}
}

func TestPlainHTTPWithHTTPSBaseURL(t *testing.T) {
	var logs bytes.Buffer
	app := New(Config{BaseURL: "https://app.test", Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	app.Live("/", &counterPage{}, hxtest.Counter)
	srv := httptest.NewServer(app)
	defer srv.Close()
	fetch(t, srv.URL+"/")
	fetch(t, srv.URL+"/")
	if n := strings.Count(logs.String(), "RT442"); n != 1 {
		t.Errorf("RT442 logged %d times, want once:\n%s", n, logs.String())
	}
	logs.Reset()
	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.Header.Set("X-Forwarded-Proto", "https") // the test server is on loopback, so trusted
	app2 := New(Config{BaseURL: "https://app.test", Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	app2.Live("/", &counterPage{}, hxtest.Counter)
	srv2 := httptest.NewServer(app2)
	defer srv2.Close()
	req.URL.Host = strings.TrimPrefix(srv2.URL, "http://")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if strings.Contains(logs.String(), "RT442") {
		t.Errorf("a forwarded https request logged RT442:\n%s", logs.String())
	}
}

// At MaxSessions open pages, a page load is 503 with Retry-After
// (RT444), and the error page shows the code.
func TestMaxSessions(t *testing.T) {
	app := New(Config{Logger: quiet(), MaxSessions: 2})
	app.Live("/", &counterPage{}, hxtest.Counter)
	srv := httptest.NewServer(app)
	defer srv.Close()
	load(t, srv)
	load(t, srv)
	body, res := fetch(t, srv.URL+"/")
	if res.StatusCode != http.StatusServiceUnavailable || res.Header.Get("Retry-After") == "" || !strings.Contains(body, "RT444") {
		t.Errorf("third page = %d, Retry-After %q:\n%s", res.StatusCode, res.Header.Get("Retry-After"), body)
	}
	if n := len(app.Sessions()); n != 2 {
		t.Errorf("%d sessions, want 2", n)
	}
}

// A session keeps two streams at most: a third closes the oldest.
func TestStreamCap(t *testing.T) {
	app, srv := newTestApp(t)
	sid, _ := load(t, srv)
	s := app.session(sid)
	first, second := s.subscribe(1), s.subscribe(1)
	third := s.subscribe(1)
	if _, ok := <-first; ok {
		t.Error("the oldest stream is still open")
	}
	s.mu.Lock()
	n := len(s.subs)
	_, kept2 := s.subs[second]
	_, kept3 := s.subs[third]
	s.mu.Unlock()
	if n != 2 || !kept2 || !kept3 {
		t.Errorf("%d streams; second kept %v, third kept %v", n, kept2, kept3)
	}
}

// Clients idle for clientMemory with nothing running are forgotten.
func TestForgetClients(t *testing.T) {
	a := New()
	ctx := context.Background()
	for _, id := range []string{"old", "running", "new"} {
		if ok, err := a.claim(ctx, id, 1); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	a.release(ctx, "old", 1, true)
	a.release(ctx, "new", 1, true)
	a.clients.m["old"].seen = time.Now().Add(-2 * clientMemory)
	a.clients.m["running"].seen = time.Now().Add(-2 * clientMemory)
	a.forgetClients(time.Now().Add(-clientMemory))
	if _, ok := a.clients.m["old"]; ok {
		t.Error("an idle client is kept")
	}
	if _, ok := a.clients.m["running"]; !ok {
		t.Error("a client with an event running was forgotten")
	}
	if _, ok := a.clients.m["new"]; !ok {
		t.Error("a recent client was forgotten")
	}
}

type slowPage struct{ counterPage }

func (p *slowPage) Events() Events {
	e := p.counterPage.Events()
	e["slow"] = func(c *Ctx, _ Payload) { time.Sleep(150 * time.Millisecond) }
	return e
}

func (p *slowPage) Assigns(c *Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "slow", Events: bound(p.Events())}
}

// A handler running past slowHandler is RT445, and loom sessions shows
// the page as busy meanwhile.
func TestSlowHandler(t *testing.T) {
	defer func(d time.Duration) { slowHandler = d }(slowHandler)
	slowHandler = 50 * time.Millisecond
	app := New(Config{Logger: quiet()})
	app.Live("/", &slowPage{}, hxtest.Counter)
	srv := httptest.NewServer(app)
	defer srv.Close()
	sid, _ := load(t, srv)
	done := make(chan int)
	go func() { done <- post(t, srv, `{"sid":"`+sid+`","event":"slow"}`) }()
	time.Sleep(100 * time.Millisecond)
	if s := app.Sessions(); len(s) != 1 || s[0].BusySeconds < 0.05 {
		t.Errorf("sessions while busy: %+v", s)
	}
	if code := <-done; code != 200 {
		t.Errorf("slow event = %d", code)
	}
	if s := app.Sessions(); s[0].BusySeconds != 0 {
		t.Errorf("busy after the handler returned: %+v", s)
	}
	errs := app.errors.all()
	if len(errs) != 1 || errs[0].Code != RT445 || !strings.Contains(errs[0].Message, `event "slow"`) {
		t.Errorf("errors = %+v", errs)
	}
}

func TestHealth(t *testing.T) {
	app, srv := newTestApp(t)
	load(t, srv)
	get := func() (int, Health) {
		res, err := http.Get(srv.URL + HealthPath)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var h Health
		if err := json.NewDecoder(res.Body).Decode(&h); err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, h
	}
	if code, h := get(); code != 200 || h.Status != "ok" || h.DB != "none" || h.Jobs != "none" || h.Node != nil || h.Watch != "none" || h.PubSub != "none" || h.Sessions != 1 {
		t.Errorf("health = %d %+v", code, h)
	}
	app.unready.Store(true) // shutdown began
	if code, h := get(); code != 503 || h.Status != "draining" {
		t.Errorf("health while draining = %d %+v", code, h)
	}
}

// Config.Jobs splits web and worker nodes: an enqueue-only node checks
// its job types but runs none; a jobs-only node answers only health.
func TestJobsModes(t *testing.T) {
	web := New(Config{Logger: quiet(), Jobs: JobsEnqueueOnly})
	web.Jobs(notifyJob{})
	web.Live("/", &counterPage{}, hxtest.Counter)
	srv := httptest.NewServer(web)
	defer srv.Close()
	load(t, srv)
	var res jobs.Result
	web.DB().Tx(context.Background(), func(tx Tx) error {
		var err error
		res, err = jobs.Enqueue(tx, notifyJob{})
		return err
	})
	time.Sleep(100 * time.Millisecond)
	if rec, err := web.JobRunner().Store().Get(context.Background(), res.ID); err != nil || rec.State != jobs.Available || web.jobs.IsLeader() {
		t.Errorf("an enqueue-only node ran a job: %+v %v", rec, err)
	}
	if body, res := fetch(t, srv.URL+HealthPath); res.StatusCode != 200 || !strings.Contains(body, `"jobs":"enqueue-only"`) || !strings.Contains(body, `"node":null`) {
		t.Errorf("enqueue-only health = %d %s", res.StatusCode, body)
	}
	if m := web.Manifest(); m.Jobs.Mode != "enqueue-only" || !strings.Contains(m.Text(), "this node enqueues only") {
		t.Errorf("manifest jobs mode: %q", m.Jobs.Mode)
	}

	worker := New(Config{Logger: quiet(), Jobs: JobsOnly})
	worker.Jobs(notifyJob{})
	worker.Live("/", &counterPage{}, hxtest.Counter)
	wsrv := httptest.NewServer(worker)
	defer wsrv.Close()
	defer worker.shutdownJobs()
	if body, res := fetch(t, wsrv.URL+"/"); res.StatusCode != 404 || !strings.Contains(body, "jobs-only") {
		t.Errorf("jobs-only page = %d %s", res.StatusCode, body)
	}
	body, hres := fetch(t, wsrv.URL+HealthPath)
	var h Health
	if json.Unmarshal([]byte(body), &h); hres.StatusCode != 200 || h.Jobs != "running" || h.Node == nil || h.Node.Name != worker.jobs.Node ||
		len(h.Node.Queues) != 1 || h.Node.Queues[0] != (jobs.NodeQueue{Name: "default", Concurrency: jobs.DefaultConcurrency}) {
		t.Errorf("jobs-only health = %d %s", hres.StatusCode, body)
	}
}

func TestHealthWithDB(t *testing.T) {
	db := testDB(t)
	app := New(Config{Logger: quiet()})
	app.UseDB(db)
	srv := httptest.NewServer(app)
	defer srv.Close()
	body, res := fetch(t, srv.URL+HealthPath)
	// The table watch's and PubSub's LISTEN open when a page needs them:
	// idle, not a fault.
	if res.StatusCode != 200 || !strings.Contains(body, `"db":"ok"`) || !strings.Contains(body, `"watch":"idle"`) || !strings.Contains(body, `"pubsub":"idle"`) {
		t.Errorf("health = %d %s", res.StatusCode, body)
	}
}

// Postgres reached directly keeps LISTEN, so the pooler check passes.
func TestSessionModeCheck(t *testing.T) {
	db := testDB(t)
	app := New()
	app.UseDB(db)
	if err := app.checkSessionMode(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type failingPage struct{}

func (p *failingPage) Mount(c *Ctx) {
	panic("SELECT secret FROM internals failed at /srv/app/store.go:42")
}

func (p *failingPage) Assigns(c *Ctx) hxtest.BadgeAssigns { return hxtest.BadgeAssigns{} }

// Outside dev, a failed page load shows the error page, never the
// error; in dev, the error itself.
func TestErrorPage(t *testing.T) {
	app := New(Config{Logger: quiet(), Dev: DevOff})
	app.Live("/fail", &failingPage{}, hxtest.Badge)
	srv := httptest.NewServer(app)
	defer srv.Close()
	body, res := fetch(t, srv.URL+"/fail")
	if res.StatusCode != 500 || strings.Contains(body, "secret") || !strings.Contains(body, "Something went wrong") {
		t.Errorf("500 outside dev = %d:\n%s", res.StatusCode, body)
	}
	if errs := app.errors.all(); len(errs) != 1 || !strings.Contains(errs[0].Message, "secret") {
		t.Errorf("the error isn't kept for loom errors: %+v", errs)
	}
	body, res = fetch(t, srv.URL+"/nowhere")
	if res.StatusCode != 404 || !strings.Contains(body, "This page doesn&#39;t exist.") {
		t.Errorf("404 = %d:\n%s", res.StatusCode, body)
	}

	app.ErrorPage(hxtest.Badge, func(e PageError) hxtest.BadgeAssigns { return hxtest.BadgeAssigns{Label: e.Title + " " + e.Code} })
	body, res = fetch(t, srv.URL+"/nowhere")
	if res.StatusCode != 404 || !strings.Contains(body, `<span class="badge">This page doesn&#39;t exist. </span>`) || !strings.Contains(body, "<html") {
		t.Errorf("custom 404 = %d:\n%s", res.StatusCode, body)
	}

	app.dev = true
	if body, res := fetch(t, srv.URL+"/fail"); res.StatusCode != 500 || !strings.Contains(body, "secret") {
		t.Errorf("500 in dev = %d:\n%s", res.StatusCode, body)
	}
}

func TestErrorPageChecked(t *testing.T) {
	for name, f := range map[string]func(a *App){
		"nil component": func(a *App) { a.ErrorPage(nil, nil) },
		"a document": func(a *App) {
			a.ErrorPage(hxtest.Root, func(PageError) hxtest.RootAssigns { return hxtest.RootAssigns{} })
		},
		"wrong func": func(a *App) {
			a.ErrorPage(hxtest.Badge, func(c *Ctx) hxtest.BadgeAssigns { return hxtest.BadgeAssigns{} })
		},
		"wrong assigns": func(a *App) {
			a.ErrorPage(hxtest.Badge, func(PageError) hxtest.CounterAssigns { return hxtest.CounterAssigns{} })
		},
	} {
		func() {
			defer func() {
				if err, _ := recover().(error); code(err) != RT446 {
					t.Errorf("%s: %v, want RT446", name, err)
				}
			}()
			f(New())
		}()
	}
}

// Every response carries the security headers; HSTS only when the app's
// address is https, and no policy on the dev endpoints.
func TestSecurityHeaders(t *testing.T) {
	_, srv := newTestApp(t)
	_, res := fetch(t, srv.URL+"/")
	h := res.Header
	if h.Get("Content-Security-Policy") != DefaultCSP || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "strict-origin-when-cross-origin" || h.Get("Strict-Transport-Security") != "" {
		t.Errorf("headers: %v", h)
	}
	csp := DefaultCSP + "; img-src 'self' https://img.example.com"
	app := New(Config{Logger: quiet(), Dev: DevOff, BaseURL: "https://app.example.com", TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, CSP: csp})
	app.Live("/", &counterPage{}, hxtest.Counter)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	app.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Security-Policy") != csp || rec.Header().Get("Strict-Transport-Security") != hstsHeader {
		t.Errorf("https app headers: %v", rec.Header())
	}
	if m := app.Manifest(); !m.Security.HSTS || !strings.Contains(m.Text(), "Content-Security-Policy: "+csp) {
		t.Errorf("manifest security: %+v", m.Security)
	}
	dev := New(Config{Logger: quiet(), Dev: DevOn})
	rec = httptest.NewRecorder()
	dev.ServeHTTP(rec, httptest.NewRequest("GET", "/_loom/preview", nil))
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Error("the preview gallery has a policy")
	}
	defer func() {
		if err, _ := recover().(error); code(err) != RT441 {
			t.Errorf("CSP with a line break: %v", err)
		}
	}()
	New(Config{CSP: "default-src 'self'\r\nSet-Cookie: x=1"})
}

// Seal and Open round-trip with the app's secret, bound to a purpose.
func TestSeal(t *testing.T) {
	app := New(Config{Secret: strings.Repeat("s", 32)})
	sealed, err := app.Seal("auth.totp", []byte("JBSWY3DPEHPK3PXP"))
	if err != nil || !IsSealed(sealed) || strings.Contains(sealed, "JBSWY3DP") {
		t.Fatalf("sealed: %q %v", sealed, err)
	}
	if b, err := app.Open("auth.totp", sealed); err != nil || string(b) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("opened: %q %v", b, err)
	}
	if _, err := app.Open("other", sealed); !errors.Is(err, ErrSealed) {
		t.Errorf("another purpose: %v", err)
	}
	if _, err := New(Config{Secret: strings.Repeat("t", 32)}).Open("auth.totp", sealed); !errors.Is(err, ErrSealed) {
		t.Errorf("another secret: %v", err)
	}
	if _, err := New().Seal("auth.totp", nil); !errors.Is(err, ErrNoSecret) {
		t.Errorf("no secret: %v", err)
	}
	defer func() {
		if err, _ := recover().(error); code(err) != RT441 {
			t.Errorf("short secret: %v", err)
		}
	}()
	New(Config{Secret: "short"})
}
