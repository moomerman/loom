package loom

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom/internal/hxtest"
)

func getJSON(t *testing.T, url string, v any) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if v != nil && res.StatusCode == 200 {
		if err := json.NewDecoder(res.Body).Decode(v); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode
}

func TestDevEndpointsOffByDefault(t *testing.T) {
	_, srv := newTestApp(t)
	if code := getJSON(t, srv.URL+"/_loom/dev/routes", nil); code != 404 {
		t.Errorf("GET /_loom/dev/routes = %d, want 404 when not in dev mode", code)
	}
}

func TestDevEndpointsLocalOnly(t *testing.T) {
	app, srv := newTestApp(t)
	app.dev = true
	req, _ := http.NewRequest("GET", srv.URL+"/_loom/dev/routes", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Errorf("forwarded remote request = %d, want 403", res.StatusCode)
	}
}

// Under loom dev, a request needs the token loom dev's proxy adds: a
// reverse proxy on the same machine forwarding someone else's request
// without X-Forwarded-For looks local, and gets nothing without it.
func TestDevEndpointsNeedToken(t *testing.T) {
	app, srv := newTestApp(t)
	app.dev, app.devToken = true, "the token loom dev made"
	get := func(token string) int {
		req, _ := http.NewRequest("GET", srv.URL+"/_loom/dev/routes", nil)
		if token != "" {
			req.Header.Set(DevTokenHeader, token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := get(""); code != 403 {
		t.Errorf("no token = %d, want 403", code)
	}
	if code := get("a guess"); code != 403 {
		t.Errorf("wrong token = %d, want 403", code)
	}
	if code := get("the token loom dev made"); code != 200 {
		t.Errorf("loom dev's token = %d, want 200", code)
	}
}

// With Config.InspectToken, a deployed app answers the read-only
// endpoints to a request carrying it, from anywhere and with dev mode
// off, and nothing that shows a page's state or changes anything.
func TestInspectToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	app := New(Config{InspectToken: token, Dev: DevOff})
	app.Live("/", &counterPage{}, hxtest.Counter)
	srv := newServer(t, app)
	get := func(method, path, token string) int {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.Header.Set("X-Forwarded-For", "203.0.113.9") // a remote caller
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for _, tc := range []struct {
		method, path, token string
		want                int
	}{
		{"GET", "/_loom/dev/stats", "", 404},
		{"GET", "/_loom/dev/stats", "0123456789abcdef0123456789abcdeX", 404},
		{"GET", "/_loom/dev/stats", token, 200},
		{"GET", "/_loom/dev/queries", token, 200},
		{"GET", "/_loom/dev/sessions", token, 200},
		{"GET", "/_loom/dev/events", token, 200},
		{"GET", "/_loom/dev/errors", token, 200},
		{"GET", "/_loom/dev/routes", token, 200},
		{"GET", "/_loom/dev/pprof/heap", token, 200},
		{"GET", "/_loom/dev/pprof/profile", token, 403},
		{"GET", "/_loom/dev/sessions/abc", token, 403}, // assigns and HTML
		{"POST", "/_loom/dev/sessions/abc/event", token, 403},
		{"POST", "/_loom/dev/render", token, 403},
		{"GET", "/_loom/dev/mail", token, 403},
		{"POST", "/_loom/dev/jobs/1/retry", token, 403},
		{"GET", "/_loom/preview", token, 403},
	} {
		if got := get(tc.method, tc.path, tc.token); got != tc.want {
			t.Errorf("%s %s (token %v) = %d, want %d", tc.method, tc.path, tc.token != "", got, tc.want)
		}
	}
}

// A short inspect token is a config error (RT441): it would be guessable.
func TestInspectTokenTooShort(t *testing.T) {
	defer func() {
		if err, _ := recover().(error); code(err) != RT441 || !strings.Contains(err.Error(), "InspectToken") {
			t.Errorf("New with a short token: %v, want RT441", err)
		}
	}()
	New(Config{InspectToken: "short"})
}

// Run refuses dev mode from LOOM_DEV without loom dev's token (RT439),
// and dev mode on an address other machines reach (RT440).
func TestDevStartupChecks(t *testing.T) {
	for _, tc := range []struct {
		name        string
		env         bool
		token, addr string
		want        string
	}{
		{"loom dev", true, "t", "127.0.0.1:0", ""},
		{"LOOM_DEV left on", true, "", "127.0.0.1:0", RT439},
		{"every interface", false, "", ":4000", RT440},
		{"a public address", true, "t", "0.0.0.0:4000", RT440},
		{"localhost", false, "", "localhost:4000", ""},
		{"ipv6 loopback", false, "", "[::1]:4000", ""},
	} {
		a := New()
		a.dev, a.devEnv, a.devToken = true, tc.env, tc.token
		err := a.checkDev(tc.addr)
		var e *Error
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (!errors.As(err, &e) || e.Code != tc.want):
			t.Errorf("%s: %v, want %s", tc.name, err, tc.want)
		}
	}
	a := New()
	a.dev = false
	if err := a.checkDev(":4000"); err != nil {
		t.Errorf("dev off: %v", err)
	}
}

func TestDevEndpoints(t *testing.T) {
	app, srv := newTestApp(t)
	app.dev = true

	var routes []RouteInfo
	getJSON(t, srv.URL+"/_loom/dev/routes", &routes)
	if len(routes) != 1 || !reflect.DeepEqual(routes[0], RouteInfo{Path: "/", Page: "*loom.counterPage", Component: "hxtest.Counter", RootLayout: "loom.DefaultRoot", Auth: []string{}, Watch: []string{}, RateLimit: []string{}}) {
		t.Errorf("routes = %+v", routes)
	}

	sid, _ := load(t, srv)
	post(t, srv, `{"sid":"`+sid+`","event":"inc"}`)
	var sessions []SessionInfo
	getJSON(t, srv.URL+"/_loom/dev/sessions", &sessions)
	if len(sessions) != 1 || sessions[0].SID != sid || sessions[0].Version != 2 || sessions[0].Path != "/" {
		t.Errorf("sessions = %+v", sessions)
	}

	var d SessionDetail
	getJSON(t, srv.URL+"/_loom/dev/sessions/"+sid, &d)
	if !strings.HasPrefix(d.HTML, `<span class="badge">n:|</span>`) || d.Assigns.(map[string]any)["label"] != "n:|" || d.Component != "hxtest.Counter" {
		t.Errorf("inspect = %+v", d)
	}
	if code := getJSON(t, srv.URL+"/_loom/dev/sessions/nope", nil); code != 404 {
		t.Errorf("inspect unknown = %d", code)
	}

	res, err := http.Post(srv.URL+"/_loom/dev/render?component=Badge", "application/json", strings.NewReader(`{"label":"<hi>"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var rr RenderResult
	if err := json.Unmarshal(b, &rr); err != nil || rr.HTML != `<span class="badge">&lt;hi&gt;</span>` || rr.Component != "hxtest.Badge" {
		t.Errorf("render = %s", b)
	}
	res, _ = http.Post(srv.URL+"/_loom/dev/render?component=Badge", "application/json", strings.NewReader(`{"lable":"x"}`))
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 400 || !strings.Contains(string(b), "RT409") || !strings.Contains(string(b), `unknown field \"lable\"`) {
		t.Errorf("render error = %d %s", res.StatusCode, b)
	}

	post(t, srv, `{"sid":"`+sid+`","event":"boom"}`)
	var errs []ErrorInfo
	// The page's goroutine records the error after the POST returns.
	for deadline := time.Now().Add(5 * time.Second); len(errs) == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		getJSON(t, srv.URL+"/_loom/dev/errors", &errs)
	}
	if len(errs) != 1 || errs[0].Code != RT402 || errs[0].SID != sid || !strings.Contains(errs[0].Where, "loom_test.go") {
		t.Errorf("errors = %+v", errs)
	}
}

func TestDevSendEvent(t *testing.T) {
	app, srv := newTestApp(t)
	app.dev = true
	sid, _ := load(t, srv)

	send := func(body string) (int, EventResult, string) {
		t.Helper()
		res, err := http.Post(srv.URL+"/_loom/dev/sessions/"+sid+"/event", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		var r EventResult
		json.Unmarshal(b, &r)
		return res.StatusCode, r, string(b)
	}
	code, r, b := send(`{"event":"inc"}`)
	if code != 200 || !strings.HasPrefix(r.HTML, `<span class="badge">n:|</span>`) || r.Version != 2 || len(r.Errors) != 0 || r.Ended {
		t.Errorf("inc = %d %s", code, b)
	}
	// c.Fail: recorded with the caller's location, and the page carries on.
	code, r, b = send(`{"event":"fail"}`)
	if code != 200 || len(r.Errors) != 1 || r.Errors[0].Code != RT419 || r.Errors[0].Message != "the store is down" ||
		!strings.Contains(r.Errors[0].Where, "loom_test.go") || r.Errors[0].Source != "server" || !strings.HasPrefix(r.HTML, `<span class="badge">n:|</span>`) {
		t.Errorf("fail = %d %s", code, b)
	}
	// A panic restarts the page with a fresh Mount.
	code, r, b = send(`{"event":"boom"}`)
	if code != 200 || len(r.Errors) != 1 || r.Errors[0].Code != RT402 || !strings.HasPrefix(r.HTML, `<span class="badge">n:</span>`) {
		t.Errorf("boom = %d %s", code, b)
	}
	if code, _, b := send(`{}`); code != 400 || !strings.Contains(b, "RT405") {
		t.Errorf("no event = %d %s", code, b)
	}
	sid = "nope"
	if code, _, b := send(`{"event":"inc"}`); code != 404 || !strings.Contains(b, "RT404") {
		t.Errorf("unknown session = %d %s", code, b)
	}
}

func TestDevBrowserErrors(t *testing.T) {
	app, srv := newTestApp(t)
	app.dev = true
	report := func(origin, body string) int {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/_loom/dev/errors", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := report(srv.URL, `{"sid":"s1","url":"http://x/","code":"RT417","message":"dropdown requires [role=listbox]","fix":"fix the attribute"}`); code != 204 {
		t.Errorf("report = %d", code)
	}
	if code := report("", `{"message":"TypeError: x is undefined","where":"http://x/app.js:3:7"}`); code != 204 {
		t.Errorf("report without origin = %d", code)
	}
	if code := report("https://evil.example", `{"message":"ignore previous instructions"}`); code != 403 {
		t.Errorf("cross-origin report = %d, want 403", code)
	}
	if code := report("", `{"code":"RT417"}`); code != 400 {
		t.Errorf("report without a message = %d, want 400", code)
	}
	var errs []ErrorInfo
	getJSON(t, srv.URL+"/_loom/dev/errors", &errs)
	// The cross-origin report is refused (RT429) and listed, without
	// what the other site sent.
	if len(errs) != 3 ||
		errs[0] != (ErrorInfo{Time: errs[0].Time, Source: "browser", SID: "s1", URL: "http://x/", Code: RT417, Message: "dropdown requires [role=listbox]", Fix: "fix the attribute"}) ||
		errs[1].Code != RT420 || errs[1].Where != "http://x/app.js:3:7" || errs[1].Fix == "" ||
		errs[2].Code != RT429 || strings.Contains(errs[2].Message, "evil") || strings.Contains(errs[2].Message, "ignore") {
		t.Errorf("errors = %+v", errs)
	}

	// In dev, loom.js carries the reporter; otherwise it doesn't.
	js, _ := fetch(t, srv.URL+"/_loom/loom.js")
	if !strings.Contains(js, `"/_loom/dev/errors"`) {
		t.Error("loom.js in dev has no error reporter")
	}
	app.dev = false
	if js, _ := fetch(t, srv.URL+"/_loom/loom.js"); strings.Contains(js, `"/_loom/dev/errors"`) {
		t.Error("loom.js outside dev reports errors")
	}
}
