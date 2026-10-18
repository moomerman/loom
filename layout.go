package loom

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/moomerman/loom/render"
)

// Layouts (docs/layouts.md). A root layout is the HTML document a page is
// served in: rendered once per page load and never diffed, except for
// the <.live_title>. An app layout is chrome around the page (nav, user
// name) rendered inside the live tree, so it is diffed with the page.
// Each is a component plus a func computing its assigns from the page's
// Ctx; the framework fills in the component's inner_block slot.

//go:generate go run ./cmd/loom build default_root.hx

// layout is a layout component and its assigns func, func(*Ctx) A, or,
// for a live layout, the prototype of its state (see LiveLayout).
type layout struct {
	tmpl render.Template
	fn   reflect.Value
	live Page // a live layout's state, copied per page load; nil for a func
}

var ctxType = reflect.TypeOf((*Ctx)(nil))

// newLayout checks a layout and its assigns func, panicking with RT412 or
// RT413 at startup rather than failing on the first request.
func newLayout(root bool, tmpl render.Template, assigns any) *layout {
	checkLayoutComponent(root, tmpl)
	name, at := qualified(tmpl), tmpl.AssignsType()
	fn := reflect.ValueOf(assigns)
	ft := fn.Type()
	if assigns == nil || ft.Kind() != reflect.Func || ft.NumIn() != 1 || ft.In(0) != ctxType || ft.NumOut() != 1 || ft.Out(0) != at {
		got := "nil"
		if assigns != nil {
			got = ft.String()
		}
		panic(&Error{Code: RT412, Msg: fmt.Sprintf("the assigns func for layout %s is %s, but must be func(*loom.Ctx) %s", name, got, at), Where: callerOutsideLoom(),
			Fix: fmt.Sprintf("pass func(c *loom.Ctx) %s { return %s{...} }", at, at)})
	}
	return &layout{tmpl: tmpl, fn: fn}
}

// checkLayoutComponent panics with RT413 if tmpl can't be that kind of
// layout.
func checkLayoutComponent(root bool, tmpl render.Template) {
	if tmpl == nil {
		panic(&Error{Code: RT413, Msg: "the layout component is nil", Where: callerOutsideLoom(), Fix: "pass a compiled component, e.g. templates.Root"})
	}
	name := qualified(tmpl)
	switch {
	case root && !tmpl.IsRootLayout():
		panic(&Error{Code: RT413, Msg: name + " is not a root layout: it has no <html> document", Where: callerOutsideLoom(),
			Fix: "pass a component with a whole document as the root layout, or use loom.Layout for a layout inside the page"})
	case !root && tmpl.IsRootLayout():
		panic(&Error{Code: RT413, Msg: name + " is a root layout, so it can't be an app layout", Where: callerOutsideLoom(),
			Fix: "use app.RootLayout or loom.RootLayout for it; loom.Layout takes a component that renders inside the page"})
	}
	if f, ok := tmpl.AssignsType().FieldByName("InnerBlock"); !ok || f.Type != reflect.TypeOf(render.Slot(nil)) {
		panic(&Error{Code: RT413, Msg: "layout " + name + " has no inner_block slot for the page", Where: callerOutsideLoom(),
			Fix: "add inner_block slot to its signature and render {render_slot(inner_block)} where the page goes"})
	}
}

// render renders the layout around inner, with assigns from its assigns
// func, or the live layout's assigns if given.
func (l *layout) render(c *Ctx, s *render.Scope, inner render.Slot, live Assigns) (*render.Rendered, error) {
	a := reflect.New(l.tmpl.AssignsType()).Elem()
	if l.live != nil {
		a.Set(reflect.ValueOf(live))
	} else {
		a.Set(l.fn.Call([]reflect.Value{reflect.ValueOf(c)})[0])
	}
	a.FieldByName("InnerBlock").Set(reflect.ValueOf(inner))
	return l.tmpl.RenderAssigns(s, a.Interface())
}

func (l *layout) name() string {
	if l == nil {
		return ""
	}
	return qualified(l.tmpl)
}

// defaultRoot is the root layout of apps that don't set one:
// default_root.hx, titled App.Title until a page sets its own.
func (a *App) defaultRoot() *layout {
	return newLayout(true, DefaultRoot, func(c *Ctx) DefaultRootAssigns {
		return DefaultRootAssigns{Title: c.TitleOr(""), DefaultTitle: a.cfg.Title}
	})
}

// RootLayout sets the root layout of every route, except in groups that
// set their own with the loom.RootLayout option. assigns is a func(c
// *loom.Ctx) A, where A is the layout's assigns type: it runs once per
// page load, after Mount. Pass only request-scoped data, never per-user
// data, so the document stays cacheable (see docs/layouts.md).
//
//	app.RootLayout(templates.Root, func(c *loom.Ctx) templates.RootAssigns {
//		return templates.RootAssigns{Title: c.TitleOr("Todos")}
//	})
func (a *App) RootLayout(tmpl render.Template, assigns any) {
	a.top.root = newLayout(true, tmpl, assigns)
}

// Group returns a route group: routes added with its Live share its
// options. Groups nest; the nearest group's layout applies, and layouts
// don't stack (an inner layout that wants the outer one calls it). Auth
// checks and middleware accumulate, outer first (see mount.go).
//
//	g := app.Group(loom.Layout(templates.App, appAssigns))
//	g.Live("/", &TodoPage{}, templates.TodoApp)
func (a *App) Group(opts ...Option) *Group { return a.top.Group(opts...) }

// Group is a set of routes sharing options: layouts, auth checks,
// middleware and assets. A group made by Mount also has a path prefix.
type Group struct {
	app    *App
	parent *Group
	prefix string  // the mount prefix, e.g. "/admin/jobs"; "" outside mounts
	mount  string  // the Go type of the sub-app mounted here, if any
	public bool    // the sub-app mounted here says it needs no auth (RequiresAuth false)
	root   *layout // the root layout, if this group sets one
	layout *layout // the app layout, if this group sets one

	auth       []AuthFunc                        // this group's own checks
	limits     []*limit                          // this group's own rate limits
	middleware []func(http.Handler) http.Handler // this group's own middleware
	assets     *assets                           // this group's own embedded assets
	dev        bool                              // DevOnly (dev.go)
}

// Group returns a group nested in g.
func (g *Group) Group(opts ...Option) *Group {
	child := &Group{app: g.app, parent: g, prefix: g.prefix}
	child.apply(opts)
	return child
}

func (g *Group) apply(opts []Option) {
	for _, o := range opts {
		o.apply(g)
	}
	if g.assets != nil {
		g.serveAssets()
	}
}

// Live serves a live page at path, like App.Live, with the group's
// options. In a mounted group, path is relative to the mount prefix:
// "/" is the prefix itself.
func (g *Group) Live(path string, page Page, tmpl render.Template, opts ...RouteOption) {
	checkPage(page, tmpl, g.appLayout())
	if !strings.HasPrefix(path, "/") {
		panic(&Error{Code: RT416, Msg: fmt.Sprintf("route path %q doesn't start with /", path), Where: callerOutsideLoom(), Fix: fmt.Sprintf("write %q", "/"+path)})
	}
	a := g.app
	full := joinPath(g.prefix, path)
	rt := &route{path: full, prototype: page, tmpl: tmpl, group: g, auth: g.authChain(), limits: g.limitChain(), scope: g.scope()}
	for _, o := range opts {
		o.applyRoute(rt)
	}
	checkLimitEvents(rt, func(name string) bool {
		if _, ok := eventsOf(page)[name]; ok {
			return true
		}
		if l := g.appLayout(); l != nil && l.live != nil {
			_, ok := eventsOf(l.live)[name]
			return ok
		}
		return false
	})
	pattern := "GET " + full
	if strings.HasSuffix(full, "/") {
		pattern += "{$}" // match the path exactly, not as a prefix
	}
	a.routes = append(a.routes, rt)
	a.routesByPattern[pattern] = rt
	a.mux.Handle(pattern, g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.servePage(w, r, rt) })))
}

// Handle serves a plain HTTP handler (a form post, a webhook, a sign-in
// that sets a cookie) at pattern, a method and a path such as
// "POST /login", with the group's auth checks and middleware. In a
// mounted group the path is relative to the mount prefix. An auth check
// that refuses answers as for a page load: 403 or a redirect.
//
//	app.Handle("POST /logout", http.HandlerFunc(logout))
func (g *Group) Handle(pattern string, h http.Handler) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok || method == "" || !strings.HasPrefix(path, "/") {
		panic(&Error{Code: RT416, Msg: fmt.Sprintf("handler pattern %q isn't a method and a path", pattern), Where: callerOutsideLoom(), Fix: `write the method and the path, e.g. "POST /login"`})
	}
	a, checks, limits, scope := g.app, g.authChain(), g.limitChain(), g.scope()
	full := joinPath(g.prefix, path)
	a.handlers = append(a.handlers, handler{pattern: method + " " + full, auth: checks, limits: limits, group: g})
	if strings.HasSuffix(full, "/") {
		full += "{$}"
	}
	a.mux.Handle(method+" "+full, g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := &Ctx{Request: r, app: a, scope: scope, ctx: r.Context()}
		if err := authorize(checks, c); err != nil {
			a.refuse(w, r, g, err.(*authError))
			return
		}
		if err := a.allow(c, limits, method+" "+full, "", nil); err != nil {
			a.refuseLimited(w, r, g, err.(*LimitError))
			return
		}
		h.ServeHTTP(w, r)
	})))
}

// handler is a plain HTTP handler's registration.
type handler struct {
	pattern string // method and full path, e.g. POST /login
	auth    []AuthFunc
	limits  []*limit
	group   *Group
}

// Handle serves a plain HTTP handler at pattern, like Group.Handle,
// outside any group.
func (a *App) Handle(pattern string, h http.Handler) { a.top.Handle(pattern, h) }

// rootLayout returns the root layout of the nearest group that sets one.
func (g *Group) rootLayout() *layout {
	for ; g != nil; g = g.parent {
		if g.root != nil {
			return g.root
		}
	}
	return nil // the top group always has one
}

// appLayout returns the app layout of the nearest group that sets one,
// or nil.
func (g *Group) appLayout() *layout {
	for ; g != nil; g = g.parent {
		if g.layout != nil {
			return g.layout
		}
	}
	return nil
}

// Option is a route group option: Layout, RootLayout, Auth, Middleware
// or Assets.
type Option interface{ apply(*Group) }

type layoutOption struct {
	root bool
	l    *layout
}

func (o layoutOption) apply(g *Group) {
	if o.root {
		g.root = o.l
	} else {
		g.layout = o.l
	}
}

// Layout is a group option setting the group's app layout: a component
// with an inner_block slot, rendered around each page inside the live
// tree. assigns is a func(c *loom.Ctx) A, where A is the layout's assigns
// type; it runs on every render of the page, so it may read values the
// page sets with c.Set.
//
//	loom.Layout(templates.App, func(c *loom.Ctx) templates.AppAssigns {
//		return templates.AppAssigns{User: currentUser(c.Request)}
//	})
func Layout(tmpl render.Template, assigns any) Option {
	return layoutOption{l: newLayout(false, tmpl, assigns)}
}

// LiveLayout is a group option setting the group's app layout, like
// Layout, but with state and events of its own: state is a pointer to a
// struct with the methods of a page, Mount, Assigns (returning the
// layout's assigns type; its inner_block is filled in) and, as needed,
// Events and HandleInfo. Each page load gets its own copy, mounted
// before the page and kept for as long as the page is open. Events the
// layout handles go to it, whichever markup sends them, so the pages in
// it handle only their own; an event both handle is RT434 at startup.
// A page reaches its layout's state with LayoutOf.
//
// Use it for chrome with state shared by every page: a composer, toasts,
// a command palette, unread counts.
//
//	g := app.Group(loom.LiveLayout(templates.Shell, &Shell{store: store}))
func LiveLayout(tmpl render.Template, state Page) Option {
	return layoutOption{l: newLiveLayout(tmpl, state)}
}

// newLiveLayout checks a live layout's component and state, panicking at
// startup: RT413 for a component that isn't an app layout, RT406 for
// state that isn't a pointer to a struct, RT412 for the wrong Assigns.
func newLiveLayout(tmpl render.Template, state Page) *layout {
	checkLayoutComponent(false, tmpl)
	if _, err := clonePage(state); err != nil {
		e := *err.(*Error)
		e.Where, e.Msg = callerOutsideLoom(), fmt.Sprintf("a live layout's state must be a pointer to a struct, got %T", state)
		e.Fix = "pass a pointer, e.g. loom.LiveLayout(templates.Shell, &Shell{})"
		panic(&e)
	}
	checkKeeper(state, callerOutsideLoom())
	name, want := qualified(tmpl), tmpl.AssignsType()
	m, ok := reflect.TypeOf(state).MethodByName("Assigns")
	if !ok || m.Type.NumIn() != 2 || m.Type.In(1) != ctxType || m.Type.NumOut() != 1 || m.Type.Out(0) != want {
		got := "has no Assigns method"
		if ok {
			got = "has Assigns of type " + strings.Replace(m.Type.String(), "func("+reflect.TypeOf(state).String()+", ", "func(", 1)
		}
		panic(&Error{Code: RT412, Where: callerOutsideLoom(),
			Msg: fmt.Sprintf("live layout %T %s, but its component %s needs %s", state, got, name, want),
			Fix: fmt.Sprintf("add func (l %T) Assigns(c *loom.Ctx) %s, returning the layout's state; leave InnerBlock unset, the page fills it", state, want)})
	}
	if err := checkForms(want, map[reflect.Type]bool{}); err != nil {
		panic(&Error{Code: err.Code, Where: callerOutsideLoom(), Msg: fmt.Sprintf("%s, in the assigns of live layout %T: %s", name, state, err.Msg), Fix: err.Fix})
	}
	return &layout{tmpl: tmpl, live: state}
}

// LayoutOf returns the state of the page's live layout: the copy mounted
// for this page load, which the page may read and change in Mount, its
// handlers and Assigns. It panics with RT435 if the page's layout isn't
// a live layout of type T.
//
//	func (p *TeamPage) Mount(c *loom.Ctx) {
//		shell := loom.LayoutOf[*Shell](c)
//		p.user = shell.User
//	}
func LayoutOf[T any](c *Ctx) T {
	if l, ok := c.layout.(T); ok {
		return l
	}
	want := reflect.TypeFor[T]().String()
	got := "the page's app layout isn't a live layout"
	if c.layout != nil {
		got = fmt.Sprintf("the page's live layout is a %T", c.layout)
	}
	panic(&Error{Code: RT435, Where: callerOutsideLoom(),
		Msg: fmt.Sprintf("LayoutOf[%s] asked for the page's live layout, but %s", want, got),
		Fix: fmt.Sprintf("serve the page in a group with loom.LiveLayout(component, %s), or ask for the type the layout has", want)})
}

// RootLayout is a group option overriding the app's root layout for the
// group's routes. See App.RootLayout.
func RootLayout(tmpl render.Template, assigns any) Option {
	return layoutOption{root: true, l: newLayout(true, tmpl, assigns)}
}
