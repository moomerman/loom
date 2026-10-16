package loom

import (
	"net/http"
	"net/url"
)

// Cross-site request forgery (docs/auth.md). The app refuses every
// request with an unsafe method (POST, PUT, PATCH, DELETE) that a
// browser sent from another origin: a live event, a form post to a
// plain handler, a sign-out. Nothing to add to forms, and nothing to
// forget: the check is in ServeHTTP, in front of every route.
//
// Browsers say where a request came from in Sec-Fetch-Site (every
// current browser) or, before that, Origin. A request with neither
// didn't come from a browser page (curl, a webhook, the loom CLI), so a
// forged one can't carry the user's cookies, and it is allowed. This is
// the check Go 1.25's http.CrossOriginProtection makes, written out here
// from when Loom supported Go 1.24. Session cookies are SameSite=Lax as well,
// and live events also need the page's unguessable session id.

// crossOrigin reports whether r is a browser request with an unsafe
// method from another origin.
func crossOrigin(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none": // "none": the user typed or bookmarked it
		return false
	case "":
	default: // "cross-site" or "same-site" (another subdomain)
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	return err != nil || u.Host != r.Host
}

// crossSiteNavigation reports whether a browser came to r from a page
// of another site, for a GET that acts (redeeming a sign-in grant).
// Browsers send no Origin on a plain navigation, so only Sec-Fetch-Site
// says; with none (an old browser, curl, loomtest) it is allowed.
func crossSiteNavigation(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return false
	}
	return true
}

// refuseCrossOrigin answers a cross-origin request with RT429. It is
// listed by `loom errors` without the request's path or origin, which
// another site chooses: agents read that list.
func (a *App) refuseCrossOrigin(w http.ResponseWriter, r *http.Request) {
	e := &Error{Code: RT429, Msg: "a " + r.Method + " request from another site was refused",
		Fix: "post forms and events from the app's own pages; a page on another site can't act for a signed-in user"}
	a.logError("", e)
	writeError(w, http.StatusForbidden, e)
}
