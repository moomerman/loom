# Live pages

A live page is a Go value holding the page's state, plus an `.hx`
component that renders it. The server keeps the state; the browser shows
the HTML and sends events; each change comes back as a small diff.

It is Loom's one page model. A page that reads a table can watch it and
re-render when it changes anywhere (`loom.Watch`); events survive a
restart; and `optimistic:*` hints give instant feedback while an event
is on its way. All three are below.

## A page

```go
package todo

import (
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/examples/todo/templates"
)

type TodoPage struct {
	todos []templates.Todo
}

// Mount runs once per page load, before the first render, and loads the
// page's state.
func (p *TodoPage) Mount(c *loom.Ctx) {}

// Assigns turns the page's state into its template's assigns. It runs
// after Mount and after every event and message.
func (p *TodoPage) Assigns(c *loom.Ctx) templates.TodoAppAssigns {
	return templates.TodoAppAssigns{Todos: p.todos}
}

// Events maps each event the template sends to the func that handles it.
func (p *TodoPage) Events() loom.Events {
	return loom.Events{"add": p.add, "delete": p.delete}
}

func (p *TodoPage) add(c *loom.Ctx, payload loom.Payload) {
	p.todos = append(p.todos, templates.Todo{Title: payload["title"]})
}
```

- Assigns returns the struct generated for the template: component
  `TodoApp` gets `templates.TodoAppAssigns`, with one field per
  parameter in PascalCase. `App.Live` checks it against the template, so
  a page with the wrong type, or none, panics at startup (RT406).
- Every event the template sends (`on:click="delete"`), including those
  of the components it calls and of its app layout, must be in Events,
  or in its live layout's (`layouts.md`).
  `App.Live` checks that too: a missing one panics at startup with the
  template's file and line, and a did-you-mean (RT422). An event the
  page doesn't handle that arrives anyway is ignored and logged (RT423).
- **An event runs only if the page shows its control.** One that no
  element of the current render binds (a button behind `:if={admin}`,
  a step not reached, a menu that isn't open) is refused before its
  handler runs and logged (RT438), so hiding a control is enough; the
  handler needn't check again.
- Handlers change the page's state and return nothing; the runtime then
  calls Assigns, renders and sends only what changed (nothing, if
  nothing did). Assigns runs after every event and message, so keep it
  cheap: load from the database in Mount and in the handlers that need
  fresh data, into fields, as `IssuePage` in examples/issues does.
- Each page load gets its own shallow copy of the value given to
  `App.Live`, owned by one goroutine; Events is called on that copy, so
  `p.add` acts on it. Mount, the handlers and Assigns never run
  concurrently for one page, so they need no locks. Fields set on the
  value given to `App.Live` (a database handle, say) are shared by every
  copy.
- `c.Request` is the request that loaded the page; `c.SID` is its session
  id. `c.Context()` is a context that lasts as long as the page and is
  cancelled when it closes or restarts: pass it to queries in Mount, the
  handlers and HandleInfo. (`c.Request.Context()` ends with the page
  load.)
- If Mount, a handler, Assigns or rendering panics, the page restarts
  with a fresh Mount and the browser gets a full render (RT402). See
  `errors.md`.
- **A URL that names nothing** (`/tracks/{id}` with no such track):
  call `c.NotFound()` in Mount and return. The page load answers 404
  with the app's error page, and Assigns doesn't run. Later, in a
  handler, it is too late for 404 (RT448).
- For an error the page handles, such as a failed write, call
  `c.Fail(err)` and leave state that tells the user: the error is
  logged and shown by `loom errors` (RT419, or its own code such as
  DB602), and the page keeps running. Don't swallow errors silently.

## The app

```go
func main() {
	app := loom.New(loom.Config{Title: "Todos"}) // see Config for the rest
	app.Live("/", &todo.TodoPage{}, templates.TodoApp)
	if err := app.Run(":4000"); err != nil { // nil after a graceful shutdown
		log.Fatal(err)
	}
}
```

`App` is an `http.Handler`, so it can also be mounted in another server.
`App.Live` matches its path exactly. The page is served inside a root
layout (the document) and, in a route group, an app layout (nav and other
chrome); a page sets its title with `c.Title("...")`. See `layouts.md`.

`loom.Config` is the one place the app is configured, every field with
a default: `DB`, `BaseURL`, `TrustedProxies`, `Title`, `Logger`,
`SessionTimeout`, `SignInFor`, `MaxSessions` (10,000 open pages; more is
503, RT444), `StatementTimeout`, `DBMaxConns`, `ShutdownGrace`,
`DrainDelay`, `SlowQuery`, `OnEvent`, `CSP`, `Secret` (encrypts what
can't be hashed, with `app.Seal` and `app.Open`; at least 32 characters
from the environment), `Jobs` (`jobs.md`), `Dev`
and `Server` (a hook on the `http.Server`, which `Run` gives header and
idle timeouts). A bad value is RT441 from `New`.
In production:

- `GET /_loom/health` answers JSON (database, jobs, leader, this node in
  the jobs' node registry, the table watch's and the PubSub's LISTEN,
  open pages, version) with 200,
  or 503 when the database is unreachable or shutdown has begun: point
  the load balancer's health check at it, and set `DrainDelay` to its
  interval so it stops sending requests before the server stops.
- Forwarded headers (`X-Forwarded-For`, `X-Forwarded-Proto`) count only
  from loopback and `TrustedProxies`; anyone else's are removed.
- Every response carries a Content-Security-Policy, `loom.DefaultCSP`:
  only the app's own scripts, connections, frames and forms (style
  attributes are allowed), so injected markup can't run script. Loom
  needs no inline script, and `loom check` warns about any in a
  template (TC353). Give `Config.CSP` your own policy to allow more,
  e.g. `loom.DefaultCSP + "; img-src 'self' https://cdn.example.com"`.
  Responses also carry `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: strict-origin-when-cross-origin` and, when
  `BaseURL` is https, `Strict-Transport-Security`. The manifest shows
  the policy.
- Responses are gzipped for browsers that accept it: pages,
  navigations, scripts, stylesheets, assets, JSON and any handler's
  text, with `Vary: Accept-Encoding`. Bodies under 1 KB, types
  compressed already (images, audio, woff2, archives), ranges, a
  response that set its own `Content-Encoding`, and the event stream
  go out as they are.
- A failed page load shows a plain error page with the error's code,
  never the error; `app.ErrorPage(component, func(loom.PageError) A)`
  brands it, rendered in the failing route's root layout (the app's
  for a URL no route matches). In dev the error itself is shown.
- An event handler running over 10 seconds is RT445, and `loom
  sessions` shows the page's `busy_seconds`.
- What the app does (loads, clicks, commits, jobs, refusals, errors) is
  one stream of named events, logged to `Logger` and passed to
  `OnEvent` for an error tracker or metrics; `loom events`, `loom
  trace` and `loom stats` read it, and `loom queries` counts the
  database's statements. See `events.md`.

## Events

In the template, `on:<event>="name"` sends event `name` when the DOM event
fires on that element or inside it (the nearest bound element wins), and
the page's handler for `name` gets the payload:

```heex
<form on:submit="add"><input name="title" /></form>
<input type="checkbox" checked={t.done} on:change="toggle" val-id={t.id} />
<button on:click="delete" val-id={t.id}>×</button>
```

Supported events: `click`, `change`, `input`, `submit`, `keydown`,
`keyup`, `focus`, `blur`.

The payload is a `map[string]string`:

| From | Payload |
|---|---|
| `val-<name>` attributes on the bound element | `<name>`: the value |
| a `<form>` (on:submit) | each named field: its value |
| `<input>`, `<select>`, `<textarea>` | `value` |
| checkbox or radio | `checked`: `"true"` or `"false"` |
| keyboard events | `key`, e.g. `"Enter"` |

A submitted form is reset after its event is sent; the page's default
submit is always prevented. For a form with validation and per-field
errors, keep a `form.Form` of a typed input struct and bind the inputs
to its fields; see `forms.md`.

The DOM is patched in place. Elements in a `:for` with a `:key` keep their
identity across updates, and an input that has focus keeps what the user
typed. The root element has class `loom-connected` while the page is
connected to the server.

### Pending events and hints

An element whose event hasn't been answered yet has class
`loom-pending`, so it can look busy (the kit's button and form field
do). Static `optimistic:*` hints on it say what the user should see
meanwhile: `optimistic:class="done"` toggles classes,
`optimistic:disabled` and `optimistic:hidden` set those. They are undone
once the event's update is applied, or the event is dropped, and the
server's render decides what stays. Use them where waiting is visible
and the outcome is near certain: disable a button that shouldn't be
clicked twice, hide a row being deleted, strike through a todo being
ticked. They can't predict data (a new row, a count); that comes back
with the update. On a component call they travel with its events, like
`val-*`, to the element the component binds them on.

### Restarts

Events survive a restart or a dropped connection. The browser keeps each
event until the server acks it, having run its handler, and resends it
otherwise; the server runs each event once, however often it arrives. On
shutdown (`SIGINT`, `SIGTERM`) the app stops taking events (browsers
resend them after the restart) and finishes the ones already queued,
within the jobs runner's `ShutdownGrace` (15s by default). When the
page's session is gone, the page remounts on a new one without a reload,
keeping what the user typed in its forms (`layouts.md`) and what the
page keeps (`Keep`, below). So deploying mid-click or mid-form loses
nothing. The limit: a handler's writes and the record
that the event ran commit separately, so a crash between the two runs
the event again on resend. Make handlers that write idempotent where
that matters, as jobs are. The wire format is in `wire.md`.

### Keeping state across a remount

A remount mounts a fresh copy of the page and its live layout: what
they held in memory, and only that, is gone. Form input comes back (the
browser restores it, `layouts.md`); anything else a restart mustn't
lose, the page names with a `Keep` method returning a pointer to it:

```go
type Player struct {
	store   *Store
	playing Playing         // what a restart mustn't lose
	tracks  map[int]Track   // loaded from playing's ids
}

// Playing is kept across a remount: plain data, so ids, not records.
type Playing struct {
	Station string
	History []int // track ids, oldest first
	At      int
}

func (p *Player) Keep() any { return &p.playing }

func (p *Player) Mount(c *loom.Ctx) {
	if p.playing.Station == "" { // nothing kept: a page load
		p.playing.Station = "mix"
	}
	p.tracks = p.store.Tracks(c.Context(), p.playing.History)
}
```

- After each render that changes it, the runtime encodes the kept state
  as JSON, seals it with `Config.Secret` and sends it to the browser,
  which holds it. A remount sends it back, and it is decoded into the
  fresh copy **before Mount**, so Mount sees it and loads what it names.
  Mount still runs: don't overwrite what was kept, and keep nothing
  Mount derives from the request (who the user is).
- It works for a page and for a live layout (`layouts.md`), each its
  own. It comes back after a restart or deploy (a remount), and after
  the page's first restart from a panic; a page load or reload starts
  afresh, as the user expects.
- Sealed means it comes back exactly as the server rendered it, for the
  browser it was given to, or not at all: the browser can't read, change
  or swap it. A deploy that changed the kept type so the old state no
  longer decodes drops it, and the page mounts afresh. `page.mount`'s
  `kept` says what happened (`events.md`).
- Kept state is exported fields of plain data that `encoding/json`
  encodes, else RT452 at startup. It travels back in a request header,
  so it is capped at `loom.MaxKept` (6 KB sealed, compressed); over that
  is RT454 and nothing is kept. Keep ids and choices, not records.
- `Run` refuses to start without `Config.Secret` outside dev (RT453).
- `loom.manifest` lists what each page and live layout keeps. In tests,
  `p.Remount()` remounts the page as a restart does (Testing below).

## Timers

A page can run one of its own handlers later with `c.SendAfter(d,
event, payload)`: after `d` the handler for `event` runs with
`payload` on the page's goroutine, as if the browser had sent it, then Assigns, and the page re-renders. A page has at most
one pending timer per event: calling SendAfter again for an event
replaces it, which is a debounce. `c.CancelTimer(event)` cancels it.

```go
"typed": func(c *loom.Ctx, payload loom.Payload) {
	p.draft = payload["value"]
	c.SendAfter(2*time.Second, "save", nil) // autosave once typing stops for 2s
},
"save": func(c *loom.Ctx, _ loom.Payload) { p.store.SaveDraft(c.Context(), p.draft) },
```

- A handler that calls SendAfter for its own event polls; start it in
  Mount. Several things due at once (toasts that expire) share one
  event: its handler drops what is due and sets the timer for the next.
- The event must be in the page's `Events`; another name is RT425,
  reported like `c.Fail`, and sets nothing.
- Timers live in the page's memory: they end with the session, and a
  restart or remount starts with none, so Mount sets the ones the page
  always needs. Work that must happen whether or not the page is open
  is a job (`jobs.md`).
- `loom inspect SID` lists a page's pending timers. In tests,
  `p.Advance(d)` runs the timers that fall due and `p.Timers()` lists
  them (see Testing below).

## State in the URL

A filter, a tab or a search that belongs in the URL, so it can be
shared, bookmarked and reloaded and the back button undoes it, lives in
the URL: `HandleParams` reads it, and `c.Patch` changes it without a page
load.

```go
// HandleParams runs after Mount, and whenever the URL changes within
// the route. It sets the page's state from the URL.
func (p *TeamPage) HandleParams(c *loom.Ctx, query url.Values) {
	p.view = query.Get("view")
	p.search = query.Get("q")
}

func (p *TeamPage) search(c *loom.Ctx, payload loom.Payload) {
	q := p.query() // url.Values from the page's state
	q.Set("q", payload["value"])
	c.Patch(c.Path("/team/"+p.key+"?"+q.Encode()), loom.Replace())
}
```

- `c.Patch(url)` in a handler, a timer or `HandleInfo` changes the page's URL to
  `url`, the same path with another query (`"?view=active"` resolves against the current
  URL), runs `HandleParams` with it, re-renders, and pushes the URL to
  the browser's history; `loom.Replace()` replaces the current entry
  instead, for each keystroke of a search. The session and everything
  the URL doesn't hold (an open menu, a draft) stay. In `Mount` or on a
  page load, `c.Patch` redirects, like `c.Navigate`.
- A plain `<a href>` to the same path with another query patches too, as
  do back and forward between such URLs, when the page has
  `HandleParams`: there is no `patch` attribute. A page without it mounts
  afresh, as any live navigation does. So tabs and filter links are
  ordinary links.
- Path values belong to Mount: a link to another path value of the route
  (`/team/DES` to `/team/ENG`) mounts the page afresh for it, as a link
  to another route does. Load what the path names in `Mount`, and only
  the query in `HandleParams`.
- Build the URL from the state, and let `HandleParams` set the state, so
  there is one way in: a link, `c.Patch`, a reload and the back button all
  go through it. Treat anything it doesn't understand as the default.
- `c.Request.URL` and `c.Request.PathValue` are the current URL's. A
  patch to another route or another path value is RT426 and a page without `HandleParams` is
  RT427; the page reports the error and navigates instead. A
  `HandleParams` with the wrong signature is RT428 at startup.
- In tests, `p.URL()` is the page's current URL, `p.Patch(url)` changes
  it as a link or the back button would, and `p.Follow(selector)` clicks
  a link: it patches to the same path, and otherwise sets `p.Navigated()`.
  `loom sessions` shows each page's current `url`, and the manifest says
  which pages keep state in the URL.

## Watching tables

A page that reads a table can watch it: when the table changes, every
page on the route re-runs `Assigns` and gets the diff, as after an
event. The change can come from anywhere: another page, a job, psql,
another server on the same database.

```go
app.Live("/", &TodoPage{}, templates.TodoApp, loom.Watch("todos"))
```

- The table must be tracked: `-- loom:track todos` in a migration
  makes it NOTIFY when a transaction that changed it commits. `app.Run`
  checks every watch (DB622), and so does `loomtest.Mount`.
- Renders are coalesced to a few a second per page however often the
  table changes, and a rolled-back change renders nothing.
- A watch is per table: every page on the route re-renders on any
  change to it, and re-reads what `Assigns` reads. For finer grain
  (one user's rows, a message to show), publish on a topic in
  `tx.AfterCommit` and subscribe to it, below.
- In tests, `p.TableChanged("todos")` re-renders a page as a change to
  the table would. `loom routes` and the manifest show each route's
  watches.
- A change costs what `Assigns` reads, once per open page. Measure it
  with a crowd: `loomtest.MountCrowd(t, app, "/", 50).TableChanged("todos")`
  returns the change's `Load` (pages, queries, their time), so a test
  can hold the route to a budget: a count shared by every page should
  be one query per change, not one per page. In a running app, `loom
  stats` shows `page.watch` by route, per minute, with queries each.

## Rate limits

A rate limit caps how often one key may load a route's pages, send its
events or call its handlers. It is a group option (counting across the
group's routes and handlers, like `loom.Auth`) or a route option:

```go
app.Group(loom.RateLimit(loom.Limit{Key: loom.ByIP, Max: 120, Per: time.Minute})).Mount("/auth", auth.New(cfg))
app.Live("/invite", &InvitePage{}, templates.Invite,
	loom.RateLimit(loom.Limit{Key: loom.ByUser, Max: 20, Per: time.Hour, On: []string{"send"}}))
```

- `Key` is whose attempts count: `loom.ByIP` (the client, through
  `TrustedProxies`), `loom.ByUser`, `loom.ByPayload("email")` (trimmed,
  lowercased) or `loom.KeyBy(name, func(c, event, payload) string)`. An
  empty key isn't counted. `On` names the events counted; without it,
  every load, event and handler request is.
- Over the limit nothing runs: a load or handler request answers 429
  with `Retry-After` and the error page, an event's ack is 429 (RT436),
  and loom.js gives `<html>` class `loom-limited` and unhides elements
  with `data-loom-limited` until `Retry-After`, so the page can say so.
- The window slides, and refused attempts count, so hammering keeps a
  key out. With a database the counts are in `loom_rate_limits`
  (one of Loom's migrations), shared by every node; else in memory.
- A limit with no key, `Max` under 1, no `Per`, or an `On` event the
  page doesn't handle panics with RT437. The manifest and `loom routes`
  show every route's limits.

## Messages from elsewhere

A page can hear about things that happen outside it: a background job
finishing, or another page changing shared data. It subscribes to a topic
with `c.Subscribe` (in Mount or a handler) and receives each message
published on it in `HandleInfo`, on its own goroutine like a handler:

```go
func (p *TodoPage) Mount(c *loom.Ctx) {
	c.Subscribe("todos")
	p.load(c)
}

func (p *TodoPage) HandleInfo(c *loom.Ctx, msg loom.Message) {
	// msg.Topic, msg.Event, msg.Payload (map[string]string)
	p.load(c) // then Assigns runs, as after an event
}
```

- Publish with `app.PubSub().Publish(loom.Message{Topic: "todos", Event:
  "changed"})`, or from a job with `c.Broadcast(topic, event, payload)`
  (see `jobs.md`). With a database, a message reaches the pages of every
  node, by Postgres NOTIFY; without one it stays in-process
  (`Config.PubSub` replaces it). Keep payloads small (RT447 over 8000
  bytes): send an id and load the rest. See `deploy.md`.
- A page that subscribes without a `HandleInfo` method is error RT410.
- Messages are like events but come from the server, so the browser
  can't forge them. Subscriptions end when the page closes and are made
  again after a restart.

## Developing

`loom dev` runs the app (the main package in `cmd/`), rebuilds and restarts it when a
`.hx` or `.go` file changes, and reloads open pages; build errors show in
the browser. While it runs, `loom sessions` lists open pages, `loom
inspect SID` shows a page's assigns, HTML and timers, `loom render Component
--assigns file.json` renders any component, and `loom errors` shows
recent runtime errors. See `cli.md`.

## Testing without a browser

`loomtest.Mount` runs the page an app serves at a path the way the
server does, without HTTP:

```go
func TestTodoPage(t *testing.T) {
	p := loomtest.Mount(t, NewApp(), "/", loomtest.NoLayout())
	p.Submit("form.new-todo", loom.Payload{"title": "Milk"})
	p.AssertHTML(".todo-list li", 1)
	p.AssertDiff(`{"d":{"0":{"o":[["1",[...]]]}}}`)
}
```

- Mount first fails the test where the app would fail to start or run
  stale code, once per app: it runs `loomtest.Validate(t, app)`, which
  runs `app.Validate` (the checks `app.Run` makes before serving: a job
  queue with no workers, a watched table that isn't tracked) and checks every
  generated `*_hx.go` is what `loom build` writes now (RT421). Call
  `loomtest.Validate` yourself in a test of an app with no live pages.
- `loomtest.Cookie(name, value)` sends a cookie with the page's request,
  for pages behind `loom.Auth`. Path values (`{id}`) are set from the
  route, so `c.Request.PathValue("id")` works as in the server.
- The page renders inside its app layout unless `loomtest.NoLayout()` is
  given; diffs then include the layout. `p.Document()` is the whole
  document with the root layout; `p.Title()` is the page's title and
  `p.Meta()` its metadata (`c.Meta`).
- `p.Click(selector)`, `p.Submit(selector, fields)`, `p.Change(selector,
  value)` and `p.Input(selector, value)` act on the rendered markup as a
  user would: the nearest `on:click` (`on:submit`, ...) at or above the
  element sends its event with its `val-*` attributes, a form its named
  fields. So the test also checks the names the template sends: a
  `name="titel"` given to Submit, or a selector matching no element or
  several, fails it. Prefer them to `p.Event`.
- `p.Event(name, payload)` sends an event by name and re-renders, for
  events no markup sends directly (a behavior's emit has `p.Emit`). An
  event the page has no handler for fails the test (RT423), as does one
  no element on the page binds (RT438: open the menu before picking
  from it, as a person would), and any runtime error, including one
  reported with `c.Fail`. `p.AssertRefused(name, payload)` checks that
  a hidden control's event is refused and changes nothing, and
  `p.AssertLimited(name, payload)` one a rate limit refuses (it returns
  the wait); `loomtest.LimitClock(t, app)` gives the limits a fake clock
  to `Advance`. Each event also applies the diff to the previous tree,
  as the browser does, and checks the result equals the new render.
- `p.AssertHTML(selector, n)` checks how many elements match a CSS
  selector: tag, `#id`, `.class`, `[attr]`, `[attr=value]` and descendant
  spaces. `loomtest.Count(html, selector)` is the same for any HTML.
  `p.AssertText(selector, text)` checks that a matching element contains
  the text.
- `p.AssertDiff(json)` checks the wire diff of the last event (see
  `wire.md`); `""` means the event changed nothing. `p.Diff()` returns it.
- `p.HTML()` returns the current HTML of the live tree, for
  `loomtest.Snapshot`.
- `p.Navigated()` is the URL the page last navigated to with
  `c.Navigate` (see `layouts.md`), or `""`. `p.URL()`, `p.Patch(url)` and
  `p.Follow(selector)` test a page's URL state (above). `p.Navigate(path)`
  returns the page a live navigation to path would, carrying a live
  layout's state (`layouts.md`). `p.Remount()` returns the page a
  remount after a restart would: a fresh copy, given what the page and
  its live layout keep (`Keep`, above), so a test shows what a deploy
  loses.
- `loomtest.AssertNotFound(t, app, path)` checks that the page at path
  calls `c.NotFound` in Mount; `Mount` fails the test if it does.
- `p.Info(loom.Message{...})` delivers a message as if published on a
  topic the page subscribed to. `p.Listen(ps)` delivers everything
  published on `ps` to the page's topics as it happens; with
  `loomtest.Jobs`, `p.Listen(q.PubSub())` lets jobs' broadcasts reach the
  page during `q.Drain`.
- Timers run only when the test moves the page's fake clock:
  `p.Advance(d)` runs each `c.SendAfter` timer that falls due, in order,
  and checks its diff like an event; `p.Timers()` maps each pending
  timer's event to the time until it runs.

### Flow tests

A flow test is a user's path through a feature, in steps of plain words,
each with the code that does or checks it:

```go
func TestFlowPlanTheDay(t *testing.T) {
	var p *loomtest.Page
	f := loomtest.Flow(t, "A guest plans the day")
	f.Step(`They add "Buy milk"`, func() {
		p = loomtest.Mount(t, app, "/")
		p.Submit("form.new-todo", loom.Payload{"title": "Buy milk"})
		p.AssertText(".todo-list li", "Buy milk")
	})
}
```

The steps are kept in `testdata/flows/<test>.md`, a heading and a
numbered list, which `go test -update` writes; a passing test whose
steps differ from the file fails. The file is what a person reads and
agrees: acceptance criteria in a pull request, with no Go to read. It
can be written first, and the test grown step by step to match it. The
test prints the flow as a checklist: ✓ passed, ✗ failed, ○ steps of the
file it didn't reach. A `t.Fatal` in a step ends the flow there.
`examples/todo/flow_test.go` is an example.

Components are tested with `loomtest.Render` and `loomtest.Snapshot`.
