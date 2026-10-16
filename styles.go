package loom

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"

	"github.com/moomerman/loom/render"
)

// Stylesheets (docs/components.md). Each route links one stylesheet: the
// design tokens and scoped CSS of the components it can render (its page,
// its layouts, and every component they call). The compiler records each
// component's CSS and callees, so the set is known at startup without
// rendering. The stylesheet's URL is its content hash, so browsers cache
// it forever and a changed style gets a new URL.

// stylesheets holds every route's stylesheet, by URL.
type stylesheets struct {
	mu    sync.Mutex
	byURL map[string]string
}

// stylesheetPrefix is where stylesheets are served.
const stylesheetPrefix = "/_loom/css/"

// add stores css and returns its URL, or "" if css is empty.
func (s *stylesheets) add(css string) string {
	if css == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(css))
	url := stylesheetPrefix + hex.EncodeToString(sum[:8]) + ".css"
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byURL == nil {
		s.byURL = map[string]string{}
	}
	s.byURL[url] = css
	return url
}

func (s *stylesheets) get(url string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	css, ok := s.byURL[url]
	return css, ok
}

// styledScope returns the route's render scope with its stylesheet and
// behaviors script, computing them on first use (the root layout and the
// scope may be set after the route is added).
func (rt *route) styledScope() *render.Scope {
	rt.styleOnce.Do(func() {
		var tmpls []render.Template
		if root := rt.group.rootLayout(); root != nil {
			tmpls = append(tmpls, root.tmpl)
		}
		if l := rt.group.appLayout(); l != nil {
			tmpls = append(tmpls, l.tmpl)
		}
		tmpls = append(tmpls, rt.tmpl)
		s := render.Scope{}
		if rt.scope != nil {
			s = *rt.scope
		}
		s.Stylesheet = rt.group.app.styles.add(render.Stylesheet(tmpls...))
		s.Script = rt.group.app.routeScript(render.Behaviors(tmpls...))
		s.LoomJS, s.NavigateJS = rt.group.app.clientScripts()
		rt.styled = &s
	})
	return rt.styled
}

// serveStylesheet serves a route's stylesheet. Its URL changes whenever
// its content does, so it can be cached for good.
func (a *App) serveStylesheet(w http.ResponseWriter, r *http.Request) {
	css, ok := a.styles.get(r.URL.Path)
	if !ok || !strings.HasSuffix(r.URL.Path, ".css") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write([]byte(css))
}
