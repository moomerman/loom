package loom

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/moomerman/loom/jobs"
)

// HealthPath answers load balancers and orchestrators: 200 while the app
// can serve, 503 once it is draining for shutdown or can't reach its
// database. It is served in production, unlike /_loom/dev/.
const HealthPath = "/_loom/health"

// Health is the body of GET /_loom/health.
type Health struct {
	Status string `json:"status"` // "ok", "draining" or "unavailable"
	DB     string `json:"db"`     // "ok", "none" or the error
	Jobs   string `json:"jobs"`   // "running", "enqueue-only" (Config.Jobs), "none" or the error starting them
	Leader bool   `json:"leader"` // this node runs cron and rescue for the cluster
	// Node is this node in the jobs' node registry, from memory: its
	// name (as jobsweb and `loom nodes` show it), queues with running
	// jobs, and its latest heartbeat. Null unless it runs jobs.
	Node *jobs.Node `json:"node"`
	// Watch is the table watch's LISTEN: "ok", "idle" (opened when a
	// page watching a table first mounts), "reconnecting", or "none"
	// without a database.
	Watch string `json:"watch"`
	// PubSub is the PubSub's LISTEN for other nodes' broadcasts: "ok",
	// "idle" (opened when a page first subscribes), "reconnecting", or
	// "none" without a database or with Config.PubSub.
	PubSub   string `json:"pubsub"`
	Sessions int    `json:"sessions"`          // live pages open on this node
	Version  string `json:"version,omitempty"` // the build's VCS revision, if known
}

// healthCache keeps the database check for a second, so a balancer
// polling every node often costs one query a second at most.
type healthCache struct {
	mu sync.Mutex
	at time.Time
	db string
}

func (a *App) serveHealth(w http.ResponseWriter, r *http.Request) {
	h := Health{Status: "ok", DB: a.healthDB(r.Context()), Jobs: "none", Version: buildVersion()}
	if a.jobs != nil {
		h.Jobs = "running"
		if a.cfg.Jobs == JobsEnqueueOnly {
			h.Jobs = "enqueue-only"
		}
		if err := a.startJobs(); err != nil {
			h.Jobs = err.Error()
		}
		h.Leader = a.jobs.IsLeader()
		if h.Jobs == "running" {
			n := a.jobs.Self()
			h.Node = &n
		}
	}
	a.mu.Lock()
	h.Sessions = len(a.sessions)
	a.mu.Unlock()
	h.Watch = "none"
	if a.db != nil && a.db.SQL() != nil {
		h.Watch = a.watches.state()
	}
	h.PubSub = "none"
	if p, ok := a.cfg.PubSub.(*nodePubSub); ok && a.db != nil && a.db.SQL() != nil {
		h.PubSub = p.state()
	}
	switch {
	case a.unready.Load():
		h.Status = "draining"
	case (h.DB != "ok" && h.DB != "none") || (h.Jobs != "running" && h.Jobs != "none" && h.Jobs != "enqueue-only"):
		h.Status = "unavailable"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if h.Status != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(h)
}

// healthDB pings the database, at most once a second.
func (a *App) healthDB(ctx context.Context) string {
	if a.db == nil || a.db.SQL() == nil {
		return "none"
	}
	c := &a.health
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) < time.Second {
		return c.db
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c.db = "ok"
	if err := a.db.SQL().PingContext(ctx); err != nil {
		c.db = err.Error()
	}
	c.at = time.Now()
	return c.db
}

// buildVersion is the VCS revision the binary was built from, if Go
// recorded one.
var buildVersion = sync.OnceValue(func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 12 {
			return s.Value[:12]
		}
	}
	return ""
})
