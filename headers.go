package loom

import (
	"net/http"
	"strings"
)

// Security headers (docs/live.md). Every response carries a
// Content-Security-Policy (Config.CSP), X-Content-Type-Options: nosniff
// and Referrer-Policy: strict-origin-when-cross-origin, and, when
// Config.BaseURL is https, Strict-Transport-Security. Loom renders no
// inline script (events are on:* attributes, client code is in files,
// a page's tree is JSON data), so the default policy needs no nonces;
// `loom check` warns about inline script in templates (TC353).

// DefaultCSP is Config.CSP's default. Style attributes are allowed
// (style={...} is part of the template language, and CSS can't run
// script); scripts, connections, frames and forms are the app's own.
const DefaultCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// hstsHeader asks browsers to use https for two years.
const hstsHeader = "max-age=63072000"

// securityHeaders sets the security headers on w. The dev endpoints and
// preview gallery, served only in dev, render their own inline script
// and styles, so they get no policy.
func (a *App) securityHeaders(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	if a.hsts() {
		h.Set("Strict-Transport-Security", hstsHeader)
	}
	if p := r.URL.Path; strings.HasPrefix(p, "/_loom/dev/") || p == "/_loom/preview" || strings.HasPrefix(p, "/_loom/preview/") {
		return
	}
	h.Set("Content-Security-Policy", a.cfg.CSP)
}

// hsts reports whether responses carry Strict-Transport-Security: the
// app's public address is https and it isn't in dev mode.
func (a *App) hsts() bool { return strings.HasPrefix(a.cfg.BaseURL, "https://") && !a.dev }
