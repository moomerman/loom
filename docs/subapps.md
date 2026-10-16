# Sub-apps, auth and middleware

A sub-app is a reusable package of live pages, such as `jobsweb`, that an
app mounts at a path prefix with one line. Route groups (see
`layouts.md`) carry what it needs: auth checks, middleware, layouts and
embedded assets.

## Auth and middleware

```go
func requireAdmin(c *loom.Ctx) error {
	if !currentUser(c.Request).Admin {
		return loom.ErrForbidden // or loom.Redirect("/login")
	}
	return nil
}

admin := app.Group(loom.Auth(requireAdmin), loom.Layout(templates.Admin, adminAssigns))
admin.Live("/admin", &DashboardPage{}, templates.Dashboard)
```

For a signed-in user, use `loom.SignedIn("/login")` and
`loom.User[T](c)` (`auth.md`); `loom.Auth` is for further checks, such
as admins only.

- `loom.Auth(fn)` runs `fn` before `Mount` on every page load, and again
  before every event and every `HandleInfo` message in the live session.
- On a page load, `loom.ErrForbidden` answers 403, `loom.Redirect(url)`
  answers 303 to `url`, and any other error answers 500 (logged).
- Later in the session, any error ends the session; the browser reloads
  and gets the page load's answer. A user who loses permission loses the
  page at their next event.
- `c.Request` is the request that loaded the page, so `fn` identifies the
  user from it and checks their current permissions.
- `loom.Middleware(func(http.Handler) http.Handler)` wraps the group's
  HTTP requests (page loads and assets) in plain `net/http` middleware.
  Live events go to `/_loom/event`, not through it: guard them with Auth.
- `loom.DevOnly()` serves the group only in dev (`Config.Dev`, set by `loom
  dev`) and to this machine: elsewhere its page loads, assets and
  handlers answer 404, and 403 to other machines. It's checked per
  request, and the manifest marks the group "dev only". For dev tools
  such as `devmail`, which adds it itself.
- Options compose. Nested groups inherit auth checks and middleware,
  outer first; layouts keep their rule, the nearest wins.

## Plain handlers

Live pages can't set cookies (other than signing in, with `c.SignIn`)
or take a form post: their events arrive after the page has loaded. For those, `g.Handle(pattern, h)` serves a
plain `http.Handler` with the group's auth checks and middleware:

```go
app.Handle("POST /login", http.HandlerFunc(login))   // app.SignIn(w, r, id), then redirect
signedIn.Handle("POST /logout", http.HandlerFunc(logout))
```

A POST from another site is refused before any handler runs (RT429).

- `pattern` is a method and a path, as in `http.ServeMux`; in a mounted
  group the path is relative to the prefix. A pattern without a method
  is RT416.
- An auth check that refuses answers as for a page load: 403, or a
  redirect.
- Handlers aren't live routes, so `loom routes` doesn't list them.

## Mounting

```go
app.Group(loom.Auth(requireAdmin)).Mount("/admin/jobs", jobsweb.New(jobsweb.Config{}))
```

`g.Mount(prefix, sub, opts...)` creates a group nested in `g` with the
prefix and options, and calls `sub.Routes(group)`. The sub-app inherits
`g`'s auth and middleware. `app.Mount` mounts at the top level.

A sub-app is any value with `Routes(g *loom.Group)`. Its routes are
relative to the prefix: `g.Live("/", ...)` is `/admin/jobs` and
`g.Live("/jobs/{id}", ...)` is `/admin/jobs/jobs/{id}`.

```go
type App struct{ cfg Config }

func (a *App) Routes(g *loom.Group) {
	g = g.Group(loom.Assets(assetsFS), loom.RootLayout(Root, rootAssigns), loom.Layout(Shell, shellAssigns))
	g.Live("/", &OverviewPage{app: a}, Overview)
	g.Live("/jobs/{id}", &JobPage{app: a}, Job)
}

// RequiresAuth makes mounting without loom.Auth a startup error (RT415).
func (a *App) RequiresAuth() bool { return true }
```

- A sub-app that implements `RequiresAuth() bool` (`loom.AuthRequired`)
  and returns true panics at startup (RT415) if no group in its chain has
  a `loom.Auth` check. One that returns false is public by design (a
  sign-in page), and the manifest doesn't flag it.
- A sub-app checks what it needs of the configured app with
  `app.StartupCheck(func(ctx) error)`, run by `Run` and
  `loomtest.Mount`: `auth` fails without a mailer (AU901).
- A sub-app's plain handler that answers the browser itself records
  what went wrong with `app.ReportError(err)`: logged, and in `loom
  errors` with its code and fix (`auth`'s OAuth callback, AU907).
- A sub-app sets its own root layout and app layout in a nested group, so
  it doesn't take the host app's styling.
- Mount prefixes are plain paths: `/admin/jobs`, not `/admin/jobs/` or
  `/{tenant}/jobs` (RT416).
- A sub-app uses only Loom's public API.

## Migrations

A sub-app that keeps data in the app's database implements
`loom.HasMigrations`, returning its package's `migrations/` directory,
embedded:

```go
//go:embed migrations
var migrations embed.FS

func (a *App) Migrations() fs.FS { m, _ := fs.Sub(migrations, "migrations"); return m }
```

The app installs them once, and again after upgrading the package:

```
loom db install example.com/notes   # copies them into migrations/ as NNNN_notes_<name>.sql
loom migrate
```

The copies are the app's migrations: committed and reviewed with it,
applied in order with its own by `loom migrate` and `loom dev`,
checksummed, and listed by `migrate status`, `db schema` and the
manifest. Each starts with `-- loom:from <package> <file>`, which is how
`install` knows what is there. Mounting a sub-app whose migrations
aren't applied is RT433 at startup and in `loomtest.Mount`. Like any
migration, a package's are forward-only: a change is a new file.

## Paths and assets

Links in a sub-app must work wherever it is mounted:

```heex
<a href={path("/jobs/" + job.id)}>{job.type}</a>
<link rel="icon" href={asset("icon.svg")} />
```

- `path(p)` in templates and `c.Path(p)` in Go resolve `p` against the
  route's mount prefix. `path("/")` is the prefix itself. `c.URL(p)`
  is the same as an absolute URL, on `Config.BaseURL` or the request's
  origin, for structured data, feeds and mail.
- `loom.Assets(fsys)` embeds files for a group, served at
  `<prefix>/_assets/<name>.<hash>.<ext>` with `Cache-Control: immutable`.
  `asset("app.css")` gives that URL, so a changed file gets a new URL.
  Nested groups use the nearest group's assets. `c.Asset(name)` is the
  same in Go.
- A name ending in `/` is a directory: `asset("player/")` is
  `<prefix>/_assets/player.<hash>/`, one hash over everything in it, and
  its files are served there under their own names. Use it for code
  that loads its files by relative URL (ES module imports, workers,
  audio worklets, `.wasm`), which per-file hashing would break.
- In a package marked reusable, a static `href`, `src` or `action`
  starting with `/` is warning TC336.

## Packaging templates

A package ships its templates already compiled; the host app never
compiles a dependency's `.hx` files and needs no build step and no npm.

1. Mark the package reusable with a `//loom:package` line in its Go
   package doc. `loom check` and `loom build` then warn about bare
   absolute URLs.
2. Add `//go:generate go run github.com/moomerman/loom/cmd/loom build .`
   and commit the generated `*_hx.go` files with the package.
3. Embed assets with `//go:embed` and pass them to `loom.Assets`.
4. Style components with scoped `<style>` blocks and the package's own
   `tokens.toml` (see `components.md`), not a CSS asset: each route's
   stylesheet then carries exactly the styles its components need.

`internal/subapptest` is a minimal example; `jobsweb` a full one (see
`example-jobsweb.md`), and `auth` one with migrations, jobs and the
host's styling (`auth.md`).

## Seeing it

`loom routes --json` lists each route's full path with its `prefix`,
`mount` (the sub-app's type), `auth` checks and `middleware` count.
