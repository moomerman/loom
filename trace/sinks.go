package trace

import (
	"context"
	"log/slog"
	"sort"
	"sync"
)

// Slog writes events to a logger: the message is the event's "msg"
// attribute, or its name, and the other attributes follow, with the
// event's name as "event" and its trace as "trace". Levels the logger
// doesn't enable cost nothing more, so the default Info logger leaves
// out the Debug events a page emits on every click.
func Slog(l *slog.Logger) Sink {
	return SinkFunc(func(e Event) {
		ctx := context.Background()
		if !l.Enabled(ctx, e.Level) {
			return
		}
		msg := e.String("msg")
		if msg == "" {
			msg = e.Name
		}
		attrs := make([]slog.Attr, 0, len(e.Attrs)+2)
		if msg != e.Name {
			attrs = append(attrs, slog.String("event", e.Name))
		}
		keys := make([]string, 0, len(e.Attrs))
		for k := range e.Attrs {
			if k != "msg" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			attrs = append(attrs, slog.Any(k, e.Attrs[k]))
		}
		if e.Trace != "" {
			attrs = append(attrs, slog.String("trace", e.Trace))
		}
		l.LogAttrs(ctx, e.Level, msg, attrs...)
	})
}

// Counters counts events by name, and totals their "ms" attributes.
type Counters struct {
	mu    sync.Mutex
	stats map[string]*Stat
}

// Stat is the count of one event name since the app started, and the
// timing of those with an "ms" attribute.
type Stat struct {
	Name   string  `json:"name"`
	Count  uint64  `json:"count"`
	Errors uint64  `json:"errors"`            // at level Error
	Timed  uint64  `json:"timed,omitempty"`   // how many had "ms"
	MeanMS float64 `json:"mean_ms,omitempty"` // over Timed
	MaxMS  float64 `json:"max_ms,omitempty"`
	totMS  float64
}

// NewCounters returns empty counters.
func NewCounters() *Counters { return &Counters{stats: map[string]*Stat{}} }

// Emit counts e.
func (c *Counters) Emit(e Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.stats[e.Name]
	if s == nil {
		s = &Stat{Name: e.Name}
		c.stats[e.Name] = s
	}
	s.Count++
	if e.Level >= slog.LevelError {
		s.Errors++
	}
	if ms, ok := e.Attrs["ms"].(float64); ok {
		s.Timed++
		s.totMS += ms
		s.MaxMS = max(s.MaxMS, ms)
	}
}

// Stats returns a copy of the counts, sorted by name.
func (c *Counters) Stats() []Stat {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Stat, 0, len(c.stats))
	for _, s := range c.stats {
		st := *s
		if st.Timed > 0 {
			st.MeanMS = float64(int64(st.totMS/float64(st.Timed)*10)) / 10
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
