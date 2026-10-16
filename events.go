package loom

import (
	"context"
	"errors"
	"log/slog"
	"maps"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/storage"
	"github.com/moomerman/loom/trace"
)

// The event stream (docs/events.md): the runtime, jobs, the data layer
// and auth emit named events to the app's trace.Stream, which logs them
// to Config.Logger, passes them to Config.OnEvent, and keeps the latest
// for `loom events`, `loom trace` and `loom stats`.

// Emit records an event on the app's stream, with its trace taken from
// ctx (a page's c.Context(), a job's ctx). attrs alternate keys and
// values, like slog's. Loom's own names are in docs/events.md; a
// sub-app's events use names of their own, prefixed with its name.
//
//	app.Emit(c.Context(), "billing.charge_failed", "user", userID, "code", code)
func (a *App) Emit(ctx context.Context, name string, attrs ...any) {
	a.stream.Emit(ctx, name, attrs...)
}

// Stream returns the app's event stream: the latest events, a live
// subscription, and counts per name.
func (a *App) Stream() *trace.Stream { return a.stream }

// newStream makes the app's stream: logged to cfg.Logger and passed to
// cfg.OnEvent.
func newStream(cfg Config) *trace.Stream {
	s := trace.NewStream(logSink(cfg.Logger))
	if cfg.OnEvent != nil {
		s.AddSink(trace.SinkFunc(cfg.OnEvent))
	}
	return s
}

// logSink logs events with trace.Slog, showing only the start of a
// session id: enough to tell sessions apart in a log, too little to name
// one. The whole id is in the stream, which only dev serves.
func logSink(l *slog.Logger) trace.Sink {
	log := trace.Slog(l)
	return trace.SinkFunc(func(e trace.Event) {
		if !l.Enabled(context.Background(), e.Level) {
			return
		}
		if sid := e.String("sid"); len(sid) > 8 {
			e.Attrs = maps.Clone(e.Attrs)
			e.Attrs["sid"] = shortSID(sid)
		}
		log.Emit(e)
	})
}

// observe connects the parts of Loom that live outside the root package
// to the app's stream: the data layer and the job runner.
func (a *App) observe(db *DB) {
	if db != nil {
		db.Observe(a.stream, a.cfg.SlowQuery)
	}
}

// reportError emits the error event, which `loom errors` lists: its
// code, location and fix as attributes, and the message on its own.
// attrs add context, such as the route.
func (a *App) reportError(ctx context.Context, sid string, err error, attrs ...any) {
	info := errorInfo(sid, err)
	a.stream.Emit(ctx, trace.Error, append([]any{"msg", info.Message, "code", info.Code, "where", info.Where, "fix", info.Fix, "sid", sid}, attrs...)...)
}

// recordError keeps each error event for `loom errors` and the
// recent_errors MCP tool, whichever part of Loom emitted it: `loom
// errors` is the stream's error events.
func (a *App) recordError(e trace.Event) {
	if e.Name != trace.Error {
		return
	}
	source := e.String("source")
	if source == "" {
		source = "server"
	}
	a.errors.record(ErrorInfo{Time: e.Time, Source: source, SID: e.String("sid"), URL: e.String("url"), Code: e.String("code"),
		Message: e.String("msg"), Where: e.String("where"), Fix: e.String("fix"), Route: e.String("route"), Trace: e.Trace})
}

// errorInfo is err as `loom errors` lists it: a Loom, jobs or data error
// keeps its code and fix.
func errorInfo(sid string, err error) ErrorInfo {
	info := ErrorInfo{Source: "server", SID: sid, Message: err.Error()}
	var e *Error
	var je *jobs.Error
	var de *data.Error
	var se *storage.Error
	switch {
	case errors.As(err, &e):
		info.Code, info.Message, info.Where, info.Fix = e.Code, e.Msg, e.Where, e.Fix
	case errors.As(err, &je):
		info.Code, info.Message, info.Where, info.Fix = je.Code, je.Msg, je.Where, je.Fix
	case errors.As(err, &de):
		info.Code, info.Message, info.Fix = de.Code, de.Msg, de.Fix
	case errors.As(err, &se):
		info.Code, info.Message, info.Fix = se.Code, se.Msg, se.Fix
	}
	return info
}
