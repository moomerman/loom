package loomtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/diff"
	"github.com/moomerman/loom/pubsub"
	"github.com/moomerman/loom/render"
	"github.com/moomerman/loom/trace"
)

// Page is a live page driven without a browser or server: the same Mount,
// event, render and diff steps the runtime runs, called directly.
//
//	p := loomtest.Mount(t, app, "/")
//	p.Event("add", loom.Payload{"title": "Milk"})
//	p.AssertHTML("li", 3)
//	p.AssertDiff(`{"d":{"0":{"o":[["1"],["2"],["3",[...]]]}}}`)
type Page struct {
	t    testing.TB
	app  *loom.App
	view *loom.View
	tree map[string]any // the client's copy of the tree, kept by applying diffs
	diff map[string]any // the diff sent by the last event
	ps   pubsub.PubSub  // set by Listen
	nav  string         // the last c.Navigate
	now  time.Time      // the fake clock of the page's timers (Advance)
	opts options        // how it was mounted, for Navigate
}

// TimersEpoch is the time a mounted page's fake clock starts at. Page
// timers (loom.Ctx.SendAfter) run only when Advance moves the clock.
var TimersEpoch = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

// validated holds the apps Mount has validated.
var validated sync.Map

// Option changes how Mount mounts a page.
type Option func(*options)

type options struct {
	noLayout bool
	cookies  []*http.Cookie
}

// NoLayout mounts the page alone, without its app layout or root layout,
// for tests focused on the page itself.
func NoLayout() Option { return func(o *options) { o.noLayout = true } }

// Cookie sends a cookie with the page's request, as a signed-in browser
// would: auth checks and Mount see it, and so does every later event.
//
//	p := loomtest.Mount(t, app, "/admin/jobs", loomtest.Cookie("user", "Admin"))
func Cookie(name, value string) Option {
	return func(o *options) { o.cookies = append(o.cookies, &http.Cookie{Name: name, Value: value}) }
}

// SignIn signs userID in to app, as the browser of someone who signed
// in would be: Mount sends the session cookie, so loom.SignedIn lets the
// page in and c.UserID and loom.User see userID.
//
//	p := loomtest.Mount(t, app, "/", loomtest.SignIn(t, app, "ana"))
func SignIn(t testing.TB, app *loom.App, userID string) Option {
	t.Helper()
	c, err := app.SessionCookie(context.Background(), userID)
	if err != nil {
		t.Fatalf("signing in %s: %v", userID, err)
	}
	return func(o *options) { o.cookies = append(o.cookies, c) }
}

// Mount mounts the live page that app routes at path, as a GET of path
// would: a fresh copy of the route's page, mounted and rendered inside
// its app layout. It fails the test on any error, such as a panic, the
// wrong assigns type or a path no live route matches, and where the app
// would fail to start (see Validate, which it runs once per app).
func Mount(t testing.TB, app *loom.App, path string, opts ...Option) *Page {
	t.Helper()
	if _, done := validated.Load(app); !done {
		Validate(t, app)
		validated.Store(app, true)
	}
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	start := time.Now()
	v, err := app.NewView(o.request(path), "test", !o.noLayout)
	return newPage(t, app, v, err, start, "mounting "+path, TimersEpoch, nil, o)
}

// request is a GET of path from the browser o describes.
func (o options) request(path string) *http.Request {
	r := httptest.NewRequest("GET", path, nil)
	for _, c := range o.cookies {
		r.AddCookie(c)
	}
	return r
}

// newPage wraps a View just mounted (by Mount or Navigate, as what
// says), failing the test as Mount documents. The page's fake clock
// starts at now, and with ps it hears what is published.
func newPage(t testing.TB, app *loom.App, v *loom.View, err error, start time.Time, what string, now time.Time, ps pubsub.PubSub, o options) *Page {
	t.Helper()
	if errors.Is(err, loom.ErrPageNotFound) {
		t.Fatalf("%s: the page called c.NotFound (check that with loomtest.AssertNotFound)", what)
	}
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	app.Emit(context.Background(), trace.PageMount, append([]any{"sid", "test", "route", v.Route(), "ms", time.Since(start), "outcome", "ok", "trace", v.Trace()}, v.Tally().Attrs()...)...)
	v.Tally().Reset()
	v.NewTopics() // Listen subscribes to every topic
	p := &Page{t: t, app: app, view: v, tree: roundTrip(t, v.Tree()), nav: v.Navigation(), now: now, ps: ps, opts: o}
	v.SetClock(func() time.Time { return p.now })
	p.failures(what)
	if ps != nil {
		p.subscribe(v.Topics())
	}
	return p
}

// AssertNotFound fails the test unless the live page app routes at path
// calls c.NotFound in Mount, so that loading it answers 404.
//
//	loomtest.AssertNotFound(t, app, "/tracks/nothing")
func AssertNotFound(t testing.TB, app *loom.App, path string, opts ...Option) {
	t.Helper()
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	v, err := app.NewView(o.request(path), "test", !o.noLayout)
	switch {
	case errors.Is(err, loom.ErrPageNotFound):
	case err != nil:
		t.Fatalf("mounting %s: %v, want c.NotFound", path, err)
	default:
		v.Close()
		t.Fatalf("mounting %s: the page loaded, want c.NotFound", path)
	}
}

// failures fails the test for each error the page reported with
// loom.Ctx.Fail, letting it carry on: the page kept running.
func (p *Page) failures(what string) {
	p.t.Helper()
	for _, err := range p.view.Failures() {
		p.t.Errorf("%s: the page reported an error with c.Fail: %v", what, err)
	}
}

// Event sends an event to the page, as a browser would with an on:<event>
// binding. It fails the test on any error, including one a handler reports
// with c.Fail. It also applies the diff to a copy of the tree, as the
// browser does, and fails if the result doesn't match a fresh render: a
// check on Loom itself.
func (p *Page) Event(name string, payload loom.Payload) {
	p.t.Helper()
	start := time.Now()
	d, err := p.view.Event(name, payload)
	p.handled(trace.PageEvent, start, d, err, "event", name)
	p.apply(fmt.Sprintf("event %q", name), d, err)
}

// AssertRefused sends an event that no element on the page binds, as
// someone posting it by hand would, and fails the test unless the page
// refuses it (RT438) and stays as it was. Use it to check that a control
// the page hides (a button behind :if={admin}, a step not reached yet)
// can't be used anyway.
//
//	p.AssertRefused("delete", loom.Payload{"id": "7"})
func (p *Page) AssertRefused(name string, payload loom.Payload) {
	p.t.Helper()
	before := p.view.HTML()
	_, err := p.view.Event(name, payload)
	var e *loom.Error
	if !errors.As(err, &e) || e.Code != loom.RT438 {
		p.t.Fatalf("event %q: got %v, want it refused with RT438: an element on the page binds it, or the page has no handler for it", name, err)
	}
	if after := p.view.HTML(); after != before {
		p.t.Fatalf("event %q was refused but the page changed:\n%s", name, after)
	}
}

// Info delivers a message as if it were published on a topic the page
// subscribed to (see loom.Ctx.Subscribe), running its HandleInfo. It
// fails the test if the page isn't subscribed to the topic or on any
// error, and checks the diff like Event.
//
//	p.Info(loom.Message{Topic: "todos", Event: "pruned", Payload: map[string]string{"count": "2"}})
func (p *Page) Info(msg loom.Message) {
	p.t.Helper()
	if !slices.Contains(p.view.Topics(), msg.Topic) {
		p.t.Fatalf("info %q on %q: the page isn't subscribed to %q (it is subscribed to %q); call c.Subscribe in Mount", msg.Event, msg.Topic, msg.Topic, p.view.Topics())
	}
	start := time.Now()
	d, err := p.view.Info(msg)
	p.handled(trace.PageInfo, start, d, err, "topic", msg.Topic, "event", msg.Event)
	p.apply(fmt.Sprintf("info %q on %q", msg.Event, msg.Topic), d, err)
}

// TableChanged re-renders the page as the runtime does when a table its
// route watches changes (loom.Watch): it re-runs Assigns and checks the
// diff like Event. It fails the test if the route doesn't watch table.
// Use it after changing the table outside the page, as another user, a
// job or another node would:
//
//	other.Event("add", loom.Payload{"title": "Milk"})
//	p.TableChanged("todos")
//	p.AssertText(".todo-list li", "Milk")
func (p *Page) TableChanged(table string) {
	p.t.Helper()
	if !slices.Contains(p.view.Watches(), table) {
		p.t.Fatalf("table %s changed, but the page's route doesn't watch it (it watches %q); add loom.Watch(%q) to its app.Live", table, p.view.Watches(), table)
	}
	start := time.Now()
	d, err := p.view.Refresh()
	p.handled(trace.PageWatch, start, d, err)
	p.apply(fmt.Sprintf("a change to %s", table), d, err)
}

// Advance moves the page's fake clock forward by d, running each timer
// set with loom.Ctx.SendAfter that falls due, in order, at its due time,
// as the runtime would: so a page polling every 5s runs three times in
// Advance(16*time.Second). Each run re-renders and checks the diff like
// Event. Nothing runs a page's timers but Advance.
//
//	p.Input("input[name=q]", "bug")   // the handler calls c.SendAfter(300*time.Millisecond, "search", nil)
//	p.AssertHTML(".result", 0)
//	p.Advance(300 * time.Millisecond)
//	p.AssertHTML(".result", 2)
func (p *Page) Advance(d time.Duration) {
	p.t.Helper()
	p.diff = nil
	until := p.now.Add(d)
	for {
		at, ok := p.view.NextTimer()
		if !ok || at.After(until) {
			break
		}
		if at.After(p.now) {
			p.now = at
		}
		names := []string{}
		for _, t := range p.view.Timers() {
			if !t.Due.After(p.now) {
				names = append(names, t.Event)
			}
		}
		start := time.Now()
		d, err := p.view.RunTimers()
		p.handled(trace.PageTimer, start, d, err)
		p.apply(fmt.Sprintf("timer %s", strings.Join(names, ", ")), d, err)
	}
	p.now = until
}

// Timers returns the page's pending timers (loom.Ctx.SendAfter), as the
// time until each runs, by event.
//
//	if got := p.Timers()["save"]; got != 2*time.Second { t.Errorf(...) }
func (p *Page) Timers() map[string]time.Duration {
	out := map[string]time.Duration{}
	for _, t := range p.view.Timers() {
		out[t.Event] = t.Due.Sub(p.now)
	}
	return out
}

// Listen connects the page to ps: messages published on its topics are
// delivered with Info as they are published, on the publishing
// goroutine. Use it with loomtest.Jobs, whose Drain runs jobs (and their
// Broadcasts) on the test's goroutine:
//
//	q := loomtest.Jobs(t)
//	p.Listen(q.PubSub())
//	q.Drain(t) // a job's c.Broadcast reaches the page's HandleInfo
func (p *Page) Listen(ps pubsub.PubSub) {
	p.ps = ps
	p.subscribe(p.view.Topics())
	p.view.NewTopics()
}

func (p *Page) subscribe(topics []string) {
	if p.ps == nil {
		return
	}
	for _, topic := range topics {
		unsub := p.ps.Subscribe(topic, func(m pubsub.Message) { p.Info(m) })
		p.t.Cleanup(unsub)
	}
}

// apply checks and applies the diff from an event or info.
func (p *Page) apply(what string, d map[string]any, err error) {
	p.t.Helper()
	if err != nil {
		p.t.Fatalf("%s: %v", what, err)
	}
	p.failures(what)
	p.subscribe(p.view.NewTopics())
	if url := p.view.Navigation(); url != "" {
		p.nav = url
	}
	p.diff = d
	if d == nil {
		return
	}
	tree, err := diff.Apply(p.tree, roundTrip(p.t, d))
	if err != nil {
		p.t.Fatalf("%s: applying the diff: %v", what, err)
	}
	if got, want := diff.HTML(tree), p.view.HTML(); got != want {
		p.t.Fatalf("%s: the diff doesn't reproduce the render (a Loom bug)\npatched:\n%s\nrendered:\n%s", what, got, want)
	}
	p.tree = tree
}

// HTML returns the HTML of the page's live tree: the app layout with the
// page inside (just the page with NoLayout).
func (p *Page) HTML() string { return p.view.HTML() }

// Document returns the page's whole HTML document as a GET would serve
// it: the root layout around the live tree (just the live root with
// NoLayout).
func (p *Page) Document() string {
	p.t.Helper()
	doc, err := p.view.Document()
	if err != nil {
		p.t.Fatalf("rendering the document: %v", err)
	}
	return doc
}

// Navigated returns the URL the page last sent the browser to with
// loom.Ctx.Navigate, in Mount or an event, or "" if it hasn't. In the
// browser, Navigate in Mount redirects the page load, and in an event
// navigates (live, if the URL is a page with the same root layout).
//
//	p.Event("create", loom.Payload{"title": "Bug"})
//	if got := p.Navigated(); got != "/issues/1" { t.Errorf(...) }
func (p *Page) Navigated() string { return p.nav }

// URL returns the page's current URL, path and query, as the browser's
// address bar shows it: the URL it was mounted at, or the last one it
// patched to with loom.Ctx.Patch or Patch.
//
//	p.Click("[role=option][data-value='2']")
//	if got := p.URL(); got != "/team/ENG?priority=2" { t.Errorf(...) }
func (p *Page) URL() string { return p.view.URL() }

// Patch changes the page's URL as the browser does when the user
// follows a link to the page's path with another query, or goes back or
// forward to one: it runs the page's HandleParams with the new URL and
// checks the diff like Event. It fails the test if the URL isn't of the
// page's path (RT426), the page has no HandleParams (RT427), or on any
// error.
//
//	p := loomtest.Mount(t, app, "/team/ENG")
//	p.Patch("/team/ENG?view=backlog")
//	p.AssertText(".tab.selected", "Backlog")
func (p *Page) Patch(url string) {
	p.t.Helper()
	start := time.Now()
	d, err := p.view.Patch(url)
	p.handled(trace.PagePatch, start, d, err, "url", url)
	p.apply(fmt.Sprintf("patching the URL to %q", url), d, err)
}

// Follow clicks the link matching selector, as navigate.js does: a link
// to the page's path with another query, on a page with HandleParams,
// patches the page (see Patch); any other link, including one to another
// path value of the route, leaves it, and Navigated
// returns its URL. It fails the test unless exactly one element matches
// and it is an <a> with an href.
//
//	p.Follow(`.tabs a[href="/team/ENG?view=backlog"]`)
func (p *Page) Follow(selector string) {
	p.t.Helper()
	sel, err := parseSelector(selector)
	if err != nil {
		p.t.Fatal(err)
	}
	found := find(parseHTML(p.HTML()), sel)
	if len(found) != 1 {
		p.t.Fatalf("follow: %q matches %d elements, want 1, in:\n%s", selector, len(found), p.HTML())
	}
	href, ok := found[0].attrs["href"]
	if found[0].tag != "a" || !ok {
		p.t.Fatalf("follow: %q is a <%s> with no href; Follow follows links, Click clicks buttons", selector, found[0].tag)
	}
	if p.view.HandlesParams() {
		start := time.Now()
		d, err := p.view.Patch(href)
		var e *loom.Error
		if !errors.As(err, &e) || e.Code != loom.RT426 {
			p.handled(trace.PagePatch, start, d, err, "url", href)
			p.apply(fmt.Sprintf("following %q to %q", selector, href), d, err)
			return
		}
	}
	p.nav = href
}

// Navigate leaves the page for the live page app routes at path, as a
// live navigation does (a link, back or forward, or c.Navigate), and
// returns the new page. If both pages are in the same live layout, the
// layout's state moves to the new page, as it does in the browser:
// what it holds survives, and its Mount doesn't run again. The old page
// does nothing more. It fails the test like Mount.
//
//	tune := loomtest.Mount(t, app, "/c64/monty-on-the-run")
//	tune.Click("button.play")
//	next := tune.Navigate("/c64/lykkehjulet")
//	next.AssertText("#now-title", "Monty on the Run") // still playing
func (p *Page) Navigate(path string) *Page {
	p.t.Helper()
	start := time.Now()
	v, err := p.app.NewViewFrom(p.view, p.opts.request(path), "test", !p.opts.noLayout)
	return newPage(p.t, p.app, v, err, start, "navigating to "+path, p.now, p.ps, p.opts)
}

// Title returns the page's title, as set with loom.Ctx.Title.
func (p *Page) Title() string { return p.view.Title() }

// Meta returns the page's metadata, as set with loom.Ctx.Meta.
func (p *Page) Meta() loom.Meta { return p.view.Meta() }

// Diff returns the wire JSON of the diff sent by the last event (see
// docs/wire.md), or "" if it changed nothing.
func (p *Page) Diff() string {
	if p.diff == nil {
		return ""
	}
	b, err := diff.Marshal(p.diff)
	if err != nil {
		p.t.Fatal(err)
	}
	return string(b)
}

// AssertDiff fails the test unless the last event sent exactly this diff.
// Use "" to assert that it changed nothing.
func (p *Page) AssertDiff(want string) {
	p.t.Helper()
	if got := p.Diff(); got != want {
		p.t.Errorf("diff:\n got: %s\nwant: %s", got, want)
	}
}

// AssertHTML fails the test unless exactly n elements match the selector.
// See Count for the supported selectors.
func (p *Page) AssertHTML(selector string, n int) {
	p.t.Helper()
	got, err := Count(p.HTML(), selector)
	if err != nil {
		p.t.Fatal(err)
	}
	if got != n {
		p.t.Errorf("%q matches %d elements, want %d, in:\n%s", selector, got, n, p.HTML())
	}
}

// AssertText fails the test unless an element matching the selector
// contains text, whitespace collapsed. See Count for the supported
// selectors.
//
//	p.AssertText(".todo-list li", "Buy milk")
func (p *Page) AssertText(selector, text string) {
	p.t.Helper()
	sel, err := parseSelector(selector)
	if err != nil {
		p.t.Fatal(err)
	}
	found := find(parseHTML(p.HTML()), sel)
	var texts []string
	for _, e := range found {
		if strings.Contains(e.Text(), strings.Join(strings.Fields(text), " ")) {
			return
		}
		texts = append(texts, e.Text())
	}
	if len(found) == 0 {
		p.t.Errorf("no element matches %q, so none contains %q, in:\n%s", selector, text, p.HTML())
		return
	}
	p.t.Errorf("no element matching %q contains %q; their texts are %q", selector, text, texts)
}

// Version returns the page's version: 1 after Mount, plus one for each
// event that changed it or its title.
func (p *Page) Version() int { return p.view.Version() }

func roundTrip(t testing.TB, v map[string]any) map[string]any {
	t.Helper()
	b, err := diff.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Emit sends an event as the behavior attached to the element matching
// selector would by calling emit(name, data) in the browser: the
// element's on:<name> event, with its val-* attributes and data as the
// payload. It fails the test unless exactly one element matches, a
// behavior is attached to it, the behavior declares name in its emits,
// and the element binds on:<name>.
//
//	p.Emit("[role=listbox]", "select", map[string]any{"value": "pear"})
func (p *Page) Emit(selector, name string, data map[string]any) {
	p.t.Helper()
	sel, err := parseSelector(selector)
	if err != nil {
		p.t.Fatal(err)
	}
	found := find(parseHTML(p.HTML()), sel)
	if len(found) != 1 {
		p.t.Fatalf("emit %q: %q matches %d elements, want 1, in:\n%s", name, selector, len(found), p.HTML())
	}
	el := found[0]
	id, ok := el.attrs["data-loom-use"]
	if !ok {
		p.t.Fatalf("emit %q: %q has no behavior attached (use:name)", name, selector)
	}
	b, ok := render.LookupBehavior(id)
	if !ok {
		p.t.Fatalf("emit %q: behavior %s isn't registered", name, id)
	}
	if !slices.Contains(b.Emits, name) {
		p.t.Fatalf("emit %q: behavior %s doesn't declare %s; it emits %q", name, b.Name, name, b.Emits)
	}
	event, ok := el.attrs["on:"+name]
	if !ok {
		p.t.Fatalf("emit %q: %q has no on:%s binding, so the browser would send nothing", name, selector, name)
	}
	payload := vals(el)
	for k, v := range data {
		payload[k] = fmt.Sprint(v)
	}
	p.Event(event, payload)
}

// Click clicks the element matching selector, as a browser would: the
// nearest element at or above it with an on:click binding sends its
// event, with that element's val-* attributes (and, for a checkbox, its
// new checked state). It fails the test unless exactly one element
// matches and something binds on:click, so a test that clicks the
// button, rather than calling Event with a name, also checks the name
// the template sends.
//
//	p.Click("li#todo-3 button.delete")
func (p *Page) Click(selector string) {
	p.t.Helper()
	el, bound := p.bound(selector, "click")
	payload := vals(bound)
	if bound.tag == "input" && (bound.attrs["type"] == "checkbox" || bound.attrs["type"] == "radio") {
		_, checked := bound.attrs["checked"]
		payload["checked"] = fmt.Sprint(!checked || bound.attrs["type"] == "radio")
	}
	if el == bound && isField(el) {
		payload["value"] = fieldValue(el)
	}
	p.Event(bound.attrs["on:click"], payload)
}

// Change sets the value of the input, select or textarea matching
// selector and fires its change event, as a browser does when the user
// leaves a changed field: the nearest on:change binding sends its event
// with its val-* attributes and the value, or, when that binding is a
// form, with all the form's fields (this one set to value) and its val-*
// attributes. For a checkbox, value is "true" or "false", sent as
// checked.
//
//	p.Change("input[name=done]", "true")
//	p.Change("select[name=priority]", "2")
func (p *Page) Change(selector, value string) {
	p.t.Helper()
	p.setField(selector, "change", value)
}

// Input is Change for on:input, which fires on every keystroke: it sets
// the field's value and sends the nearest on:input binding's event.
//
//	p.Input("input[type=search]", "bug")
func (p *Page) Input(selector, value string) {
	p.t.Helper()
	p.setField(selector, "input", value)
}

func (p *Page) setField(selector, event, value string) {
	p.t.Helper()
	if el := p.one(selector, event); !isField(el) {
		p.t.Fatalf("%s %q: it is a <%s>, not an input, select or textarea", event, selector, el.tag)
	}
	el, bound := p.bound(selector, event)
	payload := vals(bound)
	if bound.tag == "form" && bound != el {
		// A form's event sends all its fields, this one changed, then its
		// val-* attributes, as loom.js does.
		payload, _ = formFields(bound)
		name := el.attrs["name"]
		switch typ := el.attrs["type"]; {
		case el.tag == "input" && (typ == "checkbox" || typ == "radio"):
			if value == "true" {
				payload[name] = fieldValue(el)
			} else if payload[name] == fieldValue(el) {
				delete(payload, name)
			}
		default:
			payload[name] = value
		}
		for k, v := range vals(bound) {
			payload[k] = v
		}
		p.Event(bound.attrs["on:"+event], payload)
		return
	}
	if el.tag == "input" && (el.attrs["type"] == "checkbox" || el.attrs["type"] == "radio") {
		if value != "true" && value != "false" {
			p.t.Fatalf("%s %q: a %s takes \"true\" or \"false\", not %q", event, selector, el.attrs["type"], value)
		}
		payload["checked"] = value
		value = fieldValue(el)
	}
	if el == bound {
		payload["value"] = value
	}
	p.Event(bound.attrs["on:"+event], payload)
}

// Submit submits the form matching selector (or the form around the
// element matching it) with fields filled in, as a browser would: the
// form's on:submit sends its event with every named field, those not in
// fields keeping the value in the markup. It fails the test if a key of
// fields names no field of the form, which catches a name="titel" that
// a Payload given to Event would not.
//
//	p.Submit("form.new-todo", loom.Payload{"title": "Milk"})
func (p *Page) Submit(selector string, fields loom.Payload) {
	p.t.Helper()
	_, form := p.bound(selector, "submit")
	if form.tag != "form" {
		p.t.Fatalf("submit %q: on:submit is on a <%s>, not a <form>", selector, form.tag)
	}
	payload, names := formFields(form)
	for k, v := range fields {
		if !slices.Contains(names, k) {
			p.t.Fatalf("submit %q: the form has no field named %q; its fields are %q", selector, k, names)
		}
		payload[k] = v
	}
	for k, v := range vals(form) {
		payload[k] = v
	}
	p.Event(form.attrs["on:submit"], payload)
}

// formFields returns the fields a form's events send, as a browser
// would, and every field's name.
func formFields(form *Element) (map[string]string, []string) {
	payload := map[string]string{}
	var names []string
	var walk func(e *Element)
	walk = func(e *Element) {
		for _, c := range e.children {
			if name, ok := c.attrs["name"]; ok && isField(c) {
				names = append(names, name)
				switch typ := c.attrs["type"]; {
				case c.tag == "input" && (typ == "checkbox" || typ == "radio"):
					if _, checked := c.attrs["checked"]; checked {
						payload[name] = fieldValue(c)
					}
				case c.tag == "input" && (typ == "submit" || typ == "button" || typ == "reset"):
				default:
					payload[name] = fieldValue(c)
				}
			}
			walk(c)
		}
	}
	walk(form)
	return payload, names
}

// bound finds the one element matching selector and the nearest element
// at or above it that binds on:<event>, failing the test otherwise.
func (p *Page) bound(selector, event string) (el, bound *Element) {
	p.t.Helper()
	el = p.one(selector, event)
	for e := el; e != nil; e = e.parent {
		if _, ok := e.attrs["on:"+event]; ok {
			return el, e
		}
	}
	p.t.Fatalf("%s %q: neither it nor an element around it has on:%s, so the browser would send nothing", event, selector, event)
	return nil, nil
}

// one finds the one element matching selector, failing the test
// otherwise.
func (p *Page) one(selector, event string) *Element {
	p.t.Helper()
	sel, err := parseSelector(selector)
	if err != nil {
		p.t.Fatal(err)
	}
	found := find(parseHTML(p.HTML()), sel)
	if len(found) != 1 {
		p.t.Fatalf("%s %q: it matches %d elements, want 1, in:\n%s", event, selector, len(found), p.HTML())
	}
	return found[0]
}

// vals returns an element's val-* attributes as a payload.
func vals(e *Element) loom.Payload {
	payload := loom.Payload{}
	for k, v := range e.attrs {
		if name, ok := strings.CutPrefix(k, "val-"); ok {
			payload[name] = v
		}
	}
	return payload
}

func isField(e *Element) bool { return e.tag == "input" || e.tag == "select" || e.tag == "textarea" }

// fieldValue is a field's value as the markup sets it.
func fieldValue(e *Element) string {
	switch e.tag {
	case "textarea":
		return e.Text()
	case "select":
		var first *Element
		var walk func(e *Element) *Element
		walk = func(e *Element) *Element {
			for _, c := range e.children {
				if c.tag == "option" {
					if first == nil {
						first = c
					}
					if _, ok := c.attrs["selected"]; ok {
						return c
					}
				}
				if o := walk(c); o != nil {
					return o
				}
			}
			return nil
		}
		o := walk(e)
		if o == nil {
			o = first
		}
		if o == nil {
			return ""
		}
		if v, ok := o.attrs["value"]; ok {
			return v
		}
		return o.Text()
	}
	if v, ok := e.attrs["value"]; ok {
		return v
	}
	if t := e.attrs["type"]; t == "checkbox" || t == "radio" {
		return "on"
	}
	return ""
}
