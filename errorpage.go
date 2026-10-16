package loom

import (
	"fmt"
	"html"
	"net/http"
	"reflect"
	"sync"

	"github.com/moomerman/loom/render"
)

// PageError is what an error page shows: never the error itself, which
// may hold file paths, SQL or a panic value. The detail goes to the log
// and `loom errors`, where Code finds it.
type PageError struct {
	Status int    // the HTTP status: 403, 404, 429, 500 or 503
	Title  string // a sentence for people, e.g. "This page doesn't exist."
	Code   string // the Loom error code, e.g. RT444; "" if there is none
}

// errorTitles are the sentences each status shows.
var errorTitles = map[int]string{
	http.StatusForbidden:           "You don't have access to this page.",
	http.StatusNotFound:            "This page doesn't exist.",
	http.StatusTooManyRequests:     "Too many attempts. Wait a minute, then try again.",
	http.StatusInternalServerError: "Something went wrong loading this page.",
	http.StatusServiceUnavailable:  "The server is busy. Try again in a moment.",
}

// errorPage is the component and assigns func set with ErrorPage.
type errorPage struct {
	tmpl render.Template
	fn   reflect.Value // func(PageError) A

	mu     sync.Mutex
	routes map[*Group]*route // tmpl in each group's root layout, for its styles and assets
}

// routeIn returns the error page as a route of g, so it renders in g's
// root layout with g's assets and styles.
func (ep *errorPage) routeIn(g *Group) *route {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	rt := ep.routes[g]
	if rt == nil {
		rt = &route{tmpl: ep.tmpl, group: g, scope: g.scope()}
		ep.routes[g] = rt
	}
	return rt
}

// ErrorPage sets the component a failed page load shows: a page that
// doesn't exist (404), an auth check that refuses (403), a page that
// fails to mount or render (500), too many attempts (429, RT436), or too
// many open pages (503). It renders inside the root layout of the route
// that failed (the app's, for a URL no route matches), with that route's
// assets, as a plain page with no session. assigns is a
// func(loom.PageError) A, where A is the component's assigns type; a
// wrong one panics with RT446 at startup.
//
//	app.ErrorPage(templates.Oops, func(e loom.PageError) templates.OopsAssigns {
//		return templates.OopsAssigns{Title: e.Title, Code: e.Code}
//	})
//
// Without one, a plain page with the same sentence is shown. In dev, a
// failure shows the error itself instead, with its fix.
func (a *App) ErrorPage(tmpl render.Template, assigns any) {
	if tmpl == nil {
		panic(&Error{Code: RT446, Msg: "the error page component is nil", Where: callerOutsideLoom(), Fix: "pass a compiled component, e.g. templates.Oops"})
	}
	name, at := qualified(tmpl), tmpl.AssignsType()
	if tmpl.IsRootLayout() {
		panic(&Error{Code: RT446, Msg: name + " is a whole document, but the error page renders inside the root layout", Where: callerOutsideLoom(),
			Fix: "pass a component without <html>; the app's root layout wraps it"})
	}
	fn := reflect.ValueOf(assigns)
	if assigns == nil || fn.Kind() != reflect.Func || fn.Type().NumIn() != 1 || fn.Type().In(0) != reflect.TypeFor[PageError]() || fn.Type().NumOut() != 1 || fn.Type().Out(0) != at {
		got := "nil"
		if assigns != nil {
			got = fn.Type().String()
		}
		panic(&Error{Code: RT446, Msg: fmt.Sprintf("the assigns func for error page %s is %s, but must be func(loom.PageError) %s", name, got, at), Where: callerOutsideLoom(),
			Fix: fmt.Sprintf("pass func(e loom.PageError) %s { return %s{...} }", at, at)})
	}
	a.errorPage = &errorPage{tmpl: tmpl, fn: fn, routes: map[*Group]*route{}}
}

// serveError answers a failed request. In dev it shows err, with its
// code and fix, for whoever is developing; otherwise the error page,
// which never does, in the root layout of g, the group of the route
// that failed (nil for none: the app's). The caller has logged err.
func (a *App) serveError(w http.ResponseWriter, r *http.Request, g *Group, status int, err error) {
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "10")
	}
	if a.dev && err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		fmt.Fprintf(w, "loom: this page failed to load.\n\n%v\n", err)
		return
	}
	info := PageError{Status: status, Title: errorTitles[status]}
	if e, ok := err.(*Error); ok {
		info.Code = e.Code
	}
	doc, rerr := a.renderErrorPage(r, g, info)
	if rerr != nil {
		a.logError("", fmt.Errorf("rendering the error page: %w", rerr))
		doc = plainErrorPage(info)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprint(w, doc)
}

// renderErrorPage renders the ErrorPage component in g's root layout,
// or the plain page if the app has none.
func (a *App) renderErrorPage(r *http.Request, g *Group, info PageError) (doc string, err error) {
	ep := a.errorPage
	if ep == nil {
		return plainErrorPage(info), nil
	}
	if g == nil {
		g = a.top
	}
	rt := ep.routeIn(g)
	defer func() {
		if p := recover(); p != nil {
			err = panicError("rendering the error page", p)
		}
	}()
	scope := rt.styledScope()
	inner, err := rt.tmpl.RenderAssigns(scope, ep.fn.Call([]reflect.Value{reflect.ValueOf(info)})[0].Interface())
	if err != nil {
		return "", err
	}
	root := g.rootLayout()
	if root == nil {
		return inner.HTML(), nil
	}
	c := &Ctx{Request: r, app: a, scope: scope, ctx: r.Context(), title: info.Title}
	page, err := root.render(c, scope, func() *render.Rendered { return inner }, nil)
	if err != nil {
		return "", err
	}
	return page.HTML(), nil
}

// plainErrorPage is the error page of an app that sets none.
func plainErrorPage(info PageError) string {
	code := ""
	if info.Code != "" {
		code = "<p><small>" + html.EscapeString(info.Code) + "</small></p>"
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>%d</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1rem;color:#222;background:#fff}@media (prefers-color-scheme:dark){body{color:#ddd;background:#111}}</style>
</head><body><h1>%s</h1>%s</body></html>
`, info.Status, html.EscapeString(info.Title), code)
}
