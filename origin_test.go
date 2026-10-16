package loom

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCrossOrigin(t *testing.T) {
	for _, c := range []struct {
		method, site, origin string
		refused              bool
	}{
		{"GET", "cross-site", "https://evil.example", false}, // safe methods are never refused
		{"POST", "same-origin", "", false},
		{"POST", "none", "", false},
		{"POST", "cross-site", "", true},
		{"POST", "same-site", "", true},
		{"DELETE", "cross-site", "", true},
		{"POST", "", "", false}, // not a browser: curl, a webhook, the CLI
		{"POST", "", "http://app.example", false},
		{"POST", "", "https://evil.example", true},
		{"POST", "", "null", true},
	} {
		r := httptest.NewRequest(c.method, "http://app.example/login", nil)
		if c.site != "" {
			r.Header.Set("Sec-Fetch-Site", c.site)
		}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := crossOrigin(r); got != c.refused {
			t.Errorf("%s Sec-Fetch-Site=%q Origin=%q: refused %v, want %v", c.method, c.site, c.origin, got, c.refused)
		}
	}
}

// The check is in front of every route: plain handlers and live events.
func TestCrossOriginRefused(t *testing.T) {
	app := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ran := false
	app.Handle("POST /transfer", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true }))
	for _, path := range []string{"/transfer", "/_loom/event"} {
		r := httptest.NewRequest("POST", path, nil)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		var e struct{ Code string }
		json.NewDecoder(w.Body).Decode(&e)
		if w.Code != 403 || e.Code != RT429 {
			t.Errorf("POST %s from another site: %d %s, want 403 RT429", path, w.Code, e.Code)
		}
	}
	if ran {
		t.Error("the handler ran")
	}
	r := httptest.NewRequest("POST", "/transfer", nil)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	app.ServeHTTP(httptest.NewRecorder(), r)
	if !ran {
		t.Error("a same-origin post was refused")
	}
}
