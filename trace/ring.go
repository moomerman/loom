package trace

import (
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// Filter selects events. Zero fields match everything.
type Filter struct {
	// Name matches an event name exactly, or every name in a group when
	// it ends in a dot or ".*": "job." and "job.*" match job.start and
	// job.finish.
	Name  string
	Trace string // the trace id
	SID   string // the "sid" attribute: one page's events
	// Level is the least level that matches: "debug", "info", "warn" or
	// "error". Empty matches every level.
	Level string
}

// Match reports whether e passes the filter.
func (f Filter) Match(e Event) bool {
	if f.Name != "" {
		group := strings.TrimSuffix(f.Name, "*")
		if strings.HasSuffix(group, ".") {
			if !strings.HasPrefix(e.Name, group) {
				return false
			}
		} else if e.Name != f.Name {
			return false
		}
	}
	if f.Trace != "" && e.Trace != f.Trace {
		return false
	}
	if f.SID != "" && e.String("sid") != f.SID {
		return false
	}
	if f.Level != "" {
		var min slog.Level
		if min.UnmarshalText([]byte(f.Level)) == nil && e.Level < min {
			return false
		}
	}
	return true
}

// Ring keeps the latest events, and passes new ones to subscribers.
type Ring struct {
	mu   sync.Mutex
	buf  []Event
	next int // where the next event goes in buf
	full bool
	subs map[*subscriber]bool
}

type subscriber struct {
	ch chan Event
	f  Filter
}

// NewRing returns a ring keeping the last n events.
func NewRing(n int) *Ring { return &Ring{buf: make([]Event, n), subs: map[*subscriber]bool{}} }

// Emit adds e, dropping the oldest event if the ring is full.
func (r *Ring) Emit(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
	for s := range r.subs {
		if s.f.Match(e) {
			select {
			case s.ch <- e:
			default: // a slow reader loses events, never the emitter time
			}
		}
	}
}

// Since returns the kept events with Seq after seq that match f, oldest
// first.
func (r *Ring) Since(seq uint64, f Filter) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var all []Event
	if r.full {
		all = append(append(all, r.buf[r.next:]...), r.buf[:r.next]...)
	} else {
		all = r.buf[:r.next]
	}
	i := sort.Search(len(all), func(i int) bool { return all[i].Seq > seq })
	out := []Event{}
	for _, e := range all[i:] {
		if f.Match(e) {
			out = append(out, e)
		}
	}
	return out
}

// Subscribe returns a channel of the events added from now on that
// match f, and a func that stops the subscription and closes the
// channel.
func (r *Ring) Subscribe(f Filter) (<-chan Event, func()) {
	s := &subscriber{ch: make(chan Event, 256), f: f}
	r.mu.Lock()
	r.subs[s] = true
	r.mu.Unlock()
	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.subs, s)
			r.mu.Unlock()
			close(s.ch)
		})
	}
}
