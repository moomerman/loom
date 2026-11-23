package loom

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
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
	Runtime  RuntimeInfo  `json:"runtime"`
}

// RuntimeInfo is the process as it is now: goroutines, memory and the
// garbage collector, the browsers connected, and the database pool. A
// number that only grows across `loom stats` calls is a leak; a pool
// whose waits grow is short of connections (Config.DBMaxConns).
type RuntimeInfo struct {
	Goroutines  int        `json:"goroutines"`
	HeapBytes   uint64     `json:"heap_bytes"` // allocated and still in use
	HeapObjects uint64     `json:"heap_objects"`
	SysBytes    uint64     `json:"sys_bytes"` // obtained from the OS
	Memory      MemoryInfo `json:"memory"`
	GCRuns      uint32     `json:"gc_runs"`
	// GCPauseMS is the longest of the last 256 collections' pauses.
	GCPauseMS float64 `json:"gc_pause_ms"`
	Streams   int     `json:"streams"` // open SSE streams: browsers connected
	// DB is the connection pool, absent without a database.
	DB *DBPoolInfo `json:"db,omitempty"`
}

// MemoryInfo splits the process's memory the way the Go runtime sees
// it, so a process that grows can be told apart: live objects the
// program still holds (heap_inuse grows, and a heap profile shows
// whose), or memory the program freed that the runtime keeps for
// reuse rather than returning to the OS (heap_unreleased grows while
// heap_inuse doesn't). The fields are runtime.MemStats', in bytes.
type MemoryInfo struct {
	// RSSBytes is the process's resident set as the OS counts it, read
	// from /proc/self/statm; 0 where there is none (not Linux).
	RSSBytes uint64 `json:"rss_bytes"`
	// HeapInuseBytes are the heap's spans holding objects: live ones and
	// garbage not yet swept (MemStats.HeapInuse).
	HeapInuseBytes uint64 `json:"heap_inuse_bytes"`
	// HeapIdleBytes are the heap's spans holding nothing (HeapIdle),
	// of which HeapReleasedBytes went back to the OS and
	// HeapUnreleasedBytes (idle minus released) the runtime still keeps
	// mapped: resident, counted in RSS, but free to reuse.
	HeapIdleBytes       uint64 `json:"heap_idle_bytes"`
	HeapReleasedBytes   uint64 `json:"heap_released_bytes"`
	HeapUnreleasedBytes uint64 `json:"heap_unreleased_bytes"`
	StackBytes          uint64 `json:"stack_bytes"` // goroutine stacks (StackInuse)
	// NextGCBytes is the heap size at which the next collection runs.
	NextGCBytes uint64 `json:"next_gc_bytes"`
	// LastGC is when the last collection finished; absent before one.
	LastGC *time.Time `json:"last_gc,omitempty"`
	// LimitBytes is the soft memory limit (GOMEMLIMIT,
	// debug.SetMemoryLimit); absent when there is none.
	LimitBytes uint64 `json:"limit_bytes,omitempty"`
}

// memoryInfo reads m and the OS's view of the process.
func memoryInfo(m *runtime.MemStats) MemoryInfo {
	mi := MemoryInfo{
		RSSBytes:            residentBytes(),
		HeapInuseBytes:      m.HeapInuse,
		HeapIdleBytes:       m.HeapIdle,
		HeapReleasedBytes:   m.HeapReleased,
		HeapUnreleasedBytes: m.HeapIdle - m.HeapReleased,
		StackBytes:          m.StackInuse,
		NextGCBytes:         m.NextGC,
	}
	if m.LastGC != 0 {
		t := time.Unix(0, int64(m.LastGC)).UTC()
		mi.LastGC = &t
	}
	if limit := debug.SetMemoryLimit(-1); limit != math.MaxInt64 { // -1 reads the limit without changing it
		mi.LimitBytes = uint64(limit)
	}
	return mi
}

// residentBytes is the process's resident set size on Linux, from
// /proc/self/statm (its second field, in pages), or 0.
func residentBytes() uint64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(f[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}

// DBPoolInfo is database/sql's view of the app's connection pool.
type DBPoolInfo struct {
	Max    int     `json:"max"` // Config.DBMaxConns
	Open   int     `json:"open"`
	InUse  int     `json:"in_use"`
	Idle   int     `json:"idle"`
	Waits  int64   `json:"waits"`   // times a statement waited for a connection, since the start
	WaitMS float64 `json:"wait_ms"` // how long those waits took, in all
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
	return StatsResult{Started: a.started.UTC(), Sessions: n, Events: a.stream.Stats(), Runtime: a.runtimeInfo()}
}

func (a *App) runtimeInfo() RuntimeInfo {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	info := RuntimeInfo{Goroutines: runtime.NumGoroutine(), HeapBytes: m.HeapAlloc, HeapObjects: m.HeapObjects, SysBytes: m.Sys, Memory: memoryInfo(&m), GCRuns: m.NumGC}
	var pause uint64
	for _, p := range m.PauseNs {
		pause = max(pause, p)
	}
	info.GCPauseMS = trace.Millis(time.Duration(pause))
	for _, s := range a.Sessions() {
		info.Streams += s.Connected
	}
	if a.db != nil && a.db.SQL() != nil {
		st := a.db.SQL().Stats()
		info.DB = &DBPoolInfo{Max: st.MaxOpenConnections, Open: st.OpenConnections, InUse: st.InUse, Idle: st.Idle, Waits: st.WaitCount, WaitMS: trace.Millis(st.WaitDuration)}
	}
	return info
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
