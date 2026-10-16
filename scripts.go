package loom

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/moomerman/loom/client"
	"github.com/moomerman/loom/render"
)

// Behavior scripts (docs/components.md). A route whose components attach
// behaviors loads one module: it imports the behaviors runtime and each
// behavior's module, and registers them. Like stylesheets, the set is
// known at startup from the compiled components, and every module is
// served at a content-hashed URL, cached for good. Modules are served as
// written: no bundler, no build step.

// scriptPrefix is where behavior modules are served.
const scriptPrefix = "/_loom/js/"

// scripts holds the modules routes load, by URL.
type scripts struct {
	mu    sync.Mutex
	byURL map[string]string
}

// add stores a module and returns its URL.
func (s *scripts) add(js string) string {
	sum := sha256.Sum256([]byte(js))
	url := scriptPrefix + hex.EncodeToString(sum[:8]) + ".js"
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byURL == nil {
		s.byURL = map[string]string{}
	}
	s.byURL[url] = js
	return url
}

func (s *scripts) get(url string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	js, ok := s.byURL[url]
	return js, ok
}

// clientScripts returns the content-hashed URLs of loom.js (with dev.js
// in dev) and navigate.js, which root layouts load. A page's tree names
// them (clientVersion), so a remount after a deploy that changed them
// reloads instead of running old scripts against the new server.
func (a *App) clientScripts() (loomJS, navigateJS string) {
	a.clientOnce.Do(func() {
		js := string(client.JS)
		if a.dev {
			js += string(client.Dev) // reports script errors to `loom errors`
		}
		a.loomJS, a.navigateJS = a.scripts.add(js), a.scripts.add(string(client.Navigate))
	})
	return a.loomJS, a.navigateJS
}

// routeScript returns the URL of the module that loads the behaviors bs,
// or "" if there are none. In dev, the runtime also checks emitted events
// and the requirements the compiler left to the browser.
func (a *App) routeScript(bs []*render.Behavior) string {
	if len(bs) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "import { define } from %q;\n", a.scripts.add(string(client.Behaviors)))
	for i, bh := range bs {
		fmt.Fprintf(&b, "import * as b%d from %q;\n", i, a.scripts.add(bh.JS))
	}
	b.WriteString("define({\n")
	for i, bh := range bs {
		emits := make([]string, len(bh.Emits))
		for j, e := range bh.Emits {
			emits[j] = fmt.Sprintf("%q", e)
		}
		fmt.Fprintf(&b, "  %q: [b%d, [%s]],\n", bh.ID, i, strings.Join(emits, ", "))
	}
	fmt.Fprintf(&b, "}, %t);\n", a.dev)
	return a.scripts.add(b.String())
}

// serveScript serves a behavior module. Its URL changes whenever its
// content does, so it can be cached for good.
func (a *App) serveScript(w http.ResponseWriter, r *http.Request) {
	js, ok := a.scripts.get(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write([]byte(js))
}
