package loom

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/moomerman/loom/data"
)

// Error is a runtime error in a live page. Like compile errors, it says
// what went wrong, where, and how to fix it, and has a stable code
// documented in docs/errors.md.
type Error struct {
	Code  string // e.g. "RT402"
	Msg   string
	Where string // file:line of the user code involved, if known
	Fix   string
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "error %s: %s", e.Code, e.Msg)
	if e.Where != "" {
		fmt.Fprintf(&b, "\n  at %s", e.Where)
	}
	if e.Fix != "" {
		fmt.Fprintf(&b, "\n  fix: %s", e.Fix)
	}
	return b.String()
}

// Runtime error codes.
const (
	RT401 = "RT401" // a page's assigns don't fit its component
	RT402 = "RT402" // Mount, an event handler, HandleInfo, Assigns or rendering panicked
	RT403 = "RT403" // a list has duplicate :key values
	RT404 = "RT404" // unknown or expired session
	RT405 = "RT405" // malformed event request
	RT406 = "RT406" // invalid page passed to App.Live
	RT407 = "RT407" // too many events waiting for a page
	RT408 = "RT408" // dev tools: unknown component
	RT409 = "RT409" // dev tools: assigns JSON doesn't fit the component
	RT410 = "RT410" // a page subscribed to a topic but has no HandleInfo
	RT411 = "RT411" // App.UseDB called too late
	RT412 = "RT412" // a layout's assigns func doesn't return its assigns type
	RT413 = "RT413" // a component can't be used as that kind of layout
	RT414 = "RT414" // no live route matches a path (App.NewView, loomtest.Mount)
	RT415 = "RT415" // a sub-app that requires auth is mounted without loom.Auth
	RT416 = "RT416" // invalid mount prefix, route path or assets
	RT417 = "RT417" // dev, browser: element does not match a behavior's requires
	RT418 = "RT418" // dev, browser: behavior emitted an undeclared event
	RT419 = "RT419" // a page reported an error with Ctx.Fail
	RT420 = "RT420" // dev, browser: an uncaught error or console.error on a page
	RT421 = "RT421" // loomtest: generated *_hx.go files are out of date with their templates
	RT422 = "RT422" // an event the page's markup sends has no handler in its Events
	RT423 = "RT423" // a page got an event it has no handler for
	RT424 = "RT424" // the server is restarting, or can't tell whether an event ran; the browser resends it
	RT425 = "RT425" // c.SendAfter names an event the page has no handler for
	RT426 = "RT426" // Ctx.Patch to a URL that isn't of the page's route
	RT427 = "RT427" // Ctx.Patch on a page with no HandleParams
	RT428 = "RT428" // a page's HandleParams has the wrong signature
	RT429 = "RT429" // a browser sent a POST (or other unsafe request) from another site
	RT430 = "RT430" // loom.User with no one signed in, or signing in an empty user id
	RT431 = "RT431" // loom.User's type isn't the one loom.Users registered, or there is no loom.Users
	RT432 = "RT432" // the sign-in session store failed
	RT433 = "RT433" // a mounted sub-app's migrations aren't installed and applied
	RT434 = "RT434" // a page and its live layout both handle an event
	RT435 = "RT435" // LayoutOf asked for a live layout the page doesn't have
	RT436 = "RT436" // too many attempts: over a rate limit (loom.RateLimit)
	RT437 = "RT437" // an invalid rate limit
	RT438 = "RT438" // an event no element on the page binds with on:*
	RT439 = "RT439" // LOOM_DEV=1 without LOOM_DEV_TOKEN: dev mode outside loom dev
	RT440 = "RT440" // dev mode on an address other machines can reach
	RT441 = "RT441" // an invalid loom.Config value
	RT442 = "RT442" // BaseURL is https but a request came over plain http from an untrusted sender
	RT443 = "RT443" // LOOM_ADDR set outside dev mode
	RT444 = "RT444" // too many live pages open: Config.MaxSessions
	RT445 = "RT445" // an event handler ran for a long time, holding its page
	RT446 = "RT446" // app.ErrorPage was given the wrong component or assigns func
	RT447 = "RT447" // a broadcast too large for Postgres NOTIFY reached only this node
	RT448 = "RT448" // c.NotFound called after Mount, where it can't answer 404
	RT449 = "RT449" // c.Meta changed after the page loaded, where nothing reads it
	RT450 = "RT450" // c.Meta's StructuredData can't be encoded as JSON
	RT451 = "RT451" // loom dev can't tell which main package to run
)

// panicError converts a recovered panic into an RT402 error, locating the
// innermost frame outside Loom and the Go runtime. A panic with a Loom
// error passes through as is, so its own fix isn't buried under RT402's.
func panicError(what string, p any) *Error {
	if e, ok := p.(*Error); ok {
		return e // already a Loom error, with its own code and fix
	}
	return &Error{
		Code:  RT402,
		Msg:   fmt.Sprintf("%s panicked: %v", what, p),
		Where: callerOutsideLoom(),
		Fix:   "fix the panic at the location shown; the page was restarted with a fresh Mount",
	}
}

// failError converts an error a page reported with Ctx.Fail into an
// *Error: RT419, unless it already carries a code of its own.
func failError(err error, where string) *Error {
	var e *Error
	if errors.As(err, &e) {
		c := *e
		if c.Where == "" {
			c.Where = where
		}
		return &c
	}
	var de *data.Error
	if errors.As(err, &de) {
		return &Error{Code: de.Code, Msg: de.Msg, Where: where, Fix: de.Fix}
	}
	return &Error{
		Code:  RT419,
		Msg:   err.Error(),
		Where: where,
		Fix:   "the page kept running with the assigns it returned; fix the cause, and show the user what happened in those assigns",
	}
}

func callerOutsideLoom() string {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if !isLoomOrRuntime(f.Function) || strings.HasSuffix(f.File, "_test.go") {
			return fmt.Sprintf("%s:%d (%s)", f.File, f.Line, f.Function)
		}
		if !more {
			return ""
		}
	}
}

func isLoomOrRuntime(fn string) bool {
	for _, prefix := range []string{"runtime.", "github.com/moomerman/loom.", "github.com/moomerman/loom/render.", "github.com/moomerman/loom/diff.", "github.com/moomerman/loom/jobs.", "github.com/moomerman/loom/pubsub."} {
		if strings.HasPrefix(fn, prefix) {
			return true
		}
	}
	return false
}
