package loom

import (
	"bufio"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Compression (docs/live.md). The app gzips its responses itself, so an
// app served straight to the internet, or behind a proxy that passes
// bodies through, needs no setup: pages, navigations, patches, scripts,
// stylesheets, embedded assets and JSON go out compressed to any browser
// that accepts gzip. A response is left as it is when it is small (under
// compressMin bytes), already encoded, a range, of a type that is
// compressed already (images, audio, fonts, archives) or a stream: Server-
// Sent Events are flushed one message at a time, and some proxies hold a
// compressed stream back until it ends.

// compressMin is the smallest body worth compressing: below it gzip's
// header and a round trip of headers cost more than they save.
const compressMin = 1024

// gzipWriters reuses gzip writers, which allocate a lot to set up.
var gzipWriters = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return w
}}

// compress wraps w so the response is gzipped when r accepts gzip and the
// response is worth compressing. The caller must call close when the
// handler returns.
func compress(w http.ResponseWriter, r *http.Request) *compressWriter {
	return &compressWriter{ResponseWriter: w, accepts: r.Method != http.MethodHead && acceptsGzip(r.Header.Get("Accept-Encoding"))}
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(part, ";")
		name = strings.TrimSpace(name)
		if name != "gzip" && name != "*" {
			continue
		}
		q := strings.TrimSpace(params)
		if v, ok := strings.CutPrefix(q, "q="); ok {
			f, err := strconv.ParseFloat(v, 64)
			return err == nil && f > 0
		}
		return true
	}
	return false
}

// compressible reports whether a content type is worth compressing: text,
// and the structured formats that aren't compressed already.
func compressible(contentType string) bool {
	t, _, _ := strings.Cut(contentType, ";")
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
	case t == "text/event-stream":
		return false
	case strings.HasPrefix(t, "text/"), strings.HasSuffix(t, "+json"), strings.HasSuffix(t, "+xml"):
		return true
	}
	switch t {
	case "application/json", "application/javascript", "application/xml", "application/wasm",
		"image/svg+xml", "image/x-icon", "image/vnd.microsoft.icon", "image/bmp",
		"font/ttf", "font/otf", "application/vnd.ms-fontobject":
		return true
	}
	return false
}

// compressWriter holds back the start of a response until it knows
// whether to compress it: until compressMin bytes are written, or the
// handler flushes or returns.
type compressWriter struct {
	http.ResponseWriter
	accepts bool         // the request accepts gzip
	status  int          // the status the handler wrote, or 0
	buf     []byte       // the body written before deciding
	decided bool         // the header has gone out
	gz      *gzip.Writer // non-nil when compressing
}

func (w *compressWriter) WriteHeader(status int) {
	if w.decided || w.status != 0 {
		return
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status) // informational, e.g. 103 Early Hints
		return
	}
	w.status = status
	if status == http.StatusNoContent || status == http.StatusNotModified || status == http.StatusSwitchingProtocols {
		w.decide(true)
	}
}

func (w *compressWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if !w.decided {
		if len(w.buf)+len(b) < compressMin && !w.declined() {
			w.buf = append(w.buf, b...)
			return len(b), nil
		}
		w.buf = append(w.buf, b...)
		if err := w.decide(false); err != nil {
			return 0, err
		}
		return len(b), nil
	}
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// declined reports whether the headers already rule out compression, so
// the body needn't be held back.
func (w *compressWriter) declined() bool {
	h := w.Header()
	if !w.accepts || h.Get("Content-Encoding") != "" || w.status == http.StatusPartialContent {
		return true
	}
	if _, set := h["Content-Type"]; set && !compressible(h.Get("Content-Type")) {
		return true
	}
	if n, err := strconv.Atoi(h.Get("Content-Length")); err == nil && n < compressMin {
		return true
	}
	return false
}

// decide sends the header, compressed or not, and the body held back.
// final says the handler has finished, so the held-back body is all of it.
func (w *compressWriter) decide(final bool) error {
	if w.decided {
		return nil
	}
	w.decided = true
	if w.status == 0 {
		return nil // nothing written: net/http sends its own empty 200
	}
	h := w.Header()
	if _, set := h["Content-Type"]; !set && len(w.buf) > 0 {
		h.Set("Content-Type", http.DetectContentType(w.buf)) // as net/http would
	}
	if compressible(h.Get("Content-Type")) {
		h.Add("Vary", "Accept-Encoding")
	}
	if !w.declined() && !(final && len(w.buf) < compressMin) {
		h.Del("Content-Length")
		h.Del("Accept-Ranges")
		h.Set("Content-Encoding", "gzip")
		if etag := h.Get("ETag"); strings.HasPrefix(etag, `"`) {
			h.Set("ETag", "W/"+etag) // the bytes differ from the identity body's
		}
		w.gz = gzipWriters.Get().(*gzip.Writer)
		w.gz.Reset(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(w.status)
	buf := w.buf
	w.buf = nil
	if len(buf) == 0 {
		return nil
	}
	var err error
	if w.gz != nil {
		_, err = w.gz.Write(buf)
	} else {
		_, err = w.ResponseWriter.Write(buf)
	}
	return err
}

// Flush sends what's written so far, deciding first if need be.
func (w *compressWriter) Flush() {
	if w.status == 0 && !w.decided {
		w.status = http.StatusOK
	}
	w.decide(false)
	if w.gz != nil {
		w.gz.Flush()
	}
	http.NewResponseController(w.ResponseWriter).Flush()
}

// close finishes the response once the handler has returned.
func (w *compressWriter) close() {
	w.decide(true)
	if w.gz != nil {
		w.gz.Close()
		w.gz.Reset(io.Discard)
		gzipWriters.Put(w.gz)
		w.gz = nil
	}
}

// Hijack hands over the connection, as the server's own writer does.
func (w *compressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.decided = true
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// Unwrap lets http.ResponseController reach the server's writer.
func (w *compressWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
