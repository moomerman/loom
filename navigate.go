package loom

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Live navigation (docs/layouts.md). Following a link between two pages
// with the same root layout swaps only the live root:
// loom.js fetches the new page with the X-Loom-Navigate header, naming
// the session of the page it leaves, and if the server answers "swap" it
// takes the new page's live root, title, stylesheet and scripts from the
// document and pushes the URL to the history. The root layout, which
// holds no per-user data, stays as it is.
//
// The server decides before running anything: a path that isn't a page
// (a plain handler, an asset, a dev endpoint), a page with a different
// root layout, or a session it doesn't know (expired) gets "reload", and
// the browser loads the URL as an ordinary navigation. So no handler runs
// twice, and anything live navigation can't swap still works.
//
// A remount is a navigation to the same URL leaving no session ("-"):
// the server lost the page's session (it restarted), so the browser asks
// for a new one, keeping its document and the user's form input. The
// server can't know the browser's root layout, so it swaps any page, and
// the browser compares the root layout's fingerprint in the new page's
// loom-tree (see View.Document) with its own, reloading if they differ.

// navigateHeader is the request header of a live navigation, holding the
// session the browser leaves, and the response header with the answer:
// "swap" or "reload".
const navigateHeader = "X-Loom-Navigate"

// navigable reports whether a navigation request can swap the live root:
// its path is a page, and the session it leaves has the same root layout
// or it is a remount.
func (a *App) navigable(r *http.Request) bool {
	_, pattern := a.mux.Handler(r)
	rt := a.routesByPattern[pattern]
	if rt != nil && r.Header.Get(navigateHeader) == "-" {
		return true
	}
	from := a.pageSession(r, r.Header.Get(navigateHeader))
	return rt != nil && from != nil && from.route.group.rootLayout() == rt.group.rootLayout()
}

// serveNavigateReload answers a navigation the live root can't swap.
func serveNavigateReload(w http.ResponseWriter) {
	w.Header().Set(navigateHeader, "reload")
	w.Header().Set("Vary", navigateHeader)
	w.WriteHeader(http.StatusNoContent)
}

// Navigate sends the browser to url after the current Mount, event
// handler or HandleInfo returns: a live navigation if url is a page
// with the same root layout, else an ordinary page load. In Mount, the
// page load answers with a redirect instead. The page's session ends
// when the browser leaves it.
//
//	func (p *NewIssuePage) create(c *loom.Ctx, payload loom.Payload) {
//		id := p.save(c, payload)
//		c.Navigate(c.Path("/issues/" + id))
//	}
func (c *Ctx) Navigate(url string) { c.navigate = url }

// ErrPageNotFound is the error NewView returns for a page whose Mount
// called Ctx.NotFound.
var ErrPageNotFound = errors.New("page not found")

// NotFound ends the page load with 404 Not Found and the app's error
// page, for a page whose URL names nothing, such as /tracks/{id} with no
// such track. Call it in Mount (or the HandleParams that runs with
// Mount) and return; Assigns doesn't run. A live navigation to the page
// loads the error page like a link would. Called later, in a handler,
// it is RT448: the page has loaded and can't answer 404.
//
//	func (p *TrackPage) Mount(c *loom.Ctx) {
//		t, err := p.store.Track(c.Context(), c.Request.PathValue("slug"))
//		if errors.Is(err, loom.ErrNotFound) {
//			c.NotFound()
//			return
//		}
//		...
//	}
func (c *Ctx) NotFound() { c.notFound = true }

// Navigation returns the URL the page asked to navigate to with
// Ctx.Navigate since the last call, or "".
func (v *View) Navigation() string {
	url := v.ctx.navigate
	v.ctx.navigate = ""
	return url
}

// navigate tells the session's browsers to go to url.
func (s *session) navigate(url string) {
	data, err := json.Marshal(map[string]string{"to": url})
	if err != nil {
		panic(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcast(message{event: "navigate", data: data})
}
