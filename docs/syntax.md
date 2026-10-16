# Template syntax

Loom templates live in `.hx` files. The syntax will feel familiar if
you know Phoenix HEEx; if you do, read "Differences from HEEx" first.

## Components

A file holds one or more components. Each starts with a declaration at the
start of a line, followed by its markup:

```heex
component TodoList(todos []Todo, filter string)

<ul>...</ul>

component Footer(count int, left slot, inner_block slot)

<footer>...</footer>
```

- Names are PascalCase: `TodoList`.
- Parameters are `name Type`, separated by commas, on one line.
  Parameters are the component's assigns.
- Types: `string`, `int`, `float`, `bool`, `slot`, a struct declared in a
  `.go` file in the same directory (`Todo`), or a list of any of these
  (`[]Todo`, `[][]Card`). See "Go types" below. `form[SettleInput]` is a
  form of such a struct; `settle.amount` is one of its fields, of type
  `field` (`name`, `value`, `error`), which `<.input field={...} />`
  takes; a `field` parameter may be left out. See `forms.md`.
- `name Type = literal` gives a default: `busy bool = false`,
  `title string = ""`. Defaults live only in the signature; a parameter
  without one is required.
- `size: :sm | :md = :md` is an enum: one of a fixed set of values.
- `on:click event` is an event parameter: the caller's handler. See
  "Events".
- `label string @preview("Save", "Delete forever")` gives sample values
  for the preview gallery, after any default. Literals only; a slot's
  are markup strings. See `components.md`.

```heex
component Button(label string, variant: :primary | :ghost = :primary, busy bool = false, on:click event)
```
- Whitespace around a component's markup is trimmed. Whitespace inside is
  kept as written.

## Expressions

`{expr}` inserts a value into text or an attribute. Text is HTML-escaped.

| Form | Example |
|---|---|
| literals | `42`, `4.2`, `"text"`, `true`, `false` |
| names | `filter` (an assign or a `:for` variable) |
| field access | `t.title` |
| indexing | `items[0]` |
| operators | `!` `-` (unary); `* / %`; `+ -`; `== != < <= > >=`; `&&`; `\|\|` (tightest first) |
| inline if | `if t.done, "done", ""` |
| helper calls | `len(todos)`, `raw(html)` |
| grouping | `(a + b) * 2` |

Strings use double quotes and support `\n`, `\t`, `\"`, `\\`.

Expressions are pure. There is no assignment, no method calls and no
arbitrary Go: only registered helpers can be called. Compute anything else
in Go and pass it in as an assign.

Builtin helpers: `len(list_or_string)`, `raw(string)` (insert HTML
unescaped; the only escape hatch), `render_slot(slot)`, `path(string)`
and `asset(string)`. Your own helpers are described under "Go types".

`path("/jobs/" + job.id)` is a link within the page's mount: under a
sub-app mounted at `/admin/jobs` it gives `/admin/jobs/jobs/7`, and
outside any mount the path itself. `asset("app.css")` is the URL of a
file embedded with `loom.Assets`, with a content hash for caching;
`asset("player/")` is a whole directory's URL (`subapps.md`). In a
reusable package (a Go file with a `//loom:package` line), a static
`href="/..."` is a warning (TC336): write `href={path("/...")}`. See
`subapps.md`.

There are no implicit conversions and no truthiness. Both sides of an
operator have the same type (`int + float` is an error), and conditions
are bools: write `len(todos) > 0`, not `todos`.

Values render as text like this: strings are HTML-escaped, ints and floats
in their shortest form, bools as `true`/`false`. Lists and structs can't
be rendered directly.

To write a literal brace or `<` in text, use a string: `{"{"}`, `{"<"}`.

## Elements and attributes

HTML is written as usual. Attribute values are either static or dynamic:

```heex
<input type="checkbox" checked={t.done} disabled />
```

- `name="text"` (or `'text'`) is static. It is never interpolated: `{` inside
  quotes is literal.
- `name={expr}` is dynamic. A string, int or float renders `name="value"`;
  a bool renders `name` when true and nothing when false
  (`checked={t.done}`).
- `name` alone is a boolean attribute.
- Void elements (`input`, `br`, `img`, `meta`, `link`, `hr`, ...) take no
  closing tag; `<br>` and `<br />` are both fine. Other elements may
  self-close: `<div />` renders `<div></div>`.
- `<script>` and `<style>` contents are raw text; braces in them are literal.
- HTML comments and `<!DOCTYPE html>` pass through unchanged.

## Directives

Directives are attributes starting with `:`. They work on any element or
component call.

```heex
<li :for={t <- todos} :if={!t.done} :key={t.id}>{t.title}</li>
```

- `:for={item <- list}` renders the tag once per item. `item` is in scope
  inside the tag, including its other attributes.
- `:if={condition}` renders the tag only when the condition is true. With
  `:for`, it filters each item.
- `:key={expr}` gives each item of a `:for` a stable identity, so updates
  move existing DOM nodes instead of rebuilding them. Use it on every list
  that can change; a `:for` over records without one is warning TC348.

## Component calls and slots

```heex
<.footer count={len(todos)}>
  <:left>Items</:left>
  Shown in the default slot
</.footer>
```

- `<.name ...>` calls the component whose PascalCase name matches:
  `<.footer>` calls `Footer`, `<.todo_item>` calls `TodoItem`.
- Attributes set the callee's parameters. Parameters without a default
  are required, except slots and events. A quoted value is a string and a
  bare attribute is `true`; other types need braces: `count={1}`.
- An enum takes a quoted value, `variant="ghost"`; an unknown value is an
  error with the closest one (TC338). It also takes an enum parameter of
  the same type, `size={size}`, but not a string variable.
- `<:name>...</:name>` passes markup to the callee's `slot` parameter
  `name`. Named slots must be direct children of the call.
- Everything else inside the call is the default slot, received as the
  `inner_block` parameter.
- The callee renders a slot with `{render_slot(left)}`.
- `<.footer count={0} />` calls with no slot content.

## Class lists

`class={...}` lists values separated by spaces. Empty values are left
out, and an inline if may leave out its else:

```heex
<button class={"button" variant size if busy, "busy"}>
```

Class names are listed, never joined: `{"btn-" + size}` is TC341. Inside
a component, compare an enum with its values: `:if={size == "sm"}`.

## Imports

```heex
import "example.com/app/components"

component TodoApp(todos []Todo)
```

`import` lines come before the first component and name a directory of
`.hx` files in the same module by its import path. Its components are
called by name, like local ones (`<.button>`); a name clash or an unused
import is TC342. `loom components add` puts the starter kit's components
in `components/`; see `components.md`.

## Layouts

A component with an `<html>` element is a root layout: the document pages
are served in. Only it may use the builtin `<.live_title default="App">{title}</.live_title>`,
`<.live_meta />`, `<.loom_styles />` and `<.loom_scripts />`, and it must render
`{render_slot(inner_block)}`.
An app layout (nav, chrome) is an ordinary component with an
`inner_block` slot. Both are attached to routes in Go; see `layouts.md`.

## Styles

```heex
component Card(title string)

<style>
  .card { padding: token(space.4); background: token(color.surface); }
</style>
<div class="card">{title}</div>
```

One `<style>` block at the top level of a component, scoped to it. Values
come from `tokens.toml` with `token(group.name)`; raw colors, spacing,
radii, font sizes and shadows are errors. See `components.md`.

## Events

```heex
<button on:click="delete" val-id={t.id}>Delete</button>
```

- `on:<dom event>="name"` sends event `name` to the page's server process
  when the DOM event fires. The name is a static string. Events:
  `click`, `change`, `input`, `submit`, `keydown`, `keyup`, `focus`, `blur`.
- Every `val-*` attribute on the same element is sent as the payload:
  `val-id={t.id}` sends `{"id": "..."}`. Forms also send their named
  fields, inputs their `value`, checkboxes `checked`, key events `key`.
  With none of these the payload is empty; a missing key reads as `""`.
- Name events in snake_case, like everything else: `clear_completed`.

A component that lets callers choose its event declares an event
parameter, and forwards it to an element (or another call) by its name
with `:` as `_`:

```heex
component Button(label string, on:click event)

<button on:click={on_click}>{label}</button>
```

The caller writes `<.button label="Delete" on:click="delete" val-id={t.id} />`;
the `val-*` attributes travel with the event. Left out, there is no
handler.

The page's Go value handles events: `Events` maps each name the
template sends to a func that changes the page's state, and `Assigns`
turns the state into the template's assigns after each one:

```go
func (p *TodoPage) Events() loom.Events {
	return loom.Events{"delete": p.delete}
}

func (p *TodoPage) delete(c *loom.Ctx, payload loom.Payload) {
	p.remove(payload["id"]) // loom.Payload is map[string]string
}

func (p *TodoPage) Assigns(c *loom.Ctx) templates.TodoAppAssigns { ... }
```

An event the template sends with no handler in `Events` is RT422, at
startup, with the template's position.

While an event is on its way, the element that sent it has class
`loom-pending`, and static hints on it apply until the server's update
arrives:

```heex
<.button label="Delete" on:click="delete" val-id={t.id} optimistic:disabled />
<li on:click="select" val-id={t.id} optimistic:class="selected">{t.title}</li>
```

`optimistic:class="a b"` toggles those classes, `optimistic:disabled`
and `optimistic:hidden` set those. Hints go beside an `on:*` (TC351),
take no expressions (TC350), and on a component call travel with its
events, like `val-*`.

More in `live.md`: the app, the payload table, driving pages in tests.

## Behaviors

Client-side code (focus, keyboard, measuring) is a typed behavior,
declared in a template and written as an ES module beside it:

```heex
behavior dropdown(open bool, value string) emits (select(value string), close) requires [role=listbox]

component Menu(open bool, value string)

<ul role="listbox" use:dropdown={open: open, value: value} on:select="pick" on:close="close">...</ul>
```

- One line, before the components: params (scalars, enums, lists of
  scalars, optional literal defaults), the events it `emits` with their
  params, and attributes the element must have (`requires`).
- `dropdown.js` beside the template exports
  `mount(el, props, emit)`, returning `{update(props), destroy()}`
  (both optional). `emit("select", {value: "pear"})` sends the element's
  `on:select` event, with its `val-*` attributes, like a DOM event.
- `use:name={prop: expr, ...}` on an HTML element, one per element; bare
  `use:name` when every prop has a default. Props are checked like a
  call's; `on:x` on an element must be a DOM event or one its behavior
  emits.
- Behaviors are imported with their package, like components.

See `components.md` for the lifecycle.

## Go types

Templates compile to Go in the package of their directory: `loom build`
writes `todo_list_hx.go` next to `todo_list.hx`. After editing a `.hx`
file, run `loom build` (or use `loom dev`, which does it on save) before
compiling the Go code that uses the new assigns.

```go
// todo.go, next to todo_list.hx
type Todo struct {
	ID    string
	Title string
	Done  bool
}
```

- Struct types used in signatures are declared in the same Go package.
- Fields are read by their snake_case names: Go `Title` is `t.title`,
  `CreatedAt` is `t.created_at`, `UserID` is `t.user_id`.
- Fields may be `string`, `int`, `float64`, `bool`, structs from the same
  package, or slices of those. Other fields exist but can't be used in
  templates (error TC322).
- A helper is a pure Go function in the same package, marked with a
  `//loom:helper` line in its doc comment, taking and returning template
  types. It is called by its snake_case name:

```go
// FormatPrice formats cents as dollars.
//
//loom:helper
func FormatPrice(cents int) string { ... }
```

```heex
<span>{format_price(item.price)}</span>
```

Each component becomes a Go value and an assigns struct, with one field
per parameter in PascalCase:

```go
html := templates.TodoList.RenderHTML(templates.TodoListAssigns{Todos: todos, Filter: "all"})
```

Test components without a browser using `loomtest`:

```go
html := loomtest.Render(t, templates.TodoList, templates.TodoListAssigns{Todos: todos})
loomtest.Snapshot(t, html) // testdata/snapshots/<TestName>.html; go test -update to refresh

p := loomtest.Mount(t, app, "/") // the live page app serves at "/"
p.Event("delete", loom.Payload{"id": "1"})
p.AssertHTML("li.done", 0) // CSS selector, expected count
```

## Differences from HEEx

- Assigns are referenced by name, without `@`: `{todos}`, not `{@todos}`.
- Components declare a typed signature (`component Name(params)`) instead
  of `attr`/`slot` declarations: `size: :sm | :md = :md` is
  `attr :size, values: [:sm, :md], default: :md`.
- `class={["btn", @busy && "busy"]}` is `class={"btn" if busy, "busy"}`.
- Components from another directory are imported with `import "path"`
  and called unqualified, not as `<Module.name>`.
- Expressions are Loom's small expression language, not Elixir: inline if
  is `if c, a, b`, not `if c, do: a, else: b`.
- Events use `on:click="name"` and `val-*` instead of `phx-click` and
  `phx-value-*`; instant feedback is `optimistic:*` hints and the
  `loom-pending` class, not `JS` commands and `phx-click-loading`.
- Client code is a typed `behavior` attached with `use:name={...}`, not a
  `phx-hook` with an untyped `this.pushEvent`.
- Strings join with `+`, not `<>`.
- Links within a mount use `path("/x")`, not `~p"/x"`.
- No `<%= %>` EEx tags.

The compiler reports each HEEx habit above with a fix.
