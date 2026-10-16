package loom

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/moomerman/loom/render"
)

// Sub-apps and group options for them (docs/subapps.md). A sub-app is a
// reusable package of live pages, such as jobsweb, mounted at a path
// prefix with Mount. Its templates link with the path and asset builtins,
// which resolve against the prefix, so it works wherever it is mounted.
// Auth checks guard a group's pages on load and on every event;
// middleware wraps its HTTP requests.

// SubApp is a reusable set of live pages, mounted with Group.Mount.
type SubApp interface {
	// Routes registers the sub-app's pages in g, with paths relative to
	// the mount prefix. It may nest groups in g to set its own layouts
	// and embedded assets.
	Routes(g *Group)
}

// AuthRequired is implemented by sub-apps that must not be mounted
// without an auth check, such as an admin dashboard. Mounting one in a
// group chain with no loom.Auth is a startup error (RT415). A sub-app
// that is public by design, such as a sign-in page, returns false, and
// the manifest doesn't flag it as mounted with no auth check.
type AuthRequired interface {
	SubApp
	RequiresAuth() bool
}

// HasMigrations is implemented by sub-apps that keep data in the app's
// database. Migrations returns the package's migrations/ directory,
// embedded; `loom db install <package>` copies them into the app's own
// migrations, so `loom migrate`, `loom dev`, `migrate status` and `db
// schema` apply and report them like the app's. Run and loomtest check
// that each is applied (RT433).
//
//	//go:embed migrations
//	var migrations embed.FS
//
//	func (a *App) Migrations() fs.FS { m, _ := fs.Sub(migrations, "migrations"); return m }
type HasMigrations interface {
	SubApp
	Migrations() fs.FS
}

// Mount mounts a sub-app at prefix, like Group.Mount, in a group of the
// app's top level.
func (a *App) Mount(prefix string, sub SubApp, opts ...Option) { a.top.Mount(prefix, sub, opts...) }

// Mount mounts a sub-app at prefix (e.g. "/admin/jobs"), in a new group
// nested in g with the given options. The sub-app inherits g's auth
// checks and middleware, and registers its routes relative to the prefix.
//
//	admin := app.Group(loom.Auth(requireAdmin))
//	admin.Mount("/admin/jobs", jobsweb.New(jobsweb.Config{}))
func (g *Group) Mount(prefix string, sub SubApp, opts ...Option) {
	if !strings.HasPrefix(prefix, "/") || prefix == "/" || strings.HasSuffix(prefix, "/") || strings.ContainsAny(prefix, "{}?#") {
		panic(&Error{Code: RT416, Msg: fmt.Sprintf("mount prefix %q is not a plain path like \"/admin/jobs\"", prefix), Where: callerOutsideLoom(),
			Fix: "start the prefix with /, end it without one, and use no {wildcards}, e.g. g.Mount(\"/admin/jobs\", ...)"})
	}
	child := &Group{app: g.app, parent: g, prefix: g.prefix + prefix, mount: fmt.Sprintf("%T", sub)}
	child.apply(opts)
	r, ok := sub.(AuthRequired)
	child.public = ok && !r.RequiresAuth()
	if ok && r.RequiresAuth() && len(child.authChain()) == 0 {
		panic(&Error{Code: RT415, Msg: fmt.Sprintf("%T requires auth, but is mounted at %s with no loom.Auth check", sub, child.prefix), Where: callerOutsideLoom(),
			Fix: fmt.Sprintf("mount it in a group with an auth check: app.Group(loom.Auth(requireAdmin)).Mount(%q, ...), or pass loom.Auth(...) to Mount", prefix)})
	}
	if m, ok := sub.(HasMigrations); ok {
		g.app.subMigrations = append(g.app.subMigrations, m)
	}
	sub.Routes(child)
}

// App returns the app the group belongs to, so a sub-app can reach the
// app's services, such as its job runner and PubSub.
func (g *Group) App() *App { return g.app }

// Prefix returns the group's mount prefix, e.g. "/admin/jobs", or "" for
// a group outside any mount.
func (g *Group) Prefix() string { return g.prefix }

// AuthFunc is an auth check: it returns nil to allow the request,
// ErrForbidden to refuse it, or Redirect(url) to send the browser
// elsewhere (to a login page, say). See Auth.
type AuthFunc func(c *Ctx) error

// ErrForbidden is returned by an auth check to refuse a request: a page
// load gets 403 Forbidden.
var ErrForbidden = errors.New("forbidden")

// RedirectError is returned by an auth check, made with Redirect, to send
// the browser to URL instead of the page.
type RedirectError struct{ URL string }

func (e *RedirectError) Error() string { return "redirect to " + e.URL }

// Redirect returns an error for an auth check that sends the browser to
// url, e.g. a login page, instead of serving the page.
func Redirect(url string) error { return &RedirectError{URL: url} }

// authError is an auth check's refusal, for the runtime to answer: 403
// or a redirect on a page load, the end of the session on an event.
type authError struct{ err error }

func (e *authError) Error() string { return "auth check failed: " + e.err.Error() }
func (e *authError) Unwrap() error { return e.err }

// Auth is a group option adding an auth check. Checks run before Mount
// on every page load, and again before every event and message in the
// live session, outer groups' checks first. A check that fails on load
// answers 403 (ErrForbidden) or a redirect (Redirect); one that fails
// later ends the session, and the browser reloads and gets the same
// answer. So a user who loses permission loses the page at their next
// event.
//
//	func requireAdmin(c *loom.Ctx) error {
//		if !currentUser(c.Request).Admin {
//			return loom.ErrForbidden
//		}
//		return nil
//	}
//
//	admin := app.Group(loom.Auth(requireAdmin))
func Auth(check AuthFunc) Option { return authOption{check} }

type authOption struct{ check AuthFunc }

func (o authOption) apply(g *Group) { g.auth = append(g.auth, o.check) }

// Middleware is a group option wrapping the group's HTTP requests (page
// loads and assets) in plain net/http middleware, outer groups' first.
// Live events don't pass through it: they are checked with Auth.
func Middleware(mw func(http.Handler) http.Handler) Option { return middlewareOption{mw} }

type middlewareOption struct {
	mw func(http.Handler) http.Handler
}

func (o middlewareOption) apply(g *Group) { g.middleware = append(g.middleware, o.mw) }

// authChain returns the auth checks of g and its parents, outer first.
func (g *Group) authChain() []AuthFunc {
	var groups []*Group
	for x := g; x != nil; x = x.parent {
		groups = append(groups, x)
	}
	var out []AuthFunc
	for i := len(groups) - 1; i >= 0; i-- {
		out = append(out, groups[i].auth...)
	}
	return out
}

// wrap wraps h in the middleware of g and its parents, outer first.
func (g *Group) wrap(h http.Handler) http.Handler {
	for x := g; x != nil; x = x.parent {
		for i := len(x.middleware) - 1; i >= 0; i-- {
			h = x.middleware[i](h)
		}
	}
	return h
}

// middlewareCount counts the middleware of g and its parents.
func (g *Group) middlewareCount() int {
	n := 0
	for x := g; x != nil; x = x.parent {
		n += len(x.middleware)
	}
	return n
}

// mountType returns the sub-app of the nearest mount, or "".
func (g *Group) mountType() string {
	if m := g.mountGroup(); m != nil {
		return m.mount
	}
	return ""
}

// mountGroup returns the group a sub-app was mounted in, g or a parent,
// or nil outside mounts.
func (g *Group) mountGroup() *Group {
	for ; g != nil; g = g.parent {
		if g.mount != "" {
			return g
		}
	}
	return nil
}

// scope returns the render scope of the group's routes: its prefix and
// the nearest group's assets.
func (g *Group) scope() *render.Scope {
	s := &render.Scope{Prefix: g.prefix}
	for x := g; x != nil; x = x.parent {
		if x.assets != nil {
			s.Asset = x.assets.url
			break
		}
	}
	return s
}

// authorize runs the checks in order and returns the first refusal.
func authorize(checks []AuthFunc, c *Ctx) error {
	for _, check := range checks {
		if err := check(c); err != nil {
			return &authError{err}
		}
	}
	return nil
}

// refuse answers a page load that an auth check refused.
func (a *App) refuse(w http.ResponseWriter, r *http.Request, g *Group, e *authError) {
	var re *RedirectError
	switch {
	case errors.As(e.err, &re):
		http.Redirect(w, r, re.URL, http.StatusSeeOther)
	case errors.Is(e.err, ErrForbidden):
		a.serveError(w, r, g, http.StatusForbidden, nil)
	default:
		a.logError("", e)
		a.serveError(w, r, g, http.StatusInternalServerError, e)
	}
}

func funcName(f any) string {
	name := runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
	return name[strings.LastIndex(name, "/")+1:]
}

// Assets is a group option embedding static files (CSS, images) for the
// group's templates, served under the group's prefix at
// /_assets/<name>.<hash>.<ext> with long cache headers. Templates link
// them with the asset builtin, asset("app.css"), which gives the hashed
// URL, so a new version of a file has a new URL. fsys is usually an
// embed.FS, narrowed with fs.Sub to the assets directory.
//
// A name ending in "/" is a directory, served whole at
// /_assets/<dir>.<hash>/ with one hash over everything in it: asset("player/")
// gives that URL. Files in it keep their names, so they can load each
// other by relative URL (an ES module's imports, a worker, a worklet, a
// .wasm file), and any change to any of them gives the whole directory
// a new URL.
//
//	//go:embed assets
//	var files embed.FS
//	assetsFS, _ := fs.Sub(files, "assets")
//	g = g.Group(loom.Assets(assetsFS))
func Assets(fsys fs.FS) Option { return assetsOption{newAssets(fsys)} }

type assetsOption struct{ a *assets }

func (o assetsOption) apply(g *Group) {
	a := *o.a // each group serves its own copy, under its own prefix
	g.assets = &a
}

// assets are a group's embedded files, by name and by hashed name, and
// its directories, each hashed over everything in it.
type assets struct {
	fsys    fs.FS
	prefix  string            // the URL path they are served under
	hashed  map[string]string // name -> hashed name
	names   map[string]string // hashed name -> name
	dirs    map[string]string // directory -> hashed directory
	dirName map[string]string // hashed directory -> directory
}

func newAssets(fsys fs.FS) *assets {
	a := &assets{fsys: fsys, hashed: map[string]string{}, names: map[string]string{}, dirs: map[string]string{}, dirName: map[string]string{}}
	// Every directory's hash covers its files' names and contents, so a
	// change anywhere under it changes its URL. WalkDir visits files in
	// lexical order, so the hash doesn't depend on the order of reads.
	trees := map[string]hash.Hash{}
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name != "." {
				trees[name] = sha256.New()
			}
			return nil
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		ext := path.Ext(name)
		h := strings.TrimSuffix(name, ext) + "." + hex.EncodeToString(sum[:4]) + ext
		a.hashed[name], a.names[h] = h, name
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			fmt.Fprintf(trees[dir], "%s\x00%x\n", strings.TrimPrefix(name, dir+"/"), sum)
		}
		return nil
	})
	if err != nil {
		panic(&Error{Code: RT416, Msg: "reading the assets: " + err.Error(), Where: callerOutsideLoom(), Fix: "pass an fs.FS of the assets directory, e.g. fs.Sub(embedded, \"assets\")"})
	}
	for dir, t := range trees {
		h := dir + "." + hex.EncodeToString(t.Sum(nil)[:4])
		a.dirs[dir], a.dirName[h] = h, dir
	}
	return a
}

// url returns the URL path of an asset by name: a file, or a directory
// when the name ends in "/".
func (a *assets) url(name string) (string, bool) {
	if dir, ok := strings.CutSuffix(name, "/"); ok {
		h, ok := a.dirs[path.Clean(dir)]
		if !ok {
			return "", false
		}
		return a.prefix + h + "/", true
	}
	h, ok := a.hashed[name]
	if !ok {
		return "", false
	}
	return a.prefix + h, true
}

// file returns the name of the file a request path under the prefix
// serves: a file by its hashed name, or a file in a hashed directory.
func (a *assets) file(p string) (string, bool) {
	if name, ok := a.names[p]; ok {
		return name, true
	}
	for i := strings.IndexByte(p, '/'); i >= 0; {
		if dir, ok := a.dirName[p[:i]]; ok {
			name := dir + p[i:]
			if _, ok := a.hashed[name]; ok {
				return name, true
			}
			return "", false
		}
		j := strings.IndexByte(p[i+1:], '/')
		if j < 0 {
			break
		}
		i += 1 + j
	}
	return "", false
}

// serveAssets serves the group's assets under its prefix.
func (g *Group) serveAssets() {
	g.assets.prefix = g.prefix + "/_assets/"
	pattern := "GET " + g.assets.prefix + "{file...}"
	for p := range g.app.assetPrefixes {
		if p == g.assets.prefix {
			panic(&Error{Code: RT416, Msg: "two groups embed assets under " + p, Where: callerOutsideLoom(), Fix: "give loom.Assets to one group and nest the others in it; nested groups use the nearest group's assets"})
		}
	}
	g.app.assetPrefixes[g.assets.prefix] = true
	a := g.assets
	g.app.mux.Handle(pattern, g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := a.file(r.PathValue("file"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		f, err := a.fsys.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		if rs, ok := f.(interface {
			Read([]byte) (int, error)
			Seek(int64, int) (int64, error)
		}); ok {
			http.ServeContent(w, r, name, time.Time{}, rs)
			return
		}
		data, err := fs.ReadFile(a.fsys, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	})))
}

// joinPath joins a mount prefix and a route path: "/" is the prefix
// itself.
func joinPath(prefix, p string) string {
	if prefix == "" {
		return p
	}
	if p == "/" {
		return prefix
	}
	return prefix + p
}
