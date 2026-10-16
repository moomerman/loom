package trace_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom/trace"
)

// Every name the code defines is documented in docs/events.md, and
// every documented name is defined, as for error codes.
func TestNamesDocumented(t *testing.T) {
	doc, err := os.ReadFile("../docs/events.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^### ([a-z_.]+)$`).FindAllStringSubmatch(string(doc), -1) {
		documented[m[1]] = true
	}
	names := trace.Names()
	for _, n := range names {
		if !documented[n] {
			t.Errorf("event %s is not documented in docs/events.md", n)
		}
	}
	for n := range documented {
		if !slices.Contains(names, n) {
			t.Errorf("docs/events.md documents %s, which trace.Names doesn't have", n)
		}
	}
}

func TestEmit(t *testing.T) {
	s := trace.NewStream()
	ctx := trace.WithTrace(context.Background(), "t1")
	s.Emit(ctx, trace.TxCommit, "ms", 1500*time.Microsecond, "err", errors.New("boom"), "skip", nil, "statements", 2)
	s.Emit(ctx, trace.JobStart, "trace", "t2") // an attribute overrides the context's trace
	s.Emit(context.Background(), "billing.charged")
	got := s.Since(0, trace.Filter{})
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	e := got[0]
	if e.Seq != 1 || e.Name != trace.TxCommit || e.Level != slog.LevelDebug || e.Trace != "t1" {
		t.Errorf("event = %+v", e)
	}
	if e.Attrs["ms"] != 1.5 || e.Attrs["err"] != "boom" || e.Attrs["statements"] != 2 || len(e.Attrs) != 3 {
		t.Errorf("attrs = %v, want ms 1.5, err as text, no nil value", e.Attrs)
	}
	if got[1].Trace != "t2" || got[1].Attrs["trace"] != nil {
		t.Errorf("trace attr: %+v", got[1])
	}
	if got[2].Level != slog.LevelInfo || got[2].Trace != "" {
		t.Errorf("an app's own name: %+v", got[2])
	}
	var none *trace.Stream
	none.Emit(ctx, trace.Error) // a nil stream discards
}

// The ring keeps the last events; Since pages through them by sequence
// and filters by name, group, trace, sid and level.
func TestRing(t *testing.T) {
	r := trace.NewRing(3)
	for i, n := range []string{"page.event", "job.start", "job.finish", "error", "page.event"} {
		e := trace.Event{Seq: uint64(i + 1), Name: n, Level: trace.Level(n), Attrs: map[string]any{"sid": "s1"}}
		if i == 4 {
			e.Trace, e.Attrs["sid"] = "x", "s2"
		}
		r.Emit(e)
	}
	seqs := func(f trace.Filter, since uint64) []uint64 {
		var out []uint64
		for _, e := range r.Since(since, f) {
			out = append(out, e.Seq)
		}
		return out
	}
	for _, tc := range []struct {
		f     trace.Filter
		since uint64
		want  []uint64
	}{
		{trace.Filter{}, 0, []uint64{3, 4, 5}}, // the first two fell out
		{trace.Filter{}, 4, []uint64{5}},
		{trace.Filter{Name: "job.*"}, 0, []uint64{3}},
		{trace.Filter{Name: "job."}, 0, []uint64{3}},
		{trace.Filter{Name: "page.event"}, 0, []uint64{5}},
		{trace.Filter{Trace: "x"}, 0, []uint64{5}},
		{trace.Filter{SID: "s1"}, 0, []uint64{3, 4}},
		{trace.Filter{Level: "error"}, 0, []uint64{4}},
	} {
		if got := seqs(tc.f, tc.since); !slices.Equal(got, tc.want) {
			t.Errorf("%+v since %d: got %v, want %v", tc.f, tc.since, got, tc.want)
		}
	}
}

func TestSubscribe(t *testing.T) {
	s := trace.NewStream()
	ch, stop := s.Subscribe(trace.Filter{Name: "job.*"})
	s.Emit(context.Background(), trace.TxCommit)
	s.Emit(context.Background(), trace.JobStart, "id", 1)
	if e := <-ch; e.Name != trace.JobStart {
		t.Errorf("got %s, want job.start", e.Name)
	}
	stop()
	if _, ok := <-ch; ok {
		t.Error("the channel is open after stop")
	}
	stop() // twice is fine
}

// The slog sink logs the msg attribute as the message, the rest as
// attributes, and leaves out levels the logger doesn't enable.
func TestSlog(t *testing.T) {
	var buf bytes.Buffer
	s := trace.NewStream(trace.Slog(slog.New(slog.NewTextHandler(&buf, nil))))
	ctx := trace.WithTrace(context.Background(), "t1")
	s.Emit(ctx, trace.TxCommit, "ms", 1.0) // Debug: left out
	s.Emit(ctx, trace.Error, "msg", "the page failed", "code", "RT402", "fix", "see the stack")
	out := buf.String()
	if strings.Contains(out, "tx.commit") {
		t.Errorf("a Debug event was logged at Info:\n%s", out)
	}
	for _, want := range []string{`level=ERROR msg="the page failed"`, "event=error", "code=RT402", `fix="see the stack"`, "trace=t1"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q lacks %q", out, want)
		}
	}
}

func TestStats(t *testing.T) {
	s := trace.NewStream()
	for _, ms := range []float64{1, 3} {
		s.Emit(context.Background(), trace.PageEvent, "ms", ms)
	}
	s.Emit(context.Background(), trace.Error)
	got := s.Stats()
	if len(got) != 2 || got[0].Name != "error" || got[0].Errors != 1 || got[1].Count != 2 || got[1].MeanMS != 2 || got[1].MaxMS != 3 {
		t.Errorf("stats = %+v", got)
	}
}
