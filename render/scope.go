package render

import (
	"fmt"
	"path"
	"strings"
)

// Scope is what a render needs to know beyond its assigns: where the page
// is mounted. The path and asset builtins read it, so a package of
// templates (a sub-app) links correctly wherever it is mounted. Generated
// code passes the scope down to every component it calls. A nil Scope is
// the root of the app with no assets, as in loomtest.Render.
type Scope struct {
	// Prefix is the mount prefix of the page's route, e.g. "/admin/jobs",
	// or "" outside any mount.
	Prefix string
	// Asset returns the URL path of an embedded asset by its file name,
	// e.g. "app.css" -> "/admin/jobs/_assets/app.3f2a1b9c.css", and false
	// if there is no such asset.
	Asset func(name string) (string, bool)
	// Stylesheet is the URL of the page's stylesheet, linked by a root
	// layout's <.loom_styles />, or "" if its components have no styles.
	Stylesheet string
	// Script is the URL of the page's behaviors module, loaded by a root
	// layout's <.loom_scripts />, or "" if its components attach none.
	Script string
	// LoomJS and NavigateJS are the URLs of loom.js and navigate.js,
	// loaded by <.loom_scripts />, content-hashed so browsers cache them
	// for good and a deploy that changes them is seen; "" for the fixed
	// /_loom/loom.js and /_loom/navigate.js.
	LoomJS, NavigateJS string
	// Meta is the page's, rendered by a root layout's <.live_meta />. The
	// runtime sets it on a copy of the route's Scope for each document.
	Meta Meta
}

// Path is the path builtin: p, an absolute path within the mount, as a
// path from the site root. path("/jobs/1") is "/admin/jobs/jobs/1" when
// mounted at /admin/jobs, and path("/") is "/admin/jobs".
func Path(s *Scope, p string) string {
	if s == nil || s.Prefix == "" {
		return p
	}
	switch {
	case p == "/":
		return s.Prefix
	case strings.HasPrefix(p, "/?"), strings.HasPrefix(p, "/#"):
		return s.Prefix + p[1:]
	}
	return s.Prefix + p
}

// Asset is the asset builtin: the URL of an asset embedded by the page's
// route group (loom.Assets), with its content hash in the name so it can
// be cached forever. Without assets (a nil Scope, as in tests) it is the
// unhashed path under the mount's _assets directory. A name ending in
// "/" is a directory, whose URL ends in "/" too. An unknown name
// panics, which a live page reports as an error with the names it has.
func Asset(s *Scope, name string) string {
	if s == nil || s.Asset == nil {
		p := "/_assets/" + path.Clean(name)
		if strings.HasSuffix(name, "/") {
			p += "/"
		}
		return Path(s, p)
	}
	url, ok := s.Asset(name)
	if !ok {
		panic(fmt.Sprintf("asset(%q): no asset named %q is embedded in this route's group; add the file to the fs.FS given to loom.Assets, or fix the name", name, name))
	}
	return url
}
