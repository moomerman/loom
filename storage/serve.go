package storage

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Serve answers a request with the object at key: its contents, with
// its Content-Type, Content-Length, ETag and Last-Modified, and its
// Cache-Control unless the handler set one first. A request whose
// If-None-Match has the object's ETag gets 304 Not Modified. A missing
// object or an invalid key is 404. Any other failure is 502, and Serve
// returns the error for the handler to log; a client that goes away
// mid-download isn't an error.
//
//	mux.HandleFunc("GET /avatars/{id}", func(w http.ResponseWriter, r *http.Request) {
//		storage.Serve(w, r, app.Storage(), "avatars/"+r.PathValue("id"))
//	})
//
// The contents pass through the app, so the bucket can stay private and
// a CDN in front of the app caches what it serves.
func Serve(w http.ResponseWriter, r *http.Request, b Bucket, key string) error {
	body, obj, err := b.Get(r.Context(), key)
	if errors.Is(err, ErrNotFound) || errors.Is(err, &Error{Code: SG006}) {
		http.NotFound(w, r)
		return nil
	}
	if err != nil {
		if r.Context().Err() != nil {
			return nil
		}
		http.Error(w, "can't read the file from storage", http.StatusBadGateway)
		return err
	}
	defer body.Close()
	h := w.Header()
	if h.Get("Cache-Control") == "" && obj.CacheControl != "" {
		h.Set("Cache-Control", obj.CacheControl)
	}
	if obj.ETag != "" {
		h.Set("ETag", obj.ETag)
	}
	if !obj.Modified.IsZero() {
		h.Set("Last-Modified", obj.Modified.Format(http.TimeFormat))
	}
	if obj.ETag != "" && etagMatch(r.Header.Get("If-None-Match"), obj.ETag) {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	h.Set("Content-Type", obj.ContentType)
	h.Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		io.Copy(w, body) // a client gone mid-download isn't the app's error
	}
	return nil
}

// etagMatch reports whether an If-None-Match header lists etag, or is *.
func etagMatch(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == "*" || t == etag {
			return true
		}
	}
	return false
}
