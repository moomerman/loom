package loom

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/moomerman/loom/internal/hxtest"
)

// keptChrome is a live layout that keeps its count across a remount.
type keptChrome struct {
	kept     chromeKept
	toast    string // not kept
	mounts   int
	mountSaw int // the count Mount saw
}

type chromeKept struct {
	Count int    `json:"count"`
	Name  string `json:"name,omitempty"`
}

func (l *keptChrome) Keep() any { return &l.kept }

func (l *keptChrome) Mount(c *Ctx) {
	l.mounts++
	l.mountSaw = l.kept.Count
	l.toast = "hello"
}

func (l *keptChrome) Assigns(c *Ctx) hxtest.ChromeAssigns {
	return hxtest.ChromeAssigns{Count: l.kept.Count, Toast: l.toast}
}

func (l *keptChrome) Events() Events {
	return Events{
		"bump":    func(c *Ctx, _ Payload) { l.kept.Count++ },
		"dismiss": func(c *Ctx, _ Payload) { l.toast = "" },
	}
}

// keptPage keeps its own state too.
type keptPage struct {
	kept struct{ Saves int }
}

func (p *keptPage) Keep() any { return &p.kept }

func (p *keptPage) Mount(c *Ctx) {}

func (p *keptPage) Assigns(c *Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: "saves " + strconv.Itoa(p.kept.Saves), Events: bound(p.Events())}
}

func (p *keptPage) Events() Events {
	return Events{
		"save": func(c *Ctx, _ Payload) { p.kept.Saves++ },
		"boom": func(c *Ctx, _ Payload) { panic("boom") },
	}
}

func newKeepApp(t *testing.T, cfg Config) (*App, *httptest.Server) {
	t.Helper()
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	app := New(cfg)
	app.Group(LiveLayout(hxtest.Chrome, &keptChrome{})).Live("/kept", &keptPage{}, hxtest.Counter)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return app, srv
}

func TestKeepAcrossRemount(t *testing.T) {
	app, _ := newKeepApp(t, Config{})
	v := mountView(t, app, "/kept", true)
	for _, ev := range []string{"bump", "bump", "save", "dismiss"} {
		if _, err := v.Event(ev, nil); err != nil {
			t.Fatal(err)
		}
	}
	if v.Kept() == "" {
		t.Fatal("nothing kept")
	}
	r, err := app.RemountView(v, httptest.NewRequest("GET", "/kept", nil), "test", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.HTML(); !strings.Contains(got, `<button class="bump" on:click="bump">2</button>`) || !strings.Contains(got, "saves 1") {
		t.Errorf("remounted: %s", got)
	}
	l := LayoutOf[*keptChrome](r.ctx)
	if l.mounts != 1 || l.mountSaw != 2 {
		t.Errorf("the layout mounted %d times, seeing count %d; want once, after the restore, seeing 2", l.mounts, l.mountSaw)
	}
	if l.toast != "hello" {
		t.Errorf("toast %q: what isn't kept starts over, from Mount", l.toast)
	}
	if got := r.Restored(); got != "restored the layout's; restored the page's" {
		t.Errorf("Restored = %q", got)
	}
	if _, err := v.Event("bump", nil); err != nil || v.HTML() == "" {
		t.Errorf("the old view: %v", err)
	}
	if LayoutOf[*keptChrome](v.ctx).kept.Count != 2 {
		t.Error("the old view ran an event after the remount")
	}
}

func TestKeepResealsOnlyOnChange(t *testing.T) {
	app, _ := newKeepApp(t, Config{})
	v := mountView(t, app, "/kept", true)
	k := v.Kept()
	v.Event("dismiss", nil) // changes nothing kept
	if v.Kept() != k {
		t.Error("resealed state that didn't change")
	}
	v.Event("bump", nil)
	if v.Kept() == k {
		t.Error("kept state didn't change with the count")
	}
}

func TestKeepNothingWithoutKeeper(t *testing.T) {
	app := newLiveLayoutApp(t)
	v := mountView(t, app, "/toast", true)
	if v.Kept() != "" {
		t.Errorf("Kept = %q for a page that keeps nothing", v.Kept())
	}
	doc, _ := v.Document()
	if !strings.Contains(doc, `"kept":""`) {
		t.Errorf("the document should carry no kept state, to replace the last page's:\n%s", doc)
	}
}

func TestKeepDropped(t *testing.T) {
	app, _ := newKeepApp(t, Config{Secret: strings.Repeat("s", 32)})
	other := New(Config{Secret: strings.Repeat("o", 32)})
	seal := func(a *App, k kept) string {
		plain, _ := json.Marshal(k)
		s, err := a.sealKept(plain)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	count := json.RawMessage(`{"count":5}`)
	for _, tc := range []struct {
		name, sealed, want string
		count              int
	}{
		{"restored", seal(app, kept{Layout: "*loom.keptChrome", LayoutKept: count}), "restored the layout's", 5},
		{"altered", seal(app, kept{Layout: "*loom.keptChrome", LayoutKept: count})[:40] + "AAAA", "dropped: not sealed by this app", 0},
		{"another secret", seal(other, kept{Layout: "*loom.keptChrome", LayoutKept: count}), "dropped: not sealed by this app", 0},
		{"another browser", seal(app, kept{Browser: "b2", Layout: "*loom.keptChrome", LayoutKept: count}), "dropped: another browser's", 0},
		{"another type", seal(app, kept{Layout: "*loom.chromeState", LayoutKept: count}), "dropped the layout's: it no longer fits *loom.keptChrome", 0},
		{"changed shape", seal(app, kept{Layout: "*loom.keptChrome", LayoutKept: json.RawMessage(`{"count":"five","name":"x"}`)}), "dropped the layout's: it no longer fits *loom.keptChrome", 0},
	} {
		v, err := newView(app.routesByPattern["GET /kept"], httptest.NewRequest("GET", "/kept", nil), "test", true, nil, "", tc.sealed)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		l := LayoutOf[*keptChrome](v.ctx)
		if v.Restored() != tc.want || l.kept.Count != tc.count || l.kept.Name != "" {
			t.Errorf("%s: Restored %q, kept %+v; want %q and count %d", tc.name, v.Restored(), l.kept, tc.want, tc.count)
		}
	}
}

// bigPage keeps more than a remount can send back.
type bigPage struct{ kept struct{ Notes []string } }

func (p *bigPage) Keep() any    { return &p.kept }
func (p *bigPage) Mount(c *Ctx) {}
func (p *bigPage) Assigns(c *Ctx) hxtest.CounterAssigns {
	return hxtest.CounterAssigns{Label: strconv.Itoa(len(p.kept.Notes)), Events: bound(p.Events())}
}
func (p *bigPage) Events() Events {
	return Events{"grow": func(c *Ctx, _ Payload) {
		for i := 0; i < 2000; i++ {
			p.kept.Notes = append(p.kept.Notes, strconv.Itoa(i*7919)+"-"+strconv.Itoa(len(p.kept.Notes)*104729))
		}
	}}
}

func TestKeepTooLarge(t *testing.T) {
	app := New()
	app.Live("/big", &bigPage{}, hxtest.Counter)
	v := mountView(t, app, "/big", false)
	if v.Kept() == "" {
		t.Fatal("nothing kept")
	}
	v.Event("grow", nil)
	f := v.Failures()
	if v.Kept() != "" || len(f) != 1 || f[0].(*Error).Code != RT454 || !strings.Contains(f[0].(*Error).Where, "*loom.bigPage.Keep") {
		t.Fatalf("Kept %d bytes, failures %v; want none kept and RT454", len(v.Kept()), f)
	}
	v.Event("grow", nil)
	if f := v.Failures(); len(f) != 1 {
		t.Errorf("failures on the next change: %v", f)
	}
}

// Pages whose Keep is wrong, for RT452.
type keepsValue struct{ keptPage }

func (p *keepsValue) Keep() any { return p.kept }

type keepsNil struct{ keptPage }

func (p *keepsNil) Keep() any { return (*chromeKept)(nil) }

type keepsChan struct {
	keptPage
	k struct{ C chan int }
}

func (p *keepsChan) Keep() any { return &p.k }

type keepsUnexported struct {
	keptPage
	k struct{ n int }
}

func (p *keepsUnexported) Keep() any { return &p.k }

func TestKeepChecked(t *testing.T) {
	for _, tc := range []struct {
		page Page
		msg  string
	}{
		{&keepsValue{}, "*loom.keepsValue.Keep returned struct { Saves int }, but must return a pointer to a struct"},
		{&keepsNil{}, "returned a nil *loom.chromeKept"},
		{&keepsChan{}, "can't be encoded as JSON"},
		{&keepsUnexported{}, "has no exported fields"},
	} {
		func() {
			defer func() {
				e, ok := recover().(*Error)
				if !ok || e.Code != RT452 || !strings.Contains(e.Msg, tc.msg) || e.Fix == "" || !strings.Contains(e.Where, "keep_test.go") {
					t.Errorf("%T: got %#v", tc.page, e)
				}
			}()
			New().Live("/", tc.page, hxtest.Counter)
		}()
	}
	func() {
		defer func() {
			if e, ok := recover().(*Error); !ok || e.Code != RT452 {
				t.Errorf("live layout: got %#v", e)
			}
		}()
		LiveLayout(hxtest.Chrome, &keptChromeNil{})
	}()
}

type keptChromeNil struct{ keptChrome }

func (l *keptChromeNil) Keep() any { return nil }

func TestKeepNeedsSecret(t *testing.T) {
	app, _ := newKeepApp(t, Config{})
	if err := app.checkKeep(); err == nil || err.(*Error).Code != RT453 {
		t.Errorf("no secret: %v, want RT453", err)
	}
	app, _ = newKeepApp(t, Config{Secret: strings.Repeat("s", 32)})
	if err := app.checkKeep(); err != nil {
		t.Errorf("with a secret: %v", err)
	}
	app, _ = newKeepApp(t, Config{Dev: DevOn})
	if err := app.checkKeep(); err != nil {
		t.Errorf("in dev: %v", err)
	}
	if err := newLiveLayoutApp(t).checkKeep(); err != nil {
		t.Errorf("keeping nothing: %v", err)
	}
	// Dev seals with a fixed key, so a rebuilt app opens what the last one sealed.
	a, b := New(Config{Dev: DevOn}), New(Config{Dev: DevOn})
	s, _ := a.sealKept([]byte(`{}`))
	if _, err := b.openKept(s); err != nil {
		t.Errorf("dev: %v", err)
	}
}

func TestKeepInManifest(t *testing.T) {
	app, _ := newKeepApp(t, Config{})
	text := app.Manifest().Text()
	for _, want := range []string{"    keeps across a restart: Saves\n", "  hxtest.Chrome\n    events, handled by (*loom.keptChrome).Events:", "    keeps across a restart: count, name\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("manifest lacks %q:\n%s", want, text)
		}
	}
}

// keptClient is a browser: its own cookie jar.
func keptClient(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func keptGet(t *testing.T, c *http.Client, url string, header map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func keptPost(t *testing.T, c *http.Client, url, body string) {
	t.Helper()
	res, err := c.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("POST %s = %d", body, res.StatusCode)
	}
}

func TestKeepOverHTTP(t *testing.T) {
	app, srv := newKeepApp(t, Config{Secret: strings.Repeat("s", 32)})
	browser := keptClient(t)
	_, body := keptGet(t, browser, srv.URL+"/kept", nil)
	sid := sidRE.FindStringSubmatch(body)[1]
	if !strings.Contains(body, `"kept":"`) {
		t.Fatalf("the document doesn't carry the kept state:\n%s", body)
	}
	keptPost(t, browser, srv.URL+"/_loom/event", `{"sid":"`+sid+`","event":"bump"}`)
	var sealed string
	app.session(sid).call(func() { sealed = app.session(sid).view.Kept() })
	// The stream sends the kept state with the diff that changed it.
	req, _ := http.NewRequest("GET", srv.URL+"/_loom/stream?sid="+sid+"&v=0", nil)
	res, err := browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64<<10)
	n, _ := res.Body.Read(buf)
	res.Body.Close()
	if !strings.Contains(string(buf[:n]), `"kept":"`+sealed+`"`) {
		t.Errorf("the full tree lacks the kept state:\n%s", buf[:n])
	}

	remount := map[string]string{"X-Loom-Navigate": "-", "X-Loom-Kept": sealed}
	res, body = keptGet(t, browser, srv.URL+"/kept", remount)
	if res.Header.Get("X-Loom-Navigate") != "swap" || !strings.Contains(body, `on:click="bump">1</button>`) {
		t.Errorf("remount: %s\n%s", res.Header.Get("X-Loom-Navigate"), body)
	}
	_, body = keptGet(t, keptClient(t), srv.URL+"/kept", remount)
	if !strings.Contains(body, `on:click="bump">0</button>`) {
		t.Errorf("another browser's remount restored the state:\n%s", body)
	}
	// A page load, unlike a remount, starts afresh.
	_, body = keptGet(t, browser, srv.URL+"/kept", map[string]string{"X-Loom-Kept": sealed})
	if !strings.Contains(body, `on:click="bump">0</button>`) {
		t.Errorf("a page load restored the state:\n%s", body)
	}
}

func TestKeepAcrossRestart(t *testing.T) {
	app, srv := newKeepApp(t, Config{})
	browser := keptClient(t)
	_, body := keptGet(t, browser, srv.URL+"/kept", nil)
	sid := sidRE.FindStringSubmatch(body)[1]
	keptPost(t, browser, srv.URL+"/_loom/event", `{"sid":"`+sid+`","event":"save"}`)
	keptPost(t, browser, srv.URL+"/_loom/event", `{"sid":"`+sid+`","event":"bump"}`)
	browser.Post(srv.URL+"/_loom/event", "application/json", strings.NewReader(`{"sid":"`+sid+`","event":"boom"}`))
	var html string
	app.session(sid).call(func() { html = app.session(sid).view.HTML() })
	if !strings.Contains(html, "saves 1") || !strings.Contains(html, `on:click="bump">1</button>`) {
		t.Errorf("after the first restart: %s", html)
	}
	// A page that fails again soon starts afresh.
	browser.Post(srv.URL+"/_loom/event", "application/json", strings.NewReader(`{"sid":"`+sid+`","event":"boom"}`))
	app.session(sid).call(func() { html = app.session(sid).view.HTML() })
	if !strings.Contains(html, "saves 0") {
		t.Errorf("after the second restart: %s", html)
	}
}
