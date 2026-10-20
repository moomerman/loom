package loom

import (
	"bufio"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestCompression gzips what's worth gzipping, and leaves small bodies,
// compressed types, ranges, HEAD and event streams alone.
func TestCompression(t *testing.T) {
	big := strings.Repeat("<p>chip music</p>\n", 200)
	app := New(Config{})
	g := app.Group(Assets(fstest.MapFS{"big.js": {Data: []byte(strings.Repeat("let x = 1;\n", 300))}}))
	jsURL, _ := g.assets.url("big.js")
	app.Handle("GET /big", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		for i := 0; i < len(big); i += 100 { // in pieces smaller than compressMin
			io.WriteString(w, big[i:min(i+100, len(big))])
		}
	}))
	app.Handle("GET /sniffed", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<!DOCTYPE html>"+big)
	}))
	app.Handle("GET /small", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<p>hi</p>")
	}))
	app.Handle("GET /png", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		io.WriteString(w, big)
	}))
	app.Handle("GET /missing", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, big)
	}))
	srv := httptest.NewServer(app)
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}

	get := func(method, p, encoding string, h ...string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+p, nil)
		if encoding != "" {
			req.Header.Set("Accept-Encoding", encoding)
		}
		for i := 0; i+1 < len(h); i += 2 {
			req.Header.Set(h[i], h[i+1])
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body io.Reader = res.Body
		if res.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(res.Body)
			if err != nil {
				t.Fatalf("%s %s: %v", method, p, err)
			}
			body = zr
		}
		b, err := io.ReadAll(body)
		if err != nil {
			t.Fatalf("%s %s: %v", method, p, err)
		}
		return res, string(b)
	}

	for _, c := range []struct {
		method, path, encoding string
		header                 []string
		status                 int
		gzipped                bool
		body                   string
	}{
		{"GET", "/big", "gzip, deflate, br", nil, 200, true, big},
		{"GET", "/sniffed", "gzip", nil, 200, true, "<!DOCTYPE html>" + big},
		{"GET", "/big", "", nil, 200, false, big},
		{"GET", "/big", "br, gzip;q=0", nil, 200, false, big},
		{"GET", "/big", "*", nil, 200, true, big},
		{"HEAD", "/big", "gzip", nil, 200, false, ""},
		{"GET", "/small", "gzip", nil, 200, false, "<p>hi</p>"},
		{"GET", "/png", "gzip", nil, 200, false, big},
		{"GET", "/missing", "gzip", nil, 404, true, big},
		{"GET", jsURL, "gzip", nil, 200, true, strings.Repeat("let x = 1;\n", 300)},
		{"GET", jsURL, "gzip", []string{"Range", "bytes=0-9"}, 206, false, "let x = 1;"},
		{"GET", "/_loom/loom.js", "gzip", nil, 200, true, ""},
	} {
		res, body := get(c.method, c.path, c.encoding, c.header...)
		if res.StatusCode != c.status || (res.Header.Get("Content-Encoding") == "gzip") != c.gzipped || (c.body != "" && body != c.body) {
			t.Errorf("%s %s (Accept-Encoding %q, %v) = %d, encoding %q, %d bytes; want %d, gzipped %v",
				c.method, c.path, c.encoding, c.header, res.StatusCode, res.Header.Get("Content-Encoding"), len(body), c.status, c.gzipped)
		}
		if strings.HasPrefix(res.Header.Get("Content-Type"), "text/") && !strings.Contains(strings.Join(res.Header.Values("Vary"), ","), "Accept-Encoding") {
			t.Errorf("%s %s: no Vary: Accept-Encoding in %v", c.method, c.path, res.Header)
		}
	}
}

// TestCompressionStreams leaves event streams uncompressed and sends a
// compressed response's flushed parts as they're flushed.
func TestCompressionStreams(t *testing.T) {
	release := make(chan struct{})
	app := New(Config{})
	app.Handle("GET /events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: one\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	app.Handle("GET /flushed", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<p>first</p>\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "<p>second</p>\n")
	}))
	srv := httptest.NewServer(app)
	defer srv.Close()
	defer close(release)
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}

	open := func(p string) *http.Response {
		req, _ := http.NewRequest("GET", srv.URL+p, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	res := open("/events")
	defer res.Body.Close()
	if e := res.Header.Get("Content-Encoding"); e != "" {
		t.Fatalf("event stream encoded %q", e)
	}
	if line, err := bufio.NewReader(res.Body).ReadString('\n'); err != nil || line != "event: one\n" {
		t.Fatalf("event stream's first line = %q, %v", line, err)
	}

	res = open("/flushed")
	defer res.Body.Close()
	if e := res.Header.Get("Content-Encoding"); e != "gzip" {
		t.Fatalf("flushed page encoded %q, want gzip", e)
	}
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(zr).ReadString('\n'); err != nil || line != "<p>first</p>\n" {
		t.Fatalf("flushed page's first line = %q, %v", line, err)
	}
}

func TestAcceptsGzip(t *testing.T) {
	for header, want := range map[string]bool{
		"":                     false,
		"gzip":                 true,
		"gzip, deflate, br":    true,
		"br;q=1.0, gzip;q=0.8": true,
		"gzip;q=0":             false,
		"identity":             false,
		"*":                    true,
		"x-gzip":               false,
	} {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}
