package loom

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
)

// URL patching (docs/live.md). A page whose state belongs in the URL
// (a filter, a tab, a search) implements ParamsHandler: HandleParams
// reads the state from the URL after Mount, and again whenever the URL
// changes within the page's route. The page changes the URL with c.Patch, and the browser
// pushes it to the history without a page load or a new session.
//
// A link to a URL of the same route with the same path values (only the
// query differs), and the back and forward buttons between such URLs,
// patch too: navigate.js asks to navigate as usual,
// the server sees that the URL is the session's own route and the page
// handles params, runs HandleParams on the running session and answers
// "patch" instead of "swap". So the page keeps everything the URL
// doesn't hold (an open menu, a draft), the URL is shareable and
// bookmarkable, and reloading it renders the same state. A link that
// changes a path value (/team/DES to /team/ENG) mounts the page afresh,
// as a link to another route does: Mount loads what the path names, and
// a patch never leaves the page showing the old path's data.

// ParamsHandler is a page whose state is (partly) in its URL.
type ParamsHandler interface {
	// HandleParams runs after Mount, and again each time the URL's
	// query changes: c.Patch, a link to the same path, or the back and
	// forward buttons. query is the URL's query; c.Request.URL is the
	// new URL. The path values stay as Mount saw them. It sets
	// the page's state from the URL; the runtime then calls Assigns and
	// renders. It runs on the page's goroutine like an event handler.
	HandleParams(c *Ctx, query url.Values)
}

// PatchOption changes how Ctx.Patch changes the URL.
type PatchOption func(*patch)

// Replace makes Ctx.Patch replace the current history entry instead of
// adding one, for changes the back button shouldn't step through one by
// one, such as each keystroke of a search.
func Replace() PatchOption { return func(p *patch) { p.replace = true } }

// patch is a URL change the page asked for with Ctx.Patch.
type patch struct {
	url     string
	replace bool
}

// Patch changes the page's URL to url, the same path with another query
// (relative URLs like "?view=active" resolve against the current one),
// after the current event handler or HandleInfo returns: the page's HandleParams runs with the new URL, the page
// re-renders, and the browser pushes the URL to its history (or
// replaces the current entry, with loom.Replace()). The session and the
// rest of the page's state stay as they are. Build the URL from the
// state it should hold, and let HandleParams set that state:
//
//	func (p *TeamPage) pick(c *loom.Ctx, payload loom.Payload) {
//		q := p.query()
//		q.Set("priority", payload["value"])
//		c.Patch(c.Path("/team/" + p.key + "?" + q.Encode()))
//	}
//
// A URL of another route or with other path values is RT426 and a page
// without HandleParams is RT427; either way the page reports the error
// and navigates to url instead. In Mount or HandleParams on a page load, Patch redirects the
// load to url, as Navigate does.
func (c *Ctx) Patch(url string, opts ...PatchOption) {
	p := &patch{url: url}
	for _, o := range opts {
		o(p)
	}
	c.patch = p
}

// Patched returns the URL the page patched to with Ctx.Patch since the
// last call, and whether it replaces the current history entry, or ""
// if it hasn't.
func (v *View) Patched() (url string, replace bool) {
	p := v.patched
	v.patched = nil
	if p == nil {
		return "", false
	}
	return p.url, p.replace
}

// URL returns the page's current URL, path and query, as a browser
// shows it: the URL it was loaded at, or the last one patched to.
func (v *View) URL() string { return v.ctx.Request.URL.RequestURI() }

// HandlesParams reports whether the page implements ParamsHandler.
func (v *View) HandlesParams() bool { return v.page.params != nil }

// Patch changes the page's URL as a browser does when it follows a link
// to another URL of the page's route, or goes back or forward to one:
// it runs the route's auth checks, then the page's HandleParams with the
// new URL, and re-renders. It returns the diff to send to the client,
// or nil if nothing changed. The URL must be of the page's route, and
// the page must implement ParamsHandler (RT427).
func (v *View) Patch(target string) (map[string]any, error) {
	if v.lent {
		return nil, nil
	}
	v.begin("")
	if err := authorize(v.auth, v.ctx); err != nil {
		return nil, err
	}
	if v.page.params == nil {
		return nil, v.noParamsHandler(target)
	}
	r, err := v.patchRequest(target)
	if err != nil {
		return nil, err
	}
	what := fmt.Sprintf("HandleParams(%q)", r.URL.RequestURI())
	assigns, lassign, err := v.call(v.page, what, func() {
		v.ctx.Request = r
		v.page.params.HandleParams(v.ctx, r.URL.Query())
		v.applyPatches()
	})
	return v.update(assigns, lassign, what, err)
}

// patchRequest returns the page's request with its URL changed to
// target, or RT426 if target isn't a URL of the page's route.
func (v *View) patchRequest(target string) (*http.Request, error) {
	u, err := v.ctx.Request.URL.Parse(target)
	if err == nil && (u.Scheme != "" || u.Host != "") {
		err = fmt.Errorf("it names a host")
	}
	if err != nil {
		return nil, &Error{Code: RT426, Msg: fmt.Sprintf("can't patch the URL of %T to %q: %v", v.page.page, target, err),
			Fix: `patch to a path of the page's own route, such as c.Path("/team/ENG?view=active") or "?view=active"; use c.Navigate to go elsewhere`}
	}
	u.Fragment, u.RawFragment = "", ""
	r := v.ctx.Request.Clone(v.ctx.Request.Context())
	r.URL, r.RequestURI = u, u.RequestURI()
	if v.route != nil {
		app := v.route.group.app
		_, pattern := app.mux.Handler(r)
		if rt := app.routesByPattern[pattern]; rt != v.route {
			other := "no live route"
			if rt != nil {
				other = "the route " + rt.path
			}
			return nil, &Error{Code: RT426,
				Msg: fmt.Sprintf("%T patched its URL to %s, which is %s, not the page's own %s; a patch keeps the page, so it only changes the URL within the route", v.page.page, u.Path, other, v.route.path),
				Fix: "to go to another page, use c.Navigate(url); to patch, give a URL that matches " + v.route.path}
		}
		if from, to := pathValues(v.route.path, v.ctx.Request.URL.Path), pathValues(v.route.path, u.Path); !slices.Equal(from, to) {
			return nil, &Error{Code: RT426,
				Msg: fmt.Sprintf("%T patched its URL to %s, which changes %s; a patch keeps the page, which Mount loaded for %s, so it only changes the query", v.page.page, u.Path, describePathValues(from, to), v.ctx.Request.URL.Path),
				Fix: "to show the page for another path value, use c.Navigate(url), which mounts it afresh; to patch, keep the path and change the query"}
		}
		setPathValues(r, v.route.path)
	}
	return r, nil
}

// handleParams runs the page's HandleParams, if it has one, with the
// page's URL: after Mount.
func (v *View) handleParams() {
	if v.page.params != nil {
		v.page.params.HandleParams(v.ctx, v.ctx.Request.URL.Query())
	}
}

// applyPatches carries out the c.Patch calls of an event handler,
// HandleInfo or HandleParams: each changes the page's URL and runs
// HandleParams again, which may patch once more. A patch that can't be
// done is reported (RT426, RT427) and becomes a navigation. It runs
// inside View.call, so a panic in HandleParams restarts the page.
func (v *View) applyPatches() {
	for i := 0; v.ctx.patch != nil; i++ {
		p := v.ctx.patch
		v.ctx.patch = nil
		var err error
		switch {
		case i == 10:
			err = &Error{Code: RT426, Msg: fmt.Sprintf("%T kept patching its URL from HandleParams, last to %q", v.page.page, p.url),
				Fix: "call c.Patch in HandleParams only to correct a URL, so the corrected URL doesn't patch again"}
		case v.page.params == nil:
			err = v.noParamsHandler(p.url)
		}
		var r *http.Request
		if err == nil {
			r, err = v.patchRequest(p.url)
		}
		if err != nil {
			v.ctx.failed = append(v.ctx.failed, err)
			v.ctx.navigate = p.url
			return
		}
		v.ctx.Request = r
		v.patched = &patch{url: r.URL.RequestURI(), replace: p.replace || (v.patched != nil && v.patched.replace)}
		v.page.params.HandleParams(v.ctx, r.URL.Query())
	}
}

func (v *View) noParamsHandler(target string) error {
	return &Error{Code: RT427,
		Msg: fmt.Sprintf("%T can't patch its URL to %q: it has no HandleParams method to read the new URL", v.page.page, target),
		Fix: fmt.Sprintf("add func (p %T) HandleParams(c *loom.Ctx, query url.Values), setting the page's state from the URL; or use c.Navigate(url) to load the page afresh", v.page.page)}
}

// checkParamsHandler panics with RT428 if the page has a HandleParams
// method that doesn't implement ParamsHandler, which the runtime would
// otherwise never call.
func checkParamsHandler(page Page, where string) {
	if _, ok := page.(ParamsHandler); ok {
		return
	}
	m, ok := reflect.TypeOf(page).MethodByName("HandleParams")
	if !ok {
		return
	}
	got := strings.Replace(m.Type.String(), "func(*"+reflect.TypeOf(page).Elem().String()+", ", "func(", 1)
	panic(&Error{Code: RT428, Where: where,
		Msg: fmt.Sprintf("%T has HandleParams of type %s, so the runtime would never call it", page, got),
		Fix: fmt.Sprintf("declare it as func (p %T) HandleParams(c *loom.Ctx, query url.Values)", page)})
}

// patchable reports whether a navigation request is a patch of the
// session it leaves: its URL is of that session's route with the same
// path values, and the page handles params. It returns the session, or nil.
func (a *App) patchable(r *http.Request) *session {
	s := a.pageSession(r, r.Header.Get(navigateHeader))
	if s == nil || !s.handlesParams {
		return nil
	}
	_, pattern := a.mux.Handler(r)
	if a.routesByPattern[pattern] != s.route || !slices.Equal(pathValues(s.route.path, r.URL.Path), s.pathValues) {
		return nil
	}
	return s
}

// describePathValues names the path values that differ between from and
// to, such as `{key} from "DES" to "ENG"`.
func describePathValues(from, to [][2]string) string {
	var diffs []string
	for i := range min(len(from), len(to)) {
		if from[i] != to[i] {
			diffs = append(diffs, fmt.Sprintf("{%s} from %q to %q", to[i][0], from[i][1], to[i][1]))
		}
	}
	if len(diffs) == 0 {
		return "its path values"
	}
	return strings.Join(diffs, " and ")
}

// servePatch answers a navigation that patches the session's URL: the
// session runs HandleParams with the new URL and sends its diff on the
// stream, and the answer tells the browser to keep the page and push
// the URL. An auth check that refuses ends the session, and the browser
// loads the URL, which answers 403 or redirects.
func (a *App) servePatch(w http.ResponseWriter, r *http.Request, s *session) {
	done := make(chan eventResult, 1)
	select {
	case s.events <- pageEvent{patch: r.URL.RequestURI(), done: done}:
	case <-s.quit:
		serveNavigateReload(w)
		return
	}
	var res eventResult
	select {
	case res = <-done:
	case <-s.quit:
		serveNavigateReload(w)
		return
	}
	if res.err != nil {
		serveNavigateReload(w)
		return
	}
	w.Header().Set(navigateHeader, "patch")
	w.Header().Set("Vary", navigateHeader)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"v": res.version, "url": res.url})
}

// patched tells the session's browsers that the page patched its URL.
func (s *session) patched(url string, replace bool) {
	data, err := json.Marshal(map[string]any{"to": url, "replace": replace})
	if err != nil {
		panic(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcast(message{event: "patch", data: data})
}
