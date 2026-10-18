package loom

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/moomerman/loom/client"
	"github.com/moomerman/loom/trace"
)

// servePage starts a live page's session and serves its first render:
// the root layout with the live root inside, the encoded tree for the
// client to take over from, and the client runtime.
func (a *App) servePage(w http.ResponseWriter, r *http.Request, rt *route) {
	// A live navigation (navigate.go) leaves the session from. If both
	// pages are in the same live layout, its state moves to the new page
	// (see LiveLayout), and goes back if the new page doesn't mount.
	var from *session
	var carry *carried
	if sid := r.Header.Get(navigateHeader); sid != "" && sid != "-" {
		if from = a.pageSession(r, sid); from != nil {
			from.call(func() { carry = from.view.lend(rt) })
		}
	}
	// A remount brings back the state the page kept (keep.go).
	kept := ""
	if r.Header.Get(navigateHeader) == "-" {
		kept = r.Header.Get(keepHeader)
	}
	_, doc, err := a.startSession(rt, r, browserID(w, r), carry, kept)
	if err != nil && carry != nil {
		from.call(func() { from.view.giveBack() })
	}
	var denied *authError
	var away *RedirectError
	switch {
	case errors.As(err, &denied):
		a.refuse(w, r, rt.group, denied)
		return
	case errors.As(err, &away): // Mount called c.Navigate
		http.Redirect(w, r, away.URL, http.StatusSeeOther)
		return
	case errors.Is(err, ErrPageNotFound): // Mount called c.NotFound
		a.serveError(w, r, rt.group, http.StatusNotFound, nil)
		return
	}
	var over *LimitError
	if errors.As(err, &over) {
		a.refuseLimited(w, r, rt.group, over)
		return
	}
	var e *Error
	switch {
	case errors.As(err, &e) && e.Code == RT444: // logged by roomForSession
		a.serveError(w, r, rt.group, http.StatusServiceUnavailable, e)
		return
	case err != nil: // reported by startSession
		a.serveError(w, r, rt.group, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Vary", navigateHeader)
	if r.Header.Get(navigateHeader) != "" {
		// The browser swaps in this page and leaves the session it came
		// from, which ends now.
		w.Header().Set(navigateHeader, "swap")
		if from != nil {
			from.close("navigated")
		}
	}
	io.WriteString(w, doc)
}

// serveStream streams a session's updates as Server-Sent Events. The
// browser passes the version it has; if that is out of date it first gets
// the full tree.
func (a *App) serveStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // don't let proxies buffer the stream

	s := a.pageSession(r, r.URL.Query().Get("sid"))
	if s == nil {
		// The session expired or the server restarted: start over.
		writeEvent(w, message{event: "reload", data: []byte("{}")})
		flusher.Flush()
		return
	}
	v, _ := strconv.Atoi(r.URL.Query().Get("v"))
	ch := s.subscribe(v)
	defer s.unsubscribe(ch)
	flusher.Flush()
	a.stream.Emit(r.Context(), trace.StreamOpen, "sid", s.id, "full", v != s.currentVersion())
	reason := "browser"
	defer func() { a.stream.Emit(r.Context(), trace.StreamClose, "sid", s.id, "reason", reason) }()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case m, ok := <-ch:
			if !ok {
				// Too slow (stream.slow), or a newer stream replaced it;
				// the browser reconnects.
				reason = "dropped"
				return
			}
			writeEvent(w, m)
			flusher.Flush()
			if m.event == "reload" {
				reason = "reload"
				return
			}
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-a.closing:
			reason = "shutdown"
			return // the browser reconnects to the restarted server
		}
	}
}

func writeEvent(w http.ResponseWriter, m message) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.event, m.data)
}

// eventRequest is the body of POST /_loom/event. Client and Seq name
// the event for deduplication (outbox.go); without them it runs each time
// it is sent. Moved says the browser queued it for an earlier session of
// the page and moved it to this one, after a restart or a reload.
type eventRequest struct {
	SID     string  `json:"sid"`
	Client  string  `json:"client"`
	Seq     int64   `json:"seq"`
	Event   string  `json:"event"`
	Payload Payload `json:"payload"`
	Moved   bool    `json:"moved"`
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v)
}

func writeError(w http.ResponseWriter, status int, e *Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"code": e.Code, "message": e.Msg, "fix": e.Fix})
}

func (a *App) serveClient(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(client.JS)
	if a.dev {
		w.Write(client.Dev) // reports script errors to `loom errors`
	}
}

func (a *App) serveNavigateJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(client.Navigate)
}
