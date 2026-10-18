# Layouts

A page renders inside two layouts:

| | Root layout | App layout |
|---|---|---|
| Contains | `<html>`, `<head>`, CSS, scripts | nav, sidebar, the user's name |
| Rendered | once, when the page loads | with the page, on every render |
| Diffed | never (except `<.live_title>`) | yes, like page content |
| Assigns from | request-scoped data only | anything, per page session |
| Set by | `app.RootLayout` (or a group) | a route group |

Both are ordinary components with an `inner_block slot`. Their assigns
come from a Go func `func(c *loom.Ctx) A`, where `A` is the component's
assigns struct; the framework fills in `InnerBlock`. An app layout with
state and events of its own (a composer, toasts, a command palette) is
a live layout instead, with Go methods like a page's (below).

## Root layout

```heex
component Root(title string, inner_block slot)

<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <.live_title default="Todos">{title}</.live_title>
  <.live_meta />
  <.loom_styles />
</head>
<body>
  {render_slot(inner_block)}
  <.loom_scripts />
</body>
</html>
```

```go
app.RootLayout(templates.Root, func(c *loom.Ctx) templates.RootAssigns {
	return templates.RootAssigns{Title: c.TitleOr("Todos")}
})
```

- A component with an `<html>` element is a root layout. It must be a
  whole document: `<!DOCTYPE html>`, then `<html>` holding `<head>` and
  `<body>` (TC331).
- `{render_slot(inner_block)}` renders the live root:
  `<div id="loom-root" data-sid="...">` holding the app layout and page.
  Without it the page doesn't appear (TC330).
- `<.live_meta />` renders the page's metadata (below; TC354).
- `<.loom_styles />` links the page's stylesheet: design tokens and the
  scoped CSS of the components the route renders (TC337, see
  `components.md`).
- `<.loom_scripts />` renders the client script tag. Put it at the end of
  `<body>`; without it the page is never live (TC329).
- The assigns func runs once per page load, after `Mount`. The root layout
  never re-renders.
- **No per-user data in the root layout.** It renders once per page
  load and never updates, and live navigation and remounts keep it
  across pages and sessions, so per-user data in it would go stale. The
  user's name, flash messages and anything else per user go in the app
  layout.
- Without `app.RootLayout`, pages use the built-in `default_root.hx`,
  titled `Config.Title` until a page sets its own.

## Titles

`<.live_title>` renders `<title>`: its content, or `default` when the
content is empty, between the optional `prefix` and `suffix` (all
strings). It is the only part of the root layout that a diff changes;
live navigation also swaps `<.live_meta />`'s tags (Metadata, below).

```go
func (p *TodoPage) Mount(c *loom.Ctx) {
	p.load(c)
	c.Title(fmt.Sprintf("Todos (%d)", len(p.todos)))
}
```

A page sets its title with `c.Title` in `Mount` or a handler. The root
layout's func reads it with `c.TitleOr(def)`. When a later handler
changes it, the new title goes to the browser with the diff, and the
client sets `document.title` to prefix + (title or default) + suffix.
So the content of `<.live_title>` should be just the page's title.

`<.live_title>`, `<.live_meta />`, `<.loom_styles />` and `<.loom_scripts />` are only allowed in a root layout,
once each (TC332, TC333).

## Metadata

A page describes itself to link previews and search engines with
`c.Meta` in `Mount`, and the root layout's `<.live_meta />` renders it
into `<head>`:

```go
func (p *TunePage) Mount(c *loom.Ctx) {
	...
	c.Title(t.Title)
	c.Meta(loom.Meta{
		Description: t.Title + " by " + t.Credit + ", a SID tune for the Commodore 64.",
		Image:       c.Asset("cards/c64.png"), // ideally 1200×630
		Canonical:   c.Path("/c64/" + t.Slug),
	})
}
```

- It renders `<meta name="description">`, Open Graph (`og:title` from
  the page's title, `og:description`, `og:image`, `og:url`), a Twitter
  card, and `<link rel="canonical">`; a field left empty is left out.
- `Image` and `Canonical` may be paths: they become absolute URLs on
  `Config.BaseURL`, or the request's origin without one, as link
  previews need. Set `BaseURL` in production.
- It is rendered with the document, on a page load or a live
  navigation (which swaps the tags for the new page's), because link
  previews and search engines only read the first load. So set it in
  `Mount` (or the `HandleParams` that runs with it); changing it later
  is RT449, reported like `c.Fail`.
- `StructuredData` is schema.org JSON-LD for search engines: any value
  `encoding/json` marshals, usually a `map[string]any` with
  `"@context": "https://schema.org"` and an `"@type"`. It is rendered
  as `<script type="application/ld+json">` (CSP doesn't apply to it),
  escaped so no value can end the script. Its URLs aren't rewritten:
  make them absolute with `c.URL(path)`, which is `c.Path` on
  `BaseURL`. A value that can't be marshalled is RT450.
- `NoIndex` asks search engines not to index the page
  (`<meta name="robots" content="noindex">`), for pages that only
  repeat others, such as search results; their links are still
  followed. Don't also disallow the page in `robots.txt`, or crawlers
  never read the tag.
- In tests, `p.Meta()` is what the page set, and `p.Document()` shows
  the tags.

## App layouts and route groups

```heex
component App(user User, inner_block slot)

<nav>
  <a href="/">Todos</a>
  <span>{user.name}</span>
</nav>
<main>{render_slot(inner_block)}</main>
```

```go
g := app.Group(loom.Layout(templates.App, func(c *loom.Ctx) templates.AppAssigns {
	return templates.AppAssigns{User: currentUser(c.Request)}
}))
g.Live("/", &TodoPage{}, templates.TodoApp)
g.Live("/settings", &SettingsPage{}, templates.Settings)

app.Live("/login", &LoginPage{}, templates.Login) // root layout only
```

- An app layout is rendered inside the live tree, so it is diffed with
  the page and its `:for`, `:if` and events work as in the page. A
  func layout's events go to the page, so every page in the layout has
  handlers for them (RT422 at startup otherwise). If those events change
  state every page shares, make it a live layout (below).
- Its func runs on the page's goroutine on every render, after `Mount`,
  a handler or `HandleInfo`. To change what the layout shows, the page
  stores a value with `c.Set(key, value)` and the func reads
  `c.Get(key)`:

  ```go
  // in a handler
  c.Set("unread", n)

  // in the layout func
  unread, _ := c.Get("unread").(int)
  ```

- A route outside any group has no app layout.
- `g.Group(opts...)` nests a group. The nearest group's layout applies;
  layouts don't stack. To wrap one layout in another, the inner layout
  calls the outer one, as with any component:

  ```heex
  component Admin(user User, inner_block slot)

  <.app user={user}>
    <aside>Admin</aside>
    {render_slot(inner_block)}
  </.app>
  ```

- A group can override the root layout too:
  `app.Group(loom.RootLayout(templates.Bare, bareAssigns))`.
- Groups take more options: `loom.Auth` checks, `loom.Middleware` and
  `loom.Assets`, which accumulate (outer first) instead of the nearest
  winning, and `g.Mount` mounts a sub-app at a prefix. See `subapps.md`.
- A func that doesn't return the component's assigns type panics at
  startup with RT412; a root layout passed as an app layout (or the
  reverse) with RT413.

## Live layouts

A live layout owns its state and handles its own events, like a page
does: `loom.LiveLayout(component, &State{})`, where the state is a
pointer to a struct with `Mount`, `Assigns` and, as needed, `Events` and
`HandleInfo`, exactly as on a page (`live.md`). Use it for chrome that
every page shares: the new-issue composer, toasts, a command palette,
unread counts.

```go
// Shell is the state of the layout around every signed-in page.
type Shell struct {
	store *Store // set on the value given to LiveLayout, shared by every copy
	user  User
	toast string
}

func (s *Shell) Mount(c *loom.Ctx) {
	s.user = currentUser(c.Request)
	c.Subscribe("inbox:" + s.user.ID) // its own topics, in its own HandleInfo
}

func (s *Shell) Assigns(c *loom.Ctx) templates.ShellAssigns {
	return templates.ShellAssigns{User: s.user, Toast: s.toast} // InnerBlock is filled in
}

func (s *Shell) Events() loom.Events {
	return loom.Events{"toast_close": func(c *loom.Ctx, _ loom.Payload) { s.toast = "" }}
}

func (s *Shell) HandleInfo(c *loom.Ctx, msg loom.Message) { ... }

g := app.Group(loom.LiveLayout(templates.Shell, &Shell{store: store}))
g.Live("/settings", &SettingsPage{}, templates.Settings)
```

- Each page load gets its own copy of the state, mounted before the
  page and kept for as long as the page is open. Its methods run on the
  page's goroutine, never at the same time as the page's.
- An event goes to whichever handles it, the layout or the page, from
  any markup: a page's button with `on:click="compose" val-team={t.id}`
  opens the layout's composer. So pages handle only their own events.
  An event both handle is RT434 at startup; one neither handles is
  RT422.
- After any event or message, the layout's `Assigns` and the page's
  both run and the whole tree is diffed, so a page that reads data in
  `Assigns` shows a change the layout heard of.
- A message on a topic the layout subscribed to goes to the layout's
  `HandleInfo`; if the page subscribed to it too, also to the page's.
- The page reaches the layout's state with `loom.LayoutOf[*Shell](c)`,
  in `Mount` (the layout is mounted by then), its handlers and
  `Assigns`, to read the user or set a toast:

  ```go
  func (p *SettingsPage) save(c *loom.Ctx, payload loom.Payload) {
  	...
  	loom.LayoutOf[*Shell](c).toast = "Saved"
  }
  ```

  A page whose layout isn't a live layout of that type panics with
  RT435.
- A state without the right `Assigns` is RT412 at startup.
- **It moves with live navigation.** Following a link (or back,
  forward, `c.Navigate`) to a page in the same live layout and root
  layout hands the state to the new page: what it holds survives, its
  `Mount` doesn't run again, and the topics it subscribed to stay
  subscribed. So a player, a draft or a toast outlives the page under
  it, as the element does in the browser. Timers (`c.SendAfter`) and
  `c.Set` values stay with the page left behind, which does nothing
  more. If the new page doesn't mount (an auth refusal, `c.NotFound`, a
  panic), the old page keeps its layout. A page load, a remount or a
  page in another layout mounts a fresh state.
- **It can survive a restart.** A remount (after a restart or deploy)
  mounts a fresh state, so a player would change its tune. The part a
  `Keep` method returns comes back before Mount, sealed in the browser
  meanwhile (`live.md`, "Keeping state across a remount"):

  ```go
  func (p *Player) Keep() any { return &p.playing } // station, track ids, queue
  ```
- `loom.manifest` lists the layout's events as handled by its `Events`,
  and marks the page's events it handles. `loom inspect` shows its
  assigns as `layout_assigns`. `loomtest.NoLayout()` still mounts the
  state, so `LayoutOf` and its events work in a test of the page alone.

examples/issues' `Shell` is a live layout (`example-issues.md`).

## Live navigation

Following a link to a page with the same root layout swaps only the live
root: no page load, the root layout stays, and the new page gets its
own session (the old one ends). Back and forward work the same way. There is nothing to write: every `<a
href>` to a page of the app navigates live, and so does a GET form to
one (a search box): its fields become the URL's query, as in the
browser's own submit.

```heex
<a href={path("/issues/" + issue.id)}>{issue.title}</a>
```

- `navigate.js`, which `<.loom_scripts />` loads after `loom.js`, takes
  clicks on links within the site (left button, no modifier key, no
  `target` or `download`) and submits of GET forms within the site
  (no `target`, no `on:submit`), and fetches the page with the
  `X-Loom-Navigate` header. The server answers before running anything:
  "swap" for a page whose root layout is the current page's, and
  "reload" for anything else, which the browser then loads as usual:
  a page in another root layout (a mounted sub-app with its own), a
  plain handler (`app.Handle`), a file. So no handler runs twice.
- On a swap the browser takes the new page's live root, title,
  stylesheet (waiting until it loads) and any scripts it lacks (its
  behaviors), pushes the URL and scrolls to the top. The app
  layout is morphed like any update, so its elements keep their state.
- Auth checks run as on any page load; a redirect is followed, and a
  refusal loads the page as usual to show it.
- A page navigates from Go with `c.Navigate(url)`: from a handler or
  `HandleInfo` the browser follows it as a link; from `Mount` the page
  load redirects. Within the same
  route, a page with `HandleParams` is patched instead: the server
  answers "patch", the session stays and only the URL and the changed
  parts update (`c.Patch`, `live.md`).

```go
func (p *ComposerPage) create(c *loom.Ctx, payload loom.Payload) {
	id := p.save(c, payload)
	c.Navigate(c.Path("/issues/" + id))
}
```

- A remount uses the same swap. When the server has lost the page's
  session (it restarted, or the session expired), the browser fetches
  the same URL leaving no session (`X-Loom-Navigate: -`) and swaps in a
  new one, keeping the document and the user's input: it restores each
  named form field by name and sends the form's `on:change`, if it has
  one, so the new session sees it, and sends back the state the page
  and its live layout keep (`Keep`, `live.md`). If the root layout changed (a deploy
  edited it) the page reloads instead.
  `loom dev` remounts the same way after a rebuild.
- HEEx's `<.link navigate={...}>` doesn't exist: write `<a href>`
  (TC315, TC316 say so).

## Testing

`loomtest.Mount(t, app, "/")` mounts the page the app routes at a path,
with its app layout. `p.HTML()` is the live tree (layout and page),
`p.Document()` the whole document with the root layout, and `p.Title()`
the page's title. `loomtest.NoLayout()` mounts the page alone:

```go
p := loomtest.Mount(t, app, "/", loomtest.NoLayout())
```

`p.Navigated()` is the URL the page last sent the browser to with
`c.Navigate`. `p.Navigate(path)` leaves the page for another as a live
navigation does and returns the new page, with the live layout's state
carried over when both share it:

```go
tune := loomtest.Mount(t, app, "/c64/monty-on-the-run")
tune.Click("button.play")                // the live layout's event
next := tune.Navigate("/c64/lykkehjulet")
next.AssertText("#now-title", "Monty on the Run") // the layout's player kept playing
```

`loom routes --json` shows each route's `root_layout` and `layout`:
links between routes with the same `root_layout` navigate live.
