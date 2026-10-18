package trace

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
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

// Counters counts events by name and, for the names below, by what
// they concern: page events by route, job events by job type, table
// watches by table. So "route / re-rendered 450 times in the last
// minute" is one line of `loom stats`, not lost in a total.
type Counters struct {
	mu    sync.Mutex
	stats map[statKey]*counter
	keys  map[string]int // distinct keys per name, capped at maxKeys
}

type statKey struct{ name, key string }

type counter struct {
	errors uint64
	timing Timing
}

// maxKeys caps the keys counted per name; later ones count as "other",
// so a route or job type per user can't grow the counters without end.
const maxKeys = 200

// keyAttr is the attribute an event's stats are kept by, if any.
func keyAttr(name string) string {
	switch {
	case strings.HasPrefix(name, "page."), name == LimitRefused:
		return "route"
	case strings.HasPrefix(name, "job."), name == CronTick:
		return "type"
	case name == WatchNotify:
		return "table"
	}
	return ""
}

// Stat is the count of one event name, or of one name and key, since
// the app started, and the timing of those with an "ms" attribute.
type Stat struct {
	Name   string `json:"name"`
	By     string `json:"by,omitempty"`  // the attribute Key is a value of: route, type or table
	Key    string `json:"key,omitempty"` // e.g. the route "/team/{key}"
	Errors uint64 `json:"errors"`        // at level Error
	TimingSummary
}

// NewCounters returns empty counters.
func NewCounters() *Counters {
	return &Counters{stats: map[statKey]*counter{}, keys: map[string]int{}}
}

// Emit counts e.
func (c *Counters) Emit(e Event) {
	k := statKey{name: e.Name}
	if a := keyAttr(e.Name); a != "" {
		k.key = e.String(a)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.stats[k]
	if s == nil {
		if k.key != "" && c.keys[k.name] >= maxKeys {
			k.key = "other"
			s = c.stats[k]
		}
	}
	if s == nil {
		s = &counter{}
		c.stats[k] = s
		if k.key != "" {
			c.keys[k.name]++
		}
	}
	if e.Level >= slog.LevelError {
		s.errors++
	}
	ms, ok := e.Attrs["ms"].(float64)
	s.timing.Add(e.Time, ms, ok)
}

// Stats returns a copy of the counts as of now, sorted by name, then
// key.
func (c *Counters) Stats(now time.Time) []Stat {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Stat, 0, len(c.stats))
	for k, s := range c.stats {
		st := Stat{Name: k.name, Key: k.key, Errors: s.errors, TimingSummary: s.timing.Summary(now)}
		if k.key != "" {
			st.By = keyAttr(k.name)
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Key < out[j].Key
	})
	return out
}
