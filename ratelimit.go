package loom

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moomerman/loom/trace"
)

// Rate limits (docs/live.md). A limit caps how often one key (an IP
// address, a user, a payload field) may load a route's pages, send its
// events or call its handlers:
//
//	app.Group(loom.RateLimit(loom.Limit{Key: loom.ByIP, Max: 120, Per: time.Minute})).Mount("/auth", auth.New(cfg))
//	g.Live("/sign-in", &SignInPage{}, SignIn,
//		loom.RateLimit(loom.Limit{Key: loom.ByPayload("email"), Max: 5, Per: time.Minute, On: []string{"sign_in"}}))
//
// It isn't middleware because events don't pass through middleware: the
// limit is checked where auth is, before Mount, before each event's
// handler and before a Handle handler. An attempt over the limit is
// refused with RT436 and Retry-After, and nothing runs.
//
// The count is a sliding window: this window's attempts plus the last
// window's, weighted by how much of it still overlaps. Refused attempts
// count too, so hammering keeps a key out. With a database the counts
// are kept in loom_rate_limits (created on first use), so a limit holds
// across nodes; without one, in memory.

// Limit is one rate limit: at most Max attempts per Per for each value of
// Key. With On, it counts only those events of the route; without, it
// counts every page load, event and handler request.
type Limit struct {
	Key LimitKey      // whose attempts are counted: ByIP, ByUser, ByPayload or KeyBy
	Max int           // attempts allowed in any window of Per
	Per time.Duration // the window, e.g. time.Minute
	On  []string      // the events counted; none means everything
}

// LimitKey says whose attempts a Limit counts. An attempt whose key is ""
// (no one signed in for ByUser, no such field for ByPayload) isn't
// counted.
type LimitKey struct {
	name string
	fn   func(c *Ctx, event string, payload Payload) string
}

// ByIP counts attempts per client IP address: the connection's, or the
// one a trusted proxy forwarded (Config.TrustedProxies).
var ByIP = LimitKey{name: "ip", fn: func(c *Ctx, _ string, _ Payload) string { return clientIP(c.Request) }}

// ByUser counts attempts per signed-in user. Attempts with no one signed
// in aren't counted.
var ByUser = LimitKey{name: "user", fn: func(c *Ctx, _ string, _ Payload) string { return c.UserID() }}

// ByPayload counts attempts per value of an event's payload field,
// trimmed and lowercased, e.g. ByPayload("email"). Page loads, handler
// requests and events without the field aren't counted.
func ByPayload(field string) LimitKey {
	return LimitKey{name: field, fn: func(_ *Ctx, _ string, p Payload) string {
		return strings.ToLower(strings.TrimSpace(p[field]))
	}}
}

// KeyBy counts attempts per the value fn returns; name says what that is
// in the manifest ("5 per minute by account"). For a page load or
// handler request, event is "" and payload nil.
func KeyBy(name string, fn func(c *Ctx, event string, payload Payload) string) LimitKey {
	return LimitKey{name: name, fn: fn}
}

// RateLimit limits a group's routes (as an Option) or one route (as a
// RouteOption to Live). A group's limits count across all its routes and
// apply to its handlers; checks accumulate outer to inner, like Auth. An
// invalid limit panics with RT437.
func RateLimit(limits ...Limit) rateLimitOption { return rateLimitOption(limits) }

type rateLimitOption []Limit

func (o rateLimitOption) apply(g *Group) {
	for _, l := range o {
		g.limits = append(g.limits, g.app.newLimit(l, false))
	}
}

func (o rateLimitOption) applyRoute(rt *route) {
	for _, l := range o {
		rt.limits = append(rt.limits, rt.group.app.newLimit(l, true))
	}
}

// limit is a registered Limit.
type limit struct {
	Limit
	seq   int    // names the limit's counts: the order it was registered in
	route bool   // given to one route, so On names its events
	where string // where it was registered, for RT437
}

func (a *App) newLimit(l Limit, route bool) *limit {
	where := callerOutsideLoom()
	bad := func(msg, fix string) {
		panic(&Error{Code: RT437, Msg: "rate limit " + msg, Where: where, Fix: fix})
	}
	switch {
	case l.Key.fn == nil:
		bad("has no Key", "set Key: loom.ByIP, loom.ByUser, loom.ByPayload(\"field\") or loom.KeyBy(name, func)")
	case l.Max < 1:
		bad(fmt.Sprintf("allows %d attempts", l.Max), "set Max to the attempts allowed per Per, at least 1")
	case l.Per <= 0:
		bad(fmt.Sprintf("has a window of %s", l.Per), "set Per to the window, e.g. time.Minute")
	case slices.Contains(l.On, ""):
		bad("counts an event named \"\"", "name the events in On, or leave On empty to count everything")
	}
	a.limitSeq++
	return &limit{Limit: l, seq: a.limitSeq, route: route, where: where}
}

// checkLimitEvents panics with RT437 if a route's own limit counts an
// event the page doesn't handle: a typo would leave it unlimited.
func checkLimitEvents(rt *route, events func(string) bool) {
	for _, l := range rt.limits {
		if !l.route {
			continue
		}
		for _, ev := range l.On {
			if !events(ev) {
				panic(&Error{Code: RT437, Where: l.where, Msg: fmt.Sprintf("rate limit on %s counts event %q, which %T doesn't handle, so it would never count anything", rt.path, ev, rt.prototype),
					Fix: "name events from the page's Events() in On"})
			}
		}
	}
}

// limitChain returns the limits of g and its parents, outer first.
func (g *Group) limitChain() []*limit {
	var out []*limit
	for x := g; x != nil; x = x.parent {
		out = append(slices.Clone(x.limits), out...)
	}
	return out
}

// String describes the limit for the manifest and `loom routes`, e.g.
// "5 per minute by email on sign_in".
func (l *limit) String() string {
	s := fmt.Sprintf("%d per %s by %s", l.Max, per(l.Per), l.Key.name)
	if len(l.On) > 0 {
		s += " on " + strings.Join(l.On, ", ")
	}
	return s
}

func per(d time.Duration) string {
	switch d {
	case time.Second:
		return "second"
	case time.Minute:
		return "minute"
	case time.Hour:
		return "hour"
	case 24 * time.Hour:
		return "day"
	}
	return d.String()
}

func limitNames(limits []*limit) []string {
	names := []string{}
	for _, l := range limits {
		names = append(names, l.String())
	}
	return names
}

// LimitError is RT436: an attempt over a rate limit. Nothing ran.
type LimitError struct {
	Err        *Error
	RetryAfter time.Duration // when the next attempt will be allowed
}

func (e *LimitError) Error() string { return e.Err.Error() }
func (e *LimitError) Unwrap() error { return e.Err }

// refuseLimited answers a page load or handler request over a rate
// limit: 429 with Retry-After, and the error page.
func (a *App) refuseLimited(w http.ResponseWriter, r *http.Request, g *Group, e *LimitError) {
	w.Header().Set("Retry-After", e.retryAfter())
	a.serveError(w, r, g, http.StatusTooManyRequests, e.Err)
}

// retryAfter is the Retry-After header value for e: whole seconds, at
// least 1.
func (e *LimitError) retryAfter() string {
	return strconv.Itoa(max(1, int(e.RetryAfter/time.Second)))
}

// allow counts an attempt against each of limits that applies to event
// ("" for a page load or handler request) and returns a *LimitError if
// any is over. A store that fails is logged and the attempt allowed:
// the limiter shouldn't take the app down with its database.
func (a *App) allow(c *Ctx, limits []*limit, path, event string, payload Payload) error {
	if len(limits) == 0 {
		return nil
	}
	now := a.limitNow()
	var over *LimitError
	for _, l := range limits {
		if len(l.On) > 0 && !slices.Contains(l.On, event) {
			continue
		}
		key := l.Key.fn(c, event, payload)
		if key == "" {
			continue
		}
		wait, err := a.limiter.hit(c.Context(), a, l, key, now)
		if err != nil {
			a.logError("", fmt.Errorf("rate limit %s: %w", l, err))
			continue
		}
		if wait > 0 {
			a.stream.Emit(c.Context(), trace.LimitRefused, "route", path, "event", event, "limit", l.String(), "retry_after", wait)
		}
		if wait > 0 && (over == nil || wait > over.RetryAfter) {
			what := "loading " + path
			if event != "" {
				what = fmt.Sprintf("event %q on %s", event, path)
			}
			over = &LimitError{RetryAfter: wait, Err: &Error{Code: RT436, Msg: fmt.Sprintf("too many attempts: %s is over its rate limit of %s, so it was refused", what, l),
				Fix: fmt.Sprintf("wait %s; in a test, advance loomtest.LimitClock", wait.Round(time.Second))}}
		}
	}
	if over != nil {
		return over
	}
	return nil
}

// SetLimitClock sets the clock rate limits count by, instead of
// time.Now. It is for tests: loomtest.LimitClock sets a fake one.
func (a *App) SetLimitClock(now func() time.Time) {
	a.limiter.mu.Lock()
	defer a.limiter.mu.Unlock()
	a.limiter.now = now
}

func (a *App) limitNow() time.Time {
	a.limiter.mu.Lock()
	now := a.limiter.now
	a.limiter.mu.Unlock()
	if now == nil {
		return time.Now()
	}
	return now()
}

// limiter keeps the counts: loom_rate_limits with a database, else memory.
type limiter struct {
	mu     sync.Mutex
	ready  bool
	stored bool
	mem    map[string]*window
	pruned time.Time
	now    func() time.Time // SetLimitClock's clock; nil for time.Now
}

// window is a key's counts in memory: this window's and the last's.
type window struct {
	n         int64 // the window's number: unix nanoseconds / Per
	cur, prev int
	expires   time.Time
}

func (lm *limiter) init(ctx context.Context, a *App) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.ready {
		return nil
	}
	if db := a.DB(); db.SQL() != nil || db.IsInTx() {
		lm.stored = true // loom_rate_limits, one of Loom's migrations
	} else {
		lm.mem = map[string]*window{}
	}
	lm.ready = true
	return nil
}

// hit counts one attempt by key at now and returns how long until the
// next would be allowed: 0 if this one is.
func (lm *limiter) hit(ctx context.Context, a *App, l *limit, key string, now time.Time) (time.Duration, error) {
	if err := lm.init(ctx, a); err != nil {
		return 0, err
	}
	n := now.UnixNano() / int64(l.Per)
	start := time.Unix(0, n*int64(l.Per))
	expires := start.Add(2 * l.Per)
	k := strconv.Itoa(l.seq) + "|" + key
	var cur, prev int
	if lm.stored {
		err := a.DB().QueryRow(ctx, `WITH cur AS (
	INSERT INTO loom_rate_limits (key, bucket, hits, expires_at) VALUES ($1, $2, 1, $3)
	ON CONFLICT (key, bucket) DO UPDATE SET hits = loom_rate_limits.hits + 1
	RETURNING hits)
SELECT (SELECT hits FROM cur), coalesce((SELECT hits FROM loom_rate_limits WHERE key = $1 AND bucket = $2 - 1), 0)`, k, n, expires).Scan(&cur, &prev)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		if err != nil {
			return 0, err
		}
	} else {
		lm.mu.Lock()
		w := lm.mem[k]
		switch {
		case w == nil:
			w = &window{n: n}
			lm.mem[k] = w
		case w.n == n-1:
			w.n, w.prev, w.cur = n, w.cur, 0
		case w.n != n:
			w.n, w.prev, w.cur = n, 0, 0
		}
		w.cur++
		w.expires = expires
		cur, prev = w.cur, w.prev
		lm.mu.Unlock()
	}
	return ceilSecond(wait(l, cur, prev, now.Sub(start))), nil
}

// ceilSecond rounds d up to whole seconds, as Retry-After says it, so
// retrying after it is never a moment too soon.
func ceilSecond(d time.Duration) time.Duration {
	if r := d % time.Second; r != 0 {
		d += time.Second - r
	}
	return d
}

// wait is how long after elapsed (into the current window) until an
// attempt would be allowed, given cur attempts in this window (this one
// included) and prev in the last; 0 if this attempt is allowed.
func wait(l *limit, cur, prev int, elapsed time.Duration) time.Duration {
	f := float64(elapsed) / float64(l.Per)
	if float64(prev)*(1-f)+float64(cur) <= float64(l.Max)+1e-9 { // allow for rounding
		return 0
	}
	// The next attempt makes cur+1: allowed once the last window's weight
	// falls to max-cur-1, in this window if it can, else in the next one,
	// where this window's cur is the last's.
	room := float64(l.Max - cur - 1)
	if room >= 0 && prev > 0 {
		at := 1 - room/float64(prev) // the fraction of this window when it's allowed
		return time.Duration(at*float64(l.Per)) - elapsed
	}
	next := max(0, 1-float64(l.Max-1)/float64(cur))
	return l.Per - elapsed + time.Duration(next*float64(l.Per))
}

// prune drops expired counts, at most once a minute. reap calls it.
func (lm *limiter) prune(a *App, now time.Time) {
	lm.mu.Lock()
	if !lm.ready || now.Sub(lm.pruned) < time.Minute {
		lm.mu.Unlock()
		return
	}
	lm.pruned = now
	stored := lm.stored
	for k, w := range lm.mem {
		if w.expires.Before(now) {
			delete(lm.mem, k)
		}
	}
	lm.mu.Unlock()
	if stored {
		if _, err := a.DB().Exec(context.Background(), `DELETE FROM loom_rate_limits WHERE expires_at < $1`, now); err != nil {
			a.logError("", fmt.Errorf("pruning loom_rate_limits: %w", err))
		}
	}
}

// clientIP is r's client address without the port: the connection's, or
// the forwarded one trustForwarded put in RemoteAddr.
func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
