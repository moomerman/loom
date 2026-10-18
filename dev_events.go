package loom

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/trace"
)

// EventsResult is a page of the event stream, as /_loom/dev/events and
// `loom events --json` give it.
type EventsResult struct {
	Events []trace.Event `json:"events"` // oldest first
	// Next is the cursor for the next call (?since=): the sequence of
	// the latest event the stream had, matching or not.
	Next uint64 `json:"next"`
}

// StatsResult counts the app's events since it started, for `loom stats`.
type StatsResult struct {
	Started  time.Time    `json:"started"`
	Sessions int          `json:"sessions"` // live pages open now
	Events   []trace.Stat `json:"events"`   // by name, and by route, job type or table
}

// QueriesResult counts the statements the app's database ran since it
// started, for `loom queries`: Loom's own (the job store, sessions,
// rate limits) as well as the app's.
type QueriesResult struct {
	Started time.Time        `json:"started"`
	Queries []data.QueryStat `json:"queries"` // the busiest first, by total time
}

// eventFilter reads a trace.Filter from a request's query.
func eventFilter(r *http.Request) trace.Filter {
	q := r.URL.Query()
	return trace.Filter{Name: q.Get("name"), Trace: q.Get("trace"), SID: q.Get("sid"), Level: q.Get("level")}
}

// serveEvents answers GET /_loom/dev/events: the kept events after
// ?since= that match the filter, or with ?follow=1 a stream of them as
// Server-Sent Events, the kept ones first.
func (a *App) serveEvents(w http.ResponseWriter, r *http.Request) {
	f := eventFilter(r)
	since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
	if r.URL.Query().Get("follow") == "" {
		next := a.stream.Last()
		writeJSON(w, EventsResult{Events: a.stream.Since(since, f), Next: next})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch, stop := a.stream.Subscribe(f)
	defer stop()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	send := func(e trace.Event) {
		b, _ := json.Marshal(e)
		w.Write([]byte("data: "))
		w.Write(b)
		w.Write([]byte("\n\n"))
	}
	last := since
	for _, e := range a.stream.Since(since, f) {
		send(e)
		last = e.Seq
	}
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.Seq <= last { // sent from the ring already
				continue
			}
			send(e)
			flusher.Flush()
		case <-ping.C:
			w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-a.closing:
			return
		}
	}
}

// Stats counts the app's events since it started.
func (a *App) Stats() StatsResult {
	a.mu.Lock()
	n := len(a.sessions)
	a.mu.Unlock()
	return StatsResult{Started: a.started.UTC(), Sessions: n, Events: a.stream.Stats()}
}

// Queries counts the statements the app's database ran since it
// started, the busiest first. It is empty without a database.
func (a *App) Queries() QueriesResult {
	res := QueriesResult{Started: a.started.UTC(), Queries: []data.QueryStat{}}
	if a.db != nil {
		res.Queries = a.db.QueryStats()
	}
	return res
}

// Topics maps each PubSub topic the open pages subscribe to to the
// sessions subscribed, for /_loom/dev/topics.
func (a *App) Topics() map[string][]string {
	a.mu.Lock()
	list := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		list = append(list, s)
	}
	a.mu.Unlock()
	out := map[string][]string{}
	for _, s := range list {
		s.call(func() {
			for _, t := range s.view.Topics() {
				out[t] = append(out[t], s.id)
			}
		})
	}
	for _, sids := range out {
		sort.Strings(sids)
	}
	return out
}
