package loom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"html"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moomerman/loom/diff"
	"github.com/moomerman/loom/render"
	"github.com/moomerman/loom/trace"
)

// View is one live page instance: a Page, its template and app layout,
// and its latest render. The runtime gives each page load its own View,
// owned by one goroutine; loomtest drives Views directly. A View is not
// safe for concurrent use.
type View struct {
	page    *boundPage
	live    *boundPage // the live layout's state, or nil (see LiveLayout)
	route   *route     // nil for a View no app routes
	tmpl    render.Template
	layout  *layout // the app layout, or nil
	root    *layout // the root layout, or nil to render the live root alone
	ctx     *Ctx
	base    context.Context  // the page's context, without a trace
	tally   trace.Tally      // the statements of the current action, in base (Tally)
	trace   string           // the trace id of the latest action (Trace)
	next    string           // the trace id the next action takes, if set (setTrace)
	auth    []AuthFunc       // checked before Mount and every event
	watch   []string         // the tables the route watches
	scope   *render.Scope    // the mount prefix and assets
	cancel  func()           // cancels ctx.Context()
	tree    *render.Rendered // the live tree: the app layout around the page
	assigns Assigns          // the page's assigns of the current render
	lassign Assigns          // the live layout's assigns of the current render
	version int
	title   string // the title as of the current version

	now      func() time.Time         // the timers' clock; nil for time.Now (SetClock)
	timers   map[string]*pendingTimer // by event (timer.go)
	timerSeq int
	patched  *patch // the URL Ctx.Patch changed to, until the runtime looks

	boundTree *render.Rendered // the tree bound was read from
	bound     map[string]bool  // events the tree binds with on:*, for RT438

	lent bool // its live layout went to the next page (lend); it does nothing more

	browser   string // the browser cookie's hash, which kept state is sealed for
	kept      string // the kept state, sealed, as the browser holds it (keep.go)
	keptPlain []byte // what kept seals, to reseal only on a change
	restored  string // what the mount did with the kept state it was given (restore)
}

// NewView mounts and renders the live page routed at r's path, as a GET
// would, without starting a session. With layouts false the page renders
// alone, without its app layout or root layout. loomtest.Mount uses it.
func (a *App) NewView(r *http.Request, sid string, layouts bool) (*View, error) {
	_, pattern := a.mux.Handler(r)
	rt := a.routesByPattern[pattern]
	if rt == nil {
		var paths []string
		for _, rt := range a.routes {
			paths = append(paths, rt.path)
		}
		return nil, &Error{Code: RT414, Msg: fmt.Sprintf("no live route matches %s %s", r.Method, r.URL.Path), Fix: "use one of the app's live paths: " + strings.Join(paths, ", ")}
	}
	setPathValues(r, rt.path)
	return newView(rt, r, sid, layouts, nil, "", "")
}

// NewViewFrom mounts and renders the live page routed at r's path as a
// live navigation from the page from would: if both pages are in the
// same live layout (and root layout), the layout's state moves to the
// new View, unmounted again, and from does nothing more. If the new
// page fails to mount, from keeps its layout. loomtest's Navigate uses it.
func (a *App) NewViewFrom(from *View, r *http.Request, sid string, layouts bool) (*View, error) {
	_, pattern := a.mux.Handler(r)
	rt := a.routesByPattern[pattern]
	if rt == nil {
		return a.NewView(r, sid, layouts) // RT414
	}
	setPathValues(r, rt.path)
	carry := from.lend(rt)
	v, err := newView(rt, r, sid, layouts, carry, from.browser, "")
	if err != nil {
		from.giveBack()
	}
	return v, err
}

// RemountView mounts the page routed at r's path again, as a remount
// after a restart would: a fresh copy of the page and its live layout,
// given the state from kept (Keep) before Mount. from does nothing
// more. loomtest's Remount uses it.
func (a *App) RemountView(from *View, r *http.Request, sid string, layouts bool) (*View, error) {
	_, pattern := a.mux.Handler(r)
	rt := a.routesByPattern[pattern]
	if rt == nil {
		return a.NewView(r, sid, layouts) // RT414
	}
	setPathValues(r, rt.path)
	from.lent = true
	return newView(rt, r, sid, layouts, nil, from.browser, from.kept)
}

// Restored says what the page's mount did with the state a remount
// brought it (Keep): "restored the layout's", "dropped: another
// browser's" and so on, or "" if it brought none.
func (v *View) Restored() string { return v.restored }

// carried is a live layout's state on its way from one page to the next
// in a live navigation: its state and the topics it subscribed to.
// Timers and Ctx values stay with the page it leaves.
type carried struct {
	state Page
	subs  []string
}

// lend hands the view's live layout state to a live navigation's next
// page, at route to, if that page is in the same live layout and root
// layout; else it returns nil. The view then does nothing more, so the
// state is never used by two pages at once, until giveBack.
func (v *View) lend(to *route) *carried {
	if v == nil || v.live == nil || v.route == nil || v.lent {
		return nil
	}
	l := v.route.group.appLayout()
	if l == nil || l.live == nil || l != to.group.appLayout() || v.route.group.rootLayout() != to.group.rootLayout() {
		return nil
	}
	v.lent = true
	subs := make([]string, 0, len(v.ctx.layoutSub))
	for t := range v.ctx.layoutSub {
		subs = append(subs, t)
	}
	sort.Strings(subs)
	return &carried{state: v.live.page, subs: subs}
}

// giveBack undoes lend: the next page didn't mount, so the layout stays.
func (v *View) giveBack() {
	if v != nil {
		v.lent = false
	}
}

// setPathValues sets r's path values from the route's {wildcards}, as
// the server's mux does, so c.Request.PathValue works in Mount.
func setPathValues(r *http.Request, route string) {
	for _, nv := range pathValues(route, r.URL.Path) {
		r.SetPathValue(nv[0], nv[1])
	}
}

// pathValues returns the names and values of the route's {wildcards} in
// path, in order.
func pathValues(route, path string) [][2]string {
	var values [][2]string
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i, p := range strings.Split(strings.Trim(route, "/"), "/") {
		if !strings.HasPrefix(p, "{") || !strings.HasSuffix(p, "}") || i >= len(segs) || p == "{$}" {
			continue
		}
		name := strings.Trim(p, "{}")
		if rest, ok := strings.CutSuffix(name, "..."); ok {
			return append(values, [2]string{rest, strings.Join(segs[i:], "/")})
		}
		values = append(values, [2]string{name, segs[i]})
	}
	return values
}

// newView copies the route's prototype page, mounts it and renders it.
// With carry, a live navigation brought the live layout's state from the
// previous page, already mounted. browser is the browser cookie's hash,
// and kept the state a remount brought back, sealed (keep.go).
func newView(rt *route, r *http.Request, sid string, layouts bool, carry *carried, browser, kept string) (*View, error) {
	clone, err := clonePage(rt.prototype)
	if err != nil {
		return nil, err
	}
	page := bindPage(clone)
	scope := rt.styledScope()
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	v := &View{page: page, route: rt, tmpl: rt.tmpl, auth: rt.auth, watch: rt.watch, scope: scope, cancel: cancel, ctx: &Ctx{SID: sid, Request: r, app: rt.group.app, scope: scope}, browser: browser}
	v.base = trace.WithTally(ctx, &v.tally)
	v.begin(trace.ID(r.Context())) // the page load's trace, if the runtime gave one
	if l := rt.group.appLayout(); l != nil && l.live != nil && carry != nil {
		v.live, v.ctx.layout = bindPage(carry.state), carry.state
		v.ctx.layoutSub = map[string]bool{}
		for _, t := range carry.subs {
			v.ctx.layoutSub[t] = true
			v.ctx.topics = append(v.ctx.topics, t)
			v.ctx.newTopics = append(v.ctx.newTopics, t)
		}
	} else if l != nil && l.live != nil {
		// A live layout's state is mounted even when the page renders
		// without its layout (loomtest.NoLayout), so the page can rely on
		// it and its events still work.
		state, err := clonePage(l.live)
		if err != nil {
			return nil, err
		}
		v.live, v.ctx.layout = bindPage(state), state
	}
	v.ctx.hasEvent = func(name string) bool {
		return page.events[name] != nil || (v.live != nil && v.live.events[name] != nil)
	}
	v.ctx.eventNames = func() []string {
		names := make([]string, 0, len(page.events))
		for n := range page.events {
			names = append(names, n)
		}
		if v.live != nil {
			for n := range v.live.events {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		return names
	}
	if layouts {
		v.layout, v.root = rt.group.appLayout(), rt.group.rootLayout()
	}
	if err := authorize(v.auth, v.ctx); err != nil {
		cancel()
		return nil, err
	}
	if err := rt.group.app.allow(v.ctx, rt.limits, rt.path, "", nil); err != nil {
		cancel()
		return nil, err
	}
	// What a remount brought back goes in before Mount, so Mount sees it.
	v.restored = v.restore(kept, carry != nil)
	// The layout mounts first, so the page's Mount can read it (LayoutOf).
	if v.live != nil && carry == nil {
		if err := v.run(v.live, "Mount", func() { v.live.page.Mount(v.ctx) }); err != nil {
			return nil, err
		}
	}
	assigns, lassign, err := v.call(v.page, "Mount", func() {
		clone.Mount(v.ctx)
		if v.ctx.notFound {
			return
		}
		v.handleParams()
		if p := v.ctx.patch; p != nil && v.ctx.navigate == "" {
			v.ctx.navigate = p.url // the page load redirects; see Ctx.Patch
		}
		v.ctx.patch = nil
	})
	if err != nil {
		return nil, err
	}
	if err := v.checkSubscriptions(); err != nil {
		return nil, err
	}
	tree, err := v.render(assigns, lassign, "Mount")
	if err != nil {
		return nil, err
	}
	v.tree, v.assigns, v.lassign, v.version, v.title = tree, assigns, lassign, 1, v.ctx.title
	v.ctx.changed, v.ctx.loaded = false, true
	v.keep()
	return v, nil
}

// Event runs the handler for the event, the page's or its live layout's,
// then Assigns, and re-renders. It returns the diff to send to the client, or nil if
// nothing changed. The page's title may have changed even when the diff
// is nil; see Title. The route's auth checks run first; if one fails,
// Event returns its error and the page is unchanged. An event the page
// has no handler for is error RT423, one no element on the page binds is
// RT438, and one over a rate limit is a *LimitError (RT436); none of
// them changes anything.
func (v *View) Event(name string, payload Payload) (map[string]any, error) {
	if v.lent {
		return nil, nil // the page is leaving; its layout is the next page's
	}
	v.begin("")
	if err := authorize(v.auth, v.ctx); err != nil {
		return nil, err
	}
	h, owner, err := v.handler(name)
	if err != nil {
		return nil, err
	}
	// Rendered without layouts (loomtest.NoLayout), the live layout's
	// markup isn't here to bind its events, so they aren't checked.
	if owner != v.live || v.layout != nil {
		if err := v.onPage(name); err != nil {
			return nil, err
		}
	}
	if v.route != nil {
		if err := v.ctx.app.allow(v.ctx, v.route.limits, v.route.path, name, payload); err != nil {
			return nil, err
		}
	}
	what := fmt.Sprintf("Events()[%q]", name)
	assigns, lassign, err := v.call(owner, what, func() {
		h(v.ctx, payload)
		v.applyPatches()
	})
	return v.update(assigns, lassign, what, err)
}

// onPage refuses an event no element of the current render binds with
// on:<event>="name" (RT438). A browser sends only events of elements it
// shows, so anything else was sent by hand, for a control the page
// chose not to render: a button behind :if={admin}, a step the person
// hasn't reached. The handler must not run; the page decided not to
// offer it.
func (v *View) onPage(name string) error {
	if v.boundTree != v.tree {
		v.bound = map[string]bool{}
		for _, m := range boundEvent.FindAllStringSubmatch(v.tree.HTML(), -1) {
			v.bound[html.UnescapeString(m[1])] = true
		}
		v.boundTree = v.tree
	}
	if v.bound[name] {
		return nil
	}
	return &Error{Code: RT438, Msg: fmt.Sprintf("the page got event %q, but no element on it has on:<event>=%q, so the event was refused", name, name),
		Fix: "render the control that sends it (an :if hides it from the page and from its handler alike); in a loomtest test of a hidden control, use p.AssertRefused"}
}

// boundEvent matches an on:<event>="name" attribute in rendered HTML.
var boundEvent = regexp.MustCompile(`\son:[a-zA-Z][\w-]*="([^"]*)"`)

// handler returns the func for an event and whose it is, the page's or
// its live layout's, or an RT423 error.
func (v *View) handler(name string) (func(c *Ctx, payload Payload), *boundPage, error) {
	if v.live != nil {
		if f := v.live.events[name]; f != nil {
			return f, v.live, nil
		}
	}
	f, err := v.page.handler(name, v.live)
	return f, v.page, err
}

// Info delivers a message published on a topic the page subscribed to,
// by running its HandleInfo method, then Assigns, and re-rendering. It
// returns the diff to send to the client, or nil if nothing changed.
// Like Event, it runs the route's auth checks first. A message on a
// topic the live layout subscribed to goes to the layout's HandleInfo,
// and also to the page's if the page subscribed to it too.
func (v *View) Info(msg Message) (map[string]any, error) {
	if v.lent {
		return nil, nil
	}
	v.begin(msg.Trace) // the action that published it, such as a job
	if err := authorize(v.auth, v.ctx); err != nil {
		return nil, err
	}
	what := fmt.Sprintf("HandleInfo(%q, %q)", msg.Topic, msg.Event)
	toLayout := v.live != nil && v.ctx.layoutSub[msg.Topic]
	if toLayout {
		if v.live.info == nil {
			return nil, noInfoHandler(v.live, v.ctx.layoutSub)
		}
		if err := v.run(v.live, what, func() {
			v.live.info.HandleInfo(v.ctx, msg)
			v.applyPatches()
		}); err != nil {
			return v.update(nil, nil, what, err)
		}
	}
	if toLayout && !v.ctx.pageSub[msg.Topic] {
		assigns, lassign, err := v.call(v.page, "Assigns", func() {})
		return v.update(assigns, lassign, what, err)
	}
	if v.page.info == nil {
		return nil, noInfoHandler(v.page, v.ctx.pageSub)
	}
	assigns, lassign, err := v.call(v.page, what, func() {
		v.page.info.HandleInfo(v.ctx, msg)
		v.applyPatches()
	})
	return v.update(assigns, lassign, what, err)
}

// Failures returns the errors the page reported with Ctx.Fail since the
// last call.
func (v *View) Failures() []error {
	f := v.ctx.failed
	v.ctx.failed = nil
	return f
}

// NewTopics returns the topics the page subscribed to since the last
// call, for the runtime to subscribe to.
func (v *View) NewTopics() []string {
	t := v.ctx.newTopics
	v.ctx.newTopics = nil
	return t
}

// Topics returns every topic the page has subscribed to.
func (v *View) Topics() []string { return v.ctx.Topics() }

func (v *View) checkSubscriptions() error {
	if v.page.info == nil && len(v.ctx.pageSub) > 0 {
		return noInfoHandler(v.page, v.ctx.pageSub)
	}
	if v.live != nil && v.live.info == nil && len(v.ctx.layoutSub) > 0 {
		return noInfoHandler(v.live, v.ctx.layoutSub)
	}
	return nil
}

// noInfoHandler is the RT410 error for a page or live layout that
// subscribed to topics but has no HandleInfo.
func noInfoHandler(b *boundPage, topics map[string]bool) error {
	names := make([]string, 0, len(topics))
	for t := range topics {
		names = append(names, t)
	}
	slices.Sort(names)
	return &Error{
		Code: RT410,
		Msg:  fmt.Sprintf("%T subscribed to %q but has no HandleInfo method to receive its messages", b.page, names),
		Fix:  fmt.Sprintf("add func (p %T) HandleInfo(c *loom.Ctx, msg loom.Message)", b.page),
	}
}

// update renders the assigns of a page after an event or message and
// diffs them against the current render.
func (v *View) update(assigns, lassign Assigns, what string, err error) (map[string]any, error) {
	if err == nil {
		err = v.checkSubscriptions()
	}
	if err != nil {
		return nil, err
	}
	v.ctx.changed = false
	tree, err := v.render(assigns, lassign, what)
	if err != nil {
		return nil, err
	}
	d, err := diff.Diff(v.tree, tree)
	if err != nil {
		return nil, err // keys were checked by render; this would be a Loom bug
	}
	v.tree, v.assigns, v.lassign = tree, assigns, lassign
	v.keep()
	if d != nil || v.ctx.title != v.title {
		v.version++
		v.title = v.ctx.title
	}
	return d, nil
}

// Route returns the path of the page's route, e.g. /todos/{id}, or ""
// for a View no app routes.
func (v *View) Route() string {
	if v.route == nil {
		return ""
	}
	return v.route.path
}

// Trace returns the trace id of the page's latest action: its mount, or
// the event, message, timer or re-render it last handled. The events
// that action emitted carry it (docs/events.md).
func (v *View) Trace() string { return v.trace }

// setTrace makes the next action use id as its trace, as the runtime
// does for a browser event (its client and sequence).
func (v *View) setTrace(id string) { v.next = id }

// Tally counts the statements run under the page's context since the
// last ResetTally: the session resets it before each action and reports
// it on the action's page.* event. A goroutine a handler started that
// still uses c.Context() counts toward whichever action is current.
func (v *View) Tally() *trace.Tally { return &v.tally }

// begin starts an action: it picks its trace id (setTrace's, or cause,
// or a new one) and puts it in the page's context, so the action's
// transactions, jobs and errors carry it.
func (v *View) begin(cause string) {
	id := v.next
	if id == "" {
		id = cause
	}
	if id == "" {
		id = trace.NewID()
	}
	v.next, v.trace = "", id
	v.ctx.ctx = trace.WithTrace(v.base, id)
}

// Close cancels the page's context (Ctx.Context). The runtime calls it
// when the session ends or the page restarts.
func (v *View) Close() { v.cancel() }

// Tree returns the encoded current render tree (see docs/wire.md).
func (v *View) Tree() map[string]any { return diff.Full(v.tree) }

// HTML returns the current rendered HTML.
func (v *View) HTML() string { return v.tree.HTML() }

// Assigns returns the page's assigns of the current render.
func (v *View) Assigns() Assigns { return v.assigns }

// LayoutAssigns returns the live layout's assigns of the current render,
// with no inner_block, or nil if the page has no live layout.
func (v *View) LayoutAssigns() Assigns { return v.lassign }

// Version counts renders that changed the page or its title, starting
// at 1.
func (v *View) Version() int { return v.version }

// Title returns the page's title, set with Ctx.Title ("" if none).
func (v *View) Title() string { return v.title }

// liveRoot is the element the client takes over: the live tree, then
// the encoded tree it starts from (see docs/wire.md).
var liveRoot = &render.Statics{ID: "loom.root", Parts: []string{
	`<div id="loom-root" data-sid="`, `">`, "</div>\n<script id=\"loom-tree\" type=\"application/json\">", "</script>",
}}

// Document renders the page's whole HTML document, as a GET serves it:
// the root layout with the live root inside. Without layouts, it is the
// live root alone.
func (v *View) Document() (string, error) {
	inner := &render.Rendered{Statics: liveRoot, Dynamics: []render.Dynamic{render.Escape(v.ctx.SID), v.tree, nil}}
	doc, root := inner, ""
	if v.root != nil {
		var err error
		s := *v.scope
		s.Meta = v.headMeta()
		if doc, err = v.renderLayout(v.root, &s, inner, nil); err != nil {
			return "", err
		}
		// The scripts' URLs change with their content, so a remount
		// after a deploy that changed loom.js reloads the page instead.
		js, nav := v.ctx.app.clientScripts()
		root = fingerprint(doc.Statics) + "." + fingerprint(&render.Statics{Parts: []string{js, nav}})
	}
	// encoding/json escapes <, > and &, so the tree is safe inside <script>.
	// "kept" is always there, so a live navigation drops the last page's.
	treeJSON, err := json.Marshal(map[string]any{"v": v.version, "tree": diff.Full(v.tree), "root": root, "p": protocol, "kept": v.kept})
	if err != nil {
		return "", err
	}
	inner.Dynamics[2] = render.Text(treeJSON)
	return doc.HTML(), nil
}

// protocol is the version of the wire protocol (docs/wire.md): the shape
// of trees, diffs, events and acks. A page's tree carries it, and a
// remount whose new tree has another reloads the page, so a tab running
// an old loom.js never talks to a server that speaks differently. Bump
// it with any change to the wire format.
const protocol = 1

// fingerprint identifies a root layout's markup, so a remount can tell
// whether the document it keeps is still the one the server renders.
func fingerprint(s *render.Statics) string {
	h := fnv.New64a()
	h.Write([]byte(s.ID))
	for _, p := range s.Parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

// run runs a method of the page or its live layout (owner), turning a
// panic into an error. Subscriptions made meanwhile are owner's.
func (v *View) run(owner *boundPage, what string, f func()) (err error) {
	defer func() {
		v.ctx.inLayout = false
		if p := recover(); p != nil {
			err = panicError(fmt.Sprintf("%T.%s", owner.page, what), p)
		}
	}()
	v.ctx.inLayout = owner == v.live
	f()
	return v.notFound(what)
}

// notFound settles a call of Ctx.NotFound by the method just run: from
// Mount it ends the page load with ErrPageNotFound; from anywhere else
// it is too late to answer 404, and is reported like c.Fail (RT448).
func (v *View) notFound(what string) error {
	if !v.ctx.notFound {
		return nil
	}
	if what == "Mount" {
		return ErrPageNotFound
	}
	v.ctx.notFound = false
	v.ctx.failed = append(v.ctx.failed, &Error{Code: RT448, Msg: fmt.Sprintf("c.NotFound was called in %s, after the page loaded", what),
		Fix: "call c.NotFound in Mount, where the page load can answer 404; after that, show the page's own empty state or c.Navigate elsewhere"})
	return nil
}

// call runs a method of the page or its live layout, then the Assigns
// of both, returning the page's assigns and the live layout's (nil if it
// has none).
func (v *View) call(owner *boundPage, what string, f func()) (a, la Assigns, err error) {
	if err := v.run(owner, what, f); err != nil {
		return nil, nil, err
	}
	if v.live != nil {
		if err := v.run(v.live, "Assigns", func() { la = v.live.assigns(v.ctx) }); err != nil {
			return nil, nil, err
		}
	}
	if err := v.run(v.page, "Assigns", func() { a = v.page.assigns(v.ctx) }); err != nil {
		return nil, nil, err
	}
	return a, la, nil
}

// render renders the page with assigns, inside its app layout if any.
func (v *View) render(assigns, lassign Assigns, what string) (*render.Rendered, error) {
	r, err := v.renderPage(assigns, what)
	if err == nil && v.layout != nil {
		r, err = v.renderLayout(v.layout, v.scope, r, lassign)
	}
	if err != nil {
		return nil, err
	}
	if err := diff.CheckKeys(r); err != nil {
		return nil, &Error{Code: RT403, Msg: err.Error(), Fix: "give each item a unique :key, such as a database id"}
	}
	return r, nil
}

// renderLayout renders a layout around inner, with its assigns func's
// assigns, or lassign for a live layout.
func (v *View) renderLayout(l *layout, scope *render.Scope, inner *render.Rendered, lassign Assigns) (r *render.Rendered, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = panicError("rendering layout "+l.name(), p)
		}
	}()
	return l.render(v.ctx, scope, func() *render.Rendered { return inner }, lassign)
}

// Meta returns the metadata the page set with Ctx.Meta.
func (v *View) Meta() Meta { return v.ctx.meta }

// headMeta is the page's metadata as the root layout's <.live_meta />
// renders it: with its title, and paths made absolute URLs on the app's
// BaseURL, or the request's origin without one.
func (v *View) headMeta() render.Meta {
	m, r, url := v.ctx.meta, v.ctx.Request, v.ctx.app.URL
	return render.Meta{Title: v.ctx.title, Description: m.Description, Image: url(r, m.Image),
		Canonical: url(r, m.Canonical), StructuredData: v.ctx.ld, NoIndex: m.NoIndex}
}

func (v *View) renderPage(assigns Assigns, what string) (r *render.Rendered, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = panicError("rendering "+v.tmpl.ComponentName(), p)
		}
	}()
	r, err = v.tmpl.RenderAssigns(v.scope, assigns)
	var ae *render.AssignsError
	if errors.As(err, &ae) {
		return nil, &Error{
			Code: RT401,
			Msg:  fmt.Sprintf("%T.%s returned %s, but component %s needs %s", v.page.page, what, ae.Got, ae.Component, ae.Want),
			Fix:  fmt.Sprintf("return a %s value, e.g. %s{...}", ae.Want, ae.Want),
		}
	}
	return r, err
}

// clonePage makes a shallow copy of a page or live layout prototype, so
// each page load starts from the same state.
func clonePage(prototype Page) (Page, error) {
	v := reflect.ValueOf(prototype)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return nil, &Error{Code: RT406, Msg: fmt.Sprintf("a live page must be a pointer to a struct, got %T", prototype), Fix: "pass a pointer, e.g. app.Live(\"/\", &TodoPage{}, templates.TodoList)"}
	}
	c := reflect.New(v.Elem().Type())
	c.Elem().Set(v.Elem())
	return c.Interface().(Page), nil
}
