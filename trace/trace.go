// Package trace is Loom's event stream: one record of what a running app
// did, from a page mount to a job's last attempt, with stable names
// documented in docs/events.md like error codes.
//
// Every part of Loom emits to one Stream: the live runtime, jobs, the
// data layer and auth. The stream numbers each event, keeps the latest
// in a ring for `loom events` and the dev endpoints, counts them for
// `loom stats`, and passes them to its sinks: the app's slog.Logger and
// Config.OnEvent, the bridge to an error tracker or metrics system.
//
// A trace id ties together what one action caused. A page load gets
// one, and so does each browser event (the tab's client id and the
// event's sequence, which a resent event keeps). It travels in the
// context: handlers' database transactions report it with tx.commit,
// jobs.Enqueue stores it with the job, and the job's events and
// broadcasts carry it, so a click, its commit, its job and the
// re-render it causes share one key: `loom trace ID`.
//
// The package is stdlib only and imports nothing from Loom, so the
// runtime, jobs and data can all use it.
package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Event is one thing that happened.
type Event struct {
	Seq   uint64         `json:"seq"`  // increases by one per event, from 1
	Time  time.Time      `json:"time"` // UTC
	Name  string         `json:"name"` // one of Names, e.g. "page.event"
	Level slog.Level     `json:"level"`
	Trace string         `json:"trace,omitempty"` // the action it belongs to
	Attrs map[string]any `json:"attrs,omitempty"` // keys listed in docs/events.md
}

// String returns the attribute key as a string, or "" if it is missing.
func (e Event) String(key string) string {
	v, ok := e.Attrs[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// A Sink receives events. Emit is called synchronously on the goroutine
// that emitted the event, so it must be quick and must not block.
type Sink interface {
	Emit(Event)
}

// SinkFunc adapts a func to a Sink.
type SinkFunc func(Event)

// Emit calls f(e).
func (f SinkFunc) Emit(e Event) { f(e) }

// Multi passes each event to every sink, in order.
func Multi(sinks ...Sink) Sink {
	return SinkFunc(func(e Event) {
		for _, s := range sinks {
			s.Emit(e)
		}
	})
}

// Stream numbers events and passes them to its ring, its counters and
// its sinks. Its methods are safe for concurrent use, and a nil *Stream
// discards everything, so code that may run without one needn't check.
type Stream struct {
	seq      atomic.Uint64
	ring     *Ring
	counters *Counters

	mu    sync.RWMutex
	sinks []Sink
	now   func() time.Time
}

// RingSize is how many events a stream keeps for Since.
const RingSize = 2000

// NewStream returns a stream with the given sinks, keeping the last
// RingSize events.
func NewStream(sinks ...Sink) *Stream {
	return &Stream{ring: NewRing(RingSize), counters: NewCounters(), sinks: sinks, now: time.Now}
}

// AddSink adds a sink, which receives events emitted from now on.
func (s *Stream) AddSink(sink Sink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sinks = append(s.sinks, sink)
}

// Emit records an event. Its level is the one docs/events.md gives the
// name (Level); its trace is ctx's (ID), unless attrs set "trace".
// attrs are alternating keys and values, like slog's:
//
//	stream.Emit(ctx, trace.TxCommit, "ms", 3.2, "statements", 2)
//
// Values that are errors become their message; durations become
// milliseconds.
func (s *Stream) Emit(ctx context.Context, name string, attrs ...any) {
	if s == nil {
		return
	}
	s.EmitLevel(ctx, Level(name), name, attrs...)
}

// EmitLevel is Emit with a level other than the name's, for an event
// whose severity depends on what happened (a job that failed).
func (s *Stream) EmitLevel(ctx context.Context, level slog.Level, name string, attrs ...any) {
	if s == nil {
		return
	}
	e := Event{Name: name, Level: level, Trace: ID(ctx)}
	if len(attrs) > 0 {
		e.Attrs = make(map[string]any, len(attrs)/2)
		for i := 0; i+1 < len(attrs); i += 2 {
			k, _ := attrs[i].(string)
			if k == "" {
				continue
			}
			switch v := attrs[i+1].(type) {
			case nil:
				continue
			case error:
				e.Attrs[k] = v.Error()
			case time.Duration:
				e.Attrs[k] = Millis(v)
			case fmt.Stringer:
				e.Attrs[k] = v.String()
			default:
				e.Attrs[k] = v
			}
		}
		if t, ok := e.Attrs["trace"].(string); ok {
			e.Trace = t
			delete(e.Attrs, "trace")
		}
		if e.Trace == "" {
			delete(e.Attrs, "trace")
		}
	}
	s.mu.RLock()
	e.Time = s.now().UTC()
	e.Seq = s.seq.Add(1)
	sinks := s.sinks
	s.mu.RUnlock()
	s.ring.Emit(e)
	s.counters.Emit(e)
	for _, sink := range sinks {
		sink.Emit(e)
	}
}

// Millis is d in milliseconds, to a tenth: the unit of every "ms"
// attribute.
func Millis(d time.Duration) float64 {
	return float64(d.Round(100*time.Microsecond)) / float64(time.Millisecond)
}

// Since returns the kept events after seq, oldest first, that match f.
func (s *Stream) Since(seq uint64, f Filter) []Event {
	if s == nil {
		return nil
	}
	return s.ring.Since(seq, f)
}

// Last returns the sequence number of the latest event: a cursor for
// Since that skips everything so far.
func (s *Stream) Last() uint64 {
	if s == nil {
		return 0
	}
	return s.seq.Load()
}

// Subscribe returns a channel of the events emitted from now on that
// match f, and a func to stop. Events a slow reader leaves in the full
// channel are dropped for it, never blocking the emitter.
func (s *Stream) Subscribe(f Filter) (<-chan Event, func()) {
	return s.ring.Subscribe(f)
}

// Stats returns the counts and timings of every event name emitted so
// far, sorted by name.
func (s *Stream) Stats() []Stat {
	if s == nil {
		return nil
	}
	return s.counters.Stats()
}

// SetClock replaces the stream's clock, for tests.
func (s *Stream) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now == nil {
		now = time.Now
	}
	s.now = now
}

type traceKey struct{}

// WithTrace returns ctx carrying the trace id.
func WithTrace(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, traceKey{}, id)
}

// ID returns the trace id ctx carries, or "".
func ID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(traceKey{}).(string)
	return id
}

// NewID returns a new random trace id.
func NewID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
