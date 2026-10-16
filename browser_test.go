package loom

import (
	"bufio"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// A page's session id is enough to act on it only from the browser that
// loaded it: another browser that learns the id (from a log, say) can't
// send events, read the stream, or end the page by navigating from it.
func TestSessionBoundToBrowser(t *testing.T) {
	app, srv := newTestApp(t)
	sid, _ := load(t, srv)

	jar, _ := cookiejar.New(nil)
	other := &http.Client{Jar: jar}
	do := func(c *http.Client, method, path, body string, header ...string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}
	event := `{"sid":"` + sid + `","event":"inc","payload":{}}`

	for name, c := range map[string]*http.Client{"no cookie": {}, "another browser's cookie": other} {
		if c == other {
			do(other, "GET", "/", "") // it loads a page of its own, so has a cookie
		}
		if res := do(c, "POST", "/_loom/event", event); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: event = %d, want 404", name, res.StatusCode)
		}
		res := do(c, "GET", "/_loom/stream?v=0&sid="+sid, "")
		if line, _ := bufio.NewReader(res.Body).ReadString('\n'); line != "event: reload\n" {
			t.Errorf("%s: stream began %q, want a reload and no tree", name, line)
		}
		if res := do(c, "GET", "/", "", navigateHeader, sid); res.Header.Get(navigateHeader) != "reload" {
			t.Errorf("%s: navigating from the page answered %q, want reload", name, res.Header.Get(navigateHeader))
		}
	}
	if app.session(sid) == nil {
		t.Fatal("another browser's navigation ended the page")
	}

	// The browser that loaded it still can.
	ch := stream(t, srv, sid, "1")
	if code := post(t, srv, event); code != 200 {
		t.Fatalf("event = %d", code)
	}
	if ev := next(t, ch); ev.event != "diff" {
		t.Fatalf("got %+v", ev)
	}
}

// The cookie is set once, HttpOnly, and kept on later loads.
func TestBrowserCookie(t *testing.T) {
	_, srv := newTestApp(t)
	res, err := (&http.Client{}).Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	var ck *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == BrowserCookie {
			ck = c
		}
	}
	if ck == nil || !ck.HttpOnly || ck.Path != "/" || ck.SameSite != http.SameSiteLaxMode || len(ck.Value) < 32 {
		t.Fatalf("browser cookie %+v, want HttpOnly, Path=/, SameSite=Lax, 256 bits", ck)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.AddCookie(ck)
	res, err = http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(res.Cookies()) != 0 {
		t.Errorf("a browser with the cookie got another: %v", res.Cookies())
	}
}
