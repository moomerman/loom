# Error reference

Every compile error has a stable code, a position with a code excerpt, a
message and a fix:

```
todo.hx:4:3: error PA203: <li> is never closed
  |
4 |   <li :for={t <- todos}>
  |   ^^^
  = fix: add </li>; close it before the end of the file
```

`loom check --json` prints the same errors as JSON (schema in `cli.md`).
Codes never change meaning; a code that is retired (the SY sync codes, DB617) is not reused. Prefixes: `LX` lexing, `PA` parsing, `TC`
type checking, `ST` styles and design tokens, `RT` the live runtime, `JB`
background jobs, `DB` the data layer, `FM` forms, `SG` file storage, `AU` the auth sub-app.

Most codes are errors, which stop `loom build`. A few are warnings
(marked below): `loom check` reports them, with `"severity": "warning"` in
`--json`, but the build goes ahead.

Each entry below gives the cause and the usual fix. The compiler's own fix
text is tailored to the case at hand, so read it first.

## Lexing (LX)

### LX101
A `}` appears in text with no matching `{`. Remove it, or write a literal brace as `{"}"}`.

### LX102
An HTML comment `<!--` has no closing `-->`. Close it.

### LX103
A `<!` declaration such as `<!DOCTYPE html` has no closing `>`. Add it.

### LX104
A closing tag is malformed: `</` without a name, or `</p` without `>`. Write `</p>`. A literal `<` in text is written `{"<"}`.

### LX105
An opening tag never reaches its `>` (often because another `<` starts first). End it with `>` or `/>`.

### LX106
A character that can't start an attribute appears inside a tag, e.g. a stray `=`. Attributes look like `name="text"` or `name={expr}`.

### LX107
An attribute has `=` and no value: `class=>`. Give it a value or remove the `=`.

### LX108
A quoted attribute value has no closing quote. Add it.

### LX109
An attribute value is neither quoted nor in braces: `class=main`. Write `class="main"` or `class={expr}`.

### LX110
A `{` is never closed. Add the matching `}`.

### LX111
A `<script>` or `<style>` element is never closed. Add `</script>` or `</style>`.

### LX112
An expression uses single quotes. Strings use double quotes: `"text"`.

### LX113
An expression contains a character that isn't part of the expression language, e.g. a single `&` (use `&&`), a `:` from Elixir's `if x, do: a` (use `if x, a, b`), or `;`.

### LX114
A string literal is not closed before the end of the line. Add the closing `"`.

### LX115
A string uses an unknown escape. Supported escapes: `\n`, `\t`, `\"`, `\\`.

### LX116
An EEx tag like `<%= x %>` is used. Write `{x}`; use `:if` and `:for` for conditions and loops.

### LX117
An assign is written with `@`, as in HEEx (`@todos`). Loom refers to assigns by name: `todos`.

## Parsing (PA)

### PA201
Markup appears before the first `component` declaration. Start the file with `component Name(params)`.

### PA202
The file has no `component` declaration. Add one.

### PA203
A tag is never closed. Add its closing tag before the end of the file or the next `component`.

### PA204
Two components in one file have the same name. Rename one.

### PA205
A component call uses an invalid name. Calls use the snake_case form of the component's name: `<.todo_item>` calls `component TodoItem`.

### PA206
A named slot has an invalid name, e.g. `<:my-slot>`. Slot names are identifiers: `<:my_slot>`.

### PA207
A named slot `<:name>` is not directly inside a component call. Move it into the `<.component>` it belongs to.

### PA208
An HTML tag name contains characters other than letters, digits and `-`.

### PA209
The same attribute is set twice on one tag. Remove one.

### PA210
A directive (`:for`, `:if`, `:key`) is used on a named slot. Put it on the component call, or on an element inside the slot.

### PA211
A directive's value isn't an expression in braces: `:if="ok"`. Write `:if={ok}`.

### PA212
An unknown directive such as `:each`. The directives are `:for`, `:if` and `:key`.

### PA213
An event binding is malformed. Write `on:<dom event>="event_name"` with a quoted, static event name; payload values go in `val-*` attributes. Inside a component, forward an event parameter with an expression: `on:click={on_click}`.

### PA214
`:key` is used on a tag without `:for`. Add `:for` or remove `:key`.

### PA215
A void element (`input`, `br`, `img`, ...) has a closing tag. Remove it; write `<input />` or `<input>`.

### PA216
A closing tag has no matching opening tag. Remove it or add the opening tag.

### PA217
A closing tag closes an outer element while an inner one is still open: `<ul><li>one</ul>`. Close the inner element first.

### PA218
A closing tag matches no open element. Fix the tag name.

### PA219
An empty expression `{}`. Put an expression inside, or remove the braces.

### PA220
An expression uses `=`. Templates can't assign; use `==` to compare.

### PA221
Extra tokens follow a complete expression, e.g. `{first last}`. Usually a missing operator.

### PA222
An expression ends too early, e.g. `{a +}`. Complete it.

### PA223
A value was expected but something else was found, e.g. `{a + )}`.

### PA224
`<-` is used outside `:for`. Repeat markup with `:for={item <- list}`.

### PA225
Something other than a helper is called, such as a method: `{user.Name()}`. Templates call only registered helpers like `len(list)`; compute other values in Go and pass them in.

### PA226
Expected punctuation is missing: an unclosed `(` or `[`, a missing `,` between arguments, or a missing field name after `.`.

### PA227
An inline if is incomplete. It has three parts: `if cond, then_value, else_value`.

### PA228
A `:for` is malformed. Write `:for={item <- list}`.

### PA229
A component declaration is malformed. Write `component Name(param Type, ...)`.

### PA230
A component name doesn't start with an uppercase letter. Use PascalCase: `component TodoList`.

### PA231
A component has two parameters with the same name. Rename or remove one.

### PA232
A parameter has no type. Write `name Type`, e.g. `todos []Todo`.

### PA233
A parameter type is malformed. Types are names (`string`, `int`, `bool`, `Todo`, `slot`) or lists (`[]Todo`).

### PA234
A number literal is too large or malformed. Integers are 64-bit.

### PA235
A Phoenix LiveView `phx-*` attribute is used. Bind events with `on:click="name"` and pass payload values as `val-name={...}`.

### PA236
A list key is written `key={...}`. Write `:key={...}`, as in HEEx.

### PA237
`<>` is Elixir's string concatenation, not a Loom operator. Join strings
with `+`: `path("/jobs/" + job.id)`.

### PA238
A parameter in a signature is malformed. Enums are written
`size: :sm | :md` (colon after the name, each value once), defaults are
literals (`label string = ""`, `size: :sm | :md = :md`), and event
parameters are `on:click event`, with no default. Only enums take the
colon: `label string`, not `label: string`. See `components.md`.

### PA239
An import line is malformed: write `import "module/path/components"`,
quoted, before the first component, and import each path once.

### PA240
A behavior declaration is malformed. Write it on one line, with no
markup after it: `behavior dropdown(open bool, value string) emits
(select(value string), close) requires [role=listbox]`. Names are
snake_case, each name is declared once, params take no `on:` events,
and `requires` lists attribute selectors like `[role=dialog]`.

### PA241
A `use:` attribute is malformed. Write `use:name={prop: expr, ...}`, or
bare `use:name` with no props, once per element, on an HTML element (not
a component call or slot). The name is the behavior's snake_case name.

### PA242
An `@preview(...)` list is malformed. It follows a component parameter's
type and default, once, and holds literals only: strings, numbers and
`true`/`false`, e.g. `label string @preview("Save", "Delete")`. Behavior
parameters take no `@preview`. See `components.md`.

## Type checking (TC)

Type errors are all reported together, unlike syntax errors.

### TC301
A name is neither a parameter of the component nor a `:for` variable. Check the spelling; the fix suggests the closest name in scope.

### TC302
A struct has no field with that name. Template field names are the snake_case form of the Go field name: Go `CreatedAt` is `created_at`, `UserID` is `user_id`.

### TC303
A field is read from a value that isn't a struct, e.g. `name.length`.

### TC304
A parameter type is unknown or not allowed. Types are `string`, `int`, `float`, `bool`, `slot`, structs declared in a `.go` file next to the template, and lists of those. Go spellings like `float64` get a targeted fix. Lists of slots aren't supported.

### TC305
An operator is applied to the wrong types. Both sides of a binary operator must have the same type: `+` takes ints, floats or strings; `- * /` ints or floats; `%` ints; `< <= > >=` ints, floats or strings; `== !=` two values of the same basic type; `&& || !` bools. There are no implicit conversions.

### TC306
A condition (`:if`, inline `if`) isn't a bool. Templates have no truthiness: write `len(todos) > 0`, not `todos`.

### TC307
The two branches of an inline `if` have different types. Make them the same.

### TC308
Indexing something that isn't a list, or with an index that isn't an int.

### TC309
`:for` loops over something that isn't a list.

### TC310
An unknown helper is called. Templates call builtins (`len`, `raw`, `render_slot`) and Go functions in the template's package marked with a `//loom:helper` comment, by their snake_case name: Go `FormatDate` is `format_date(...)`.

### TC311
A helper is called with the wrong number of arguments.

### TC312
A helper argument has the wrong type.

### TC313
A list or struct is rendered as text. Render a list with `:for` and a struct by its fields.

### TC314
An attribute is given a value that can't be an attribute: a list, a struct, or html from `raw()`.

### TC315
A component call names no component in the package. Calls use the snake_case name: `<.todo_item>` calls `component TodoItem`. HEEx's `<.link navigate={...}>` has no Loom twin: write `<a href={...}>`, which navigates live (`layouts.md`).

### TC316
A component call passes an attribute that isn't one of the component's parameters. `navigate` or `patch` on a link component means HEEx's `<.link>`: pass the URL as `href`.

### TC317
A component call doesn't pass all of the component's parameters. Every parameter except slots is required.

### TC318
A component argument has the wrong type. A quoted value is a string and a bare attribute is `true`; anything else needs braces: `count={1}`.

### TC319
A named slot isn't declared by the component, or the call has inner content but the component has no `inner_block slot` parameter.

### TC320
A named slot is passed twice in one call. Pass all its markup in one `<:name>`.

### TC321
A named slot has attributes. Not supported yet; pass the value as a component parameter.

### TC322
A struct field has a Go type templates can't use (e.g. `time.Time`, `map`, pointers). Add a field of a usable type, such as a formatted string, or a helper.

### TC323
`:key` is neither a string nor an int.

### TC324
A slot is used as a value: rendered directly (`{header}`) or passed as an attribute. Render slots with `{render_slot(header)}`; pass them as `<:header>...</:header>`.

### TC325
Two `.hx` files in one package declare the same component. Component names are unique per package.

### TC326
A Go function marked `//loom:helper` can't be a helper: its parameters or result aren't template types, it doesn't return exactly one value, or its name clashes with a builtin. This error points at the Go file.

### TC327
An event binding (`on:click`) is used on a component call. Bind events on an element inside the component.

### TC328
A `:for` variable reuses a name already in scope, or a component's
generated Go names (`Todo` and `TodoAssigns` for `component Todo`) clash
with a type declared in the package's Go files. Pick another name, so
each name means one thing.

### TC329
A root layout (a component with an `<html>` element) has no `<.loom_scripts />`, so the page would never become live. Add `<.loom_scripts />` at the end of `<body>`. See `layouts.md`.

### TC330
A root layout has no `inner_block slot` parameter, or never renders it. The page goes where `{render_slot(inner_block)}` is: declare the slot and render it inside `<body>`.

### TC331
A root layout isn't a complete document: it lacks `<!DOCTYPE html>` before `<html>`, or `<head>` or `<body>` directly inside `<html>`. Write the whole document, as in `layouts.md`.

### TC332
`<.live_title>`, `<.live_meta />`, `<.loom_styles />` or `<.loom_scripts />` is used wrongly: twice, with `:for`/`:if`/`:key` or a named slot, with an attribute other than `default`, `prefix` and `suffix` (each a string) on `<.live_title>`, with markup instead of text inside `<.live_title>`, or with any attribute or content on the others.

### TC333
`<.live_title>`, `<.live_meta />`, `<.loom_styles />` or `<.loom_scripts />` is used outside a root layout. They belong in the document's `<head>` and `<body>`; app layouts and pages can't use them. A page sets its title with `c.Title(...)` and its metadata with `c.Meta(...)` in Go.

### TC334
A component is named after a builtin component (`LiveTitle`, `LoomStyles`, `LoomScripts`). Rename it.

### TC335
A literal argument to `path` or `asset` can't resolve against the mount
prefix. `path` takes a path within the mount starting with `/`
(`path("/jobs")`, not `path("jobs")` or `path("//host/x")`); `asset` takes
a file name in the group's embedded assets (`asset("app.css")`, not
`asset("/_assets/app.css")`). See `subapps.md`.

### TC336
Warning, in a reusable package (a Go file has a `//loom:package` line): a
static `href`, `src`, `action`, `formaction` or `poster` starts with `/`,
so it ignores the prefix the package is mounted at. Write
`href={path("/jobs")}`, or `src={asset("logo.png")}` for an embedded
asset. URLs under `/_loom/` and other hosts are fine. Warnings don't fail
`loom check` or `loom build`.

### TC337
A root layout has no `<.loom_styles />`, so pages would render without their components' styles. Add `<.loom_styles />` inside `<head>`. See `components.md`.

### TC338
A value isn't one of an enum parameter's values: in a call
(`variant="primry"`), a default, or a comparison (`size == "lg"`). The fix
names the closest value. A string variable can't be passed to an enum
parameter, since its value isn't known: pass a value as written
(`variant="primary"`), or declare the parameter you pass from as an enum
too. See `components.md`.

### TC339
A parameter's default doesn't fit its type (`count int = "none"`), or a
list, struct or slot parameter has a default. Write a literal of the
parameter's type, or remove the default to make the parameter required.

### TC340
An event parameter is misused: `on:click={x}` forwards an event, but `x`
isn't an event parameter; `val-*` is passed to a component that has no
event parameter; or an event parameter is rendered as text or an
attribute. Name a server event in quotes (`on:click="save"`), or declare
`on:click event` in the signature and forward it with
`on:click={on_click}`.

### TC341
Class names are joined with `+` (`class={"btn-" + size}`). List each
class as a value instead, `class={"btn" size if busy, "busy"}`, and style
the combination in the component's `<style>` (`.btn.sm`), so every class
name appears whole in the source.

### TC342
An import is wrong: no directory of `.hx` files at that import path in
this module, nothing from it is called, or it exports a component with
the same name as a local one or one from another import. Calls aren't
qualified, so each name must mean one component: rename the local one,
or remove the import.

### TC343
A behavior can't be declared as written: a prop has a type other than a
scalar, an enum or a list of scalars, an emit param isn't a scalar, or
the module beside the template (`name.js`) is missing or doesn't
`export function mount(el, props, emit)`.

### TC344
A `use:` doesn't match the behavior: no behavior by that name (declare
it, or import its package), a prop it doesn't declare, a required prop
left out, or a prop value of the wrong type.

### TC345
The element doesn't meet the behavior's `requires`: add the attribute,
for example `role="listbox"`, with the value the behavior names. When
the value is dynamic, the check runs in the browser in dev (RT417).

### TC346
`on:x` on an element must be a DOM event (`click`, `change`, `input`,
`submit`, `keydown`, `keyup`, `focus`, `blur`) or an event the element's
behavior declares in `emits (...)`.

### TC347
`@preview` is on a parameter that can't take one, or its values have the
wrong type. Enum and bool parameters need none: previews already show
each of their values. Lists, structs and events can't be given a value
with a literal; preview a component that builds the value instead. The
values must match the parameter's type, and a slot's values are markup
strings, e.g. `inner_block slot @preview("<p>Some content</p>")`.

### TC349
An `optimistic:*` hint Loom doesn't have. The hints are
`optimistic:class="names"`, `optimistic:disabled` and
`optimistic:hidden`; the fix suggests the closest.

### TC350
A hint's value is the wrong kind. `optimistic:class` takes class names
as a quoted string, `optimistic:class="done"`; `optimistic:disabled` and
`optimistic:hidden` take none. Hints are static: they show the user
their event is on its way, and the server's render decides the result,
so a value can't come from an expression.

### TC351
A hint is on a tag with no `on:*` event, so nothing would ever apply it.
Put it on the element or component call that sends the event it
anticipates: `<.button label="Delete" on:click="delete" optimistic:disabled />`.

### TC352
A `form[Input]`'s input struct has an exported field of a type a form
can't decode, such as `time.Time`, or no exported fields at all. Form
fields are `string`, `int`, `int64`, `float64`, `bool`,
`map[string]string` and `map[string]bool`: hold richer values as a
string and parse them in `Validate` (`forms.md`). Fields form inputs
don't fill belong outside the input struct.

### TC353
Warning: inline script that won't run, because every response carries a
Content-Security-Policy (`live.md`) that allows only scripts the app
serves: a `<script>` with code in it rather than a `src`, or an
`onclick`-style handler attribute. Bind events with `on:click="name"`
and handle them in Go; for code that must run in the browser, write a
behavior (`use:name`, `components.md`) or serve a file and load it with
`<script src=...>`. `<script type="application/json">` holds data and
is fine.

### TC354
A root layout has no `<.live_meta />`, so the descriptions and link
preview images pages set with `c.Meta` would never reach the document.
Add `<.live_meta />` inside `<head>`, after `<.live_title>`. See
`layouts.md`.

## Styles (ST)

Errors in a component's `<style>` block or in `tokens.toml`. See
`components.md`.

### TC348
Warning: a `:for` over records (structs) has no `:key`. Without one, the
patcher matches items by position, so removing the first item rewrites
every row, and focus, typed input and behaviors stay with the position
rather than the item. Add `:key={item.id}`, or another field unique to
each item; for a list that only grows, a sequence number works. Lists of
strings and numbers aren't flagged, since their values may repeat.

### ST701
`tokens.toml` can't be read: a value isn't quoted, a token is outside a `[group]`, a group or token is declared twice, or a name has characters other than lower-case letters, digits and `-`. The format is groups of `name = "value"` lines, with `{ light = "...", dark = "..." }` for a value that changes in dark mode.

### ST702
A token's value doesn't fit its group: a `color` token that isn't a color (`#rrggbb`, `rgb()`, `hsl()`, `oklch()`), a `space`, `radius` or `text` token that isn't a length, a `duration` that isn't `150ms` or `0.2s`, or a dark value on a group other than `color` and `shadow`.

### ST703
`token(group.name)` names a token that doesn't exist, or the app has no `tokens.toml`. The fix suggests the closest token or lists the group's tokens; add the token to `tokens.toml` if it is missing.

### ST704
A raw value where a token is required: a color (`#fff`, `rgb()`, `red`), a spacing (`margin`, `padding`, `gap`), a radius, a font size or a shadow written as a literal, or a token's custom property written `var(--color-accent)`. Use `token(...)`; the fix lists the closest tokens. Layout values (`display`, `width: 50%`, `1fr`, `0`, `auto`) need no token. For a genuine one-off, use `<style unsafe>`.

### ST705
Warning. A style block is marked `<style unsafe>`, so its raw values aren't checked. Allowed, but reported so it stays visible; replace the raw values with tokens when you can.

### ST706
Warning. A pair of color tokens `X` and `on-X` (text drawn on a background) has a contrast ratio below 4.5:1, the WCAG AA level for text, in light or dark mode, or in a theme ("in theme amiga"). The message gives the measured ratio; adjust either color.

### ST707
A `<style>` block is in the wrong place or malformed: inside an element instead of at the top level of the component, a second block in one component, `:for`/`:if` on it, or an attribute other than `unsafe`.

### ST708
The CSS in a style block can't be parsed, or uses something Loom doesn't support: nested rules, `&`, `@import`, or an at-rule other than `@media`, `@supports`, `@container`, `@layer`, `@keyframes` and `@font-face`.

### ST709
`:global(...)` outside a root layout, or not as a whole compound selector. Styles are scoped to the component that renders the elements; only the root layout (the document) may style elements globally, e.g. `:global(body)`.

### ST710
A token from the wrong group for the property, e.g. `padding: token(color.accent)`. Spacing takes `space` tokens, radii `radius`, font sizes `text`, `font` takes `text` and `font`, colors `color`, shadows `shadow`, and transitions and animations `duration`.

### ST711
A theme in `tokens.toml` is wrong: its name is `light` or `dark` (those are the modes) or isn't lower-case letters, digits and `-`, a group is declared twice, or it overrides a group or token the base doesn't declare. A theme only overrides existing tokens; add the token to the base table first. The fix gives the closest names.

### ST712
A `data-theme` attribute names no theme in `tokens.toml`, or isn't written out. It takes `light`, `dark`, `""` or a theme's name, as a string or an `if` of strings (`data-theme={if t.platform == "amiga", "amiga", ""}`), so every name it can take is checked. To pick from many, write each case in the `if`.

## Runtime (RT)

Runtime errors come from live pages. They are logged with the code, the
location in your code where it is known, and a fix. A page whose Mount,
event handler, Assigns or render fails is restarted with a fresh Mount and the browser
gets a full render; after 5 restarts in a minute the session stops and
the browser reloads. `/_loom/event` answers JSON errors as
`{"code": "RT404", "message": "...", "fix": "..."}`.

### RT401
A page rendered with assigns of the wrong type for its template. A page's `Assigns` method is checked when its route is registered (RT406), so this points at a Loom bug or a page built around that check. Return the assigns struct generated for the template, e.g. `templates.TodoAppAssigns{...}`.

### RT402
Mount, an event handler, `HandleInfo`, `Assigns` or rendering panicked. The error gives the panic value and the innermost location outside Loom. Fix the panic; the page was restarted.

### RT403
A `:for` list rendered two items with the same `:key`. Keys must be unique within a list; use an id, not a title.

### RT404
An event or stream named a session that doesn't exist: it expired after the browser was gone longer than `Config.SessionTimeout`, or the server restarted. A request without the `loom_browser` cookie of the browser that loaded the page gets the same answer, so a session id seen in a log can't act on the page; a custom client must load the page first and keep its cookies, or use `loom send` in dev. The browser remounts the page in place, keeping form input, and resends events still in its outbox to the new session.

### RT405
A POST to `/_loom/event` wasn't `{"sid": "...", "client": "...", "seq": 1, "event": "name", "payload": {...}}` with string payload values (see `wire.md`). loom.js always sends this shape; check any custom client.

### RT406
`App.Live` was given a page that doesn't fit: not a pointer to a struct (pass `&MyPage{}`), or without an `Assigns(c *loom.Ctx) T` method returning its template's assigns type `T`. The message names the type the template needs; add or fix the method. It panics at startup, where the route is registered.

### RT407
More than 64 events are waiting for one page, so the event was refused (HTTP 503); the browser resends it. Make the page's handlers faster, or send fewer events, e.g. `on:change` instead of `on:input`.

### RT408
`loom render` (or `/_loom/dev/render`) named a component that doesn't exist, or a name two packages share. The message lists the known components; use `package.Component` to pick one.

### RT409
The assigns JSON given to `loom render` doesn't fit the component: invalid JSON, an unknown field, or a value of the wrong type. The message names the path, e.g. `todos[1].done: want true or false`. Use template (snake_case) names for every field, including fields of nested structs.

### RT410
A page (or a live layout, in its own code) called `c.Subscribe(topic)` but has no `HandleInfo` method, so it could never receive the topic's messages. Add `func (p *MyPage) HandleInfo(c *loom.Ctx, msg loom.Message)`, which changes the page's state; `Assigns` runs after it. See `live.md`.

### RT411
`app.UseDB` was called after the app's database was already in use: after `app.DB`, `app.JobStore`, `app.Jobs`, `app.Queues` or `app.Cron`, which create the in-memory default. Call `app.UseDB(db)` first. A jobs store must be created on the same DB: `pgjobs.New(db, ...)`.

### RT412
A layout's assigns func has the wrong type. It must be `func(c *loom.Ctx) A`, where `A` is the layout component's assigns struct, e.g. `func(c *loom.Ctx) templates.AppAssigns` for `templates.App`. Reported at startup, by `app.RootLayout`, `loom.Layout` or `loom.RootLayout`. For `loom.LiveLayout`, the layout's state has no `Assigns` method or the wrong one: add `func (l *Shell) Assigns(c *loom.Ctx) templates.ShellAssigns`, leaving `InnerBlock` unset.

### RT413
A component can't be used as that kind of layout: a root layout (one with `<html>`) was passed to `loom.Layout`, a component without `<html>` to `RootLayout`, or the component has no `inner_block slot` for the page. Reported at startup. See `layouts.md`.

### RT414
No live route matches the path given to `loomtest.Mount` (or `App.NewView`). Use one of the paths the error lists, as registered with `Live`.

### RT415
A sub-app that requires auth (its `RequiresAuth` returns true, as
`jobsweb` does) is mounted in a group chain with no `loom.Auth` check. It
panics at startup. Mount it in a group with a check,
`app.Group(loom.Auth(requireAdmin)).Mount("/admin/jobs", ...)`, or pass
`loom.Auth(...)` to `Mount`.

### RT416
A mount prefix, route path or set of assets is invalid, at startup: a
prefix must be a plain path like `/admin/jobs` (leading `/`, no trailing
`/`, no `{wildcards}`), a route path must start with `/`, and only one
group per prefix may embed assets (nested groups use the nearest group's
assets).

### RT417
In dev, in the browser console and `loom errors`: an element with `use:` doesn't match
the behavior's `requires` once its dynamic attributes are rendered. Fix
the attribute's value in the template.

### RT418
In dev, in the browser console and `loom errors`: a behavior called `emit` with a name
not in its `emits (...)`. Add the event to the declaration, or fix the
name in the module.

### RT419
A page reported an error with `c.Fail(err)`, in Mount, an event
handler or HandleInfo: something it handled without panicking, such as a failed
write. The page kept running. The message
is the error's; `where` is the line that called `c.Fail`. An error that
has a code of its own (a `loom.Error`, or a data error such as DB602)
keeps it. Fix the cause, and have the page tell the user what happened.
loomtest's `Mount`, `Event` and `Info` fail the test on it.

### RT420
In dev, `loom errors`: a script on a page failed with no Loom code: an
uncaught error, a rejected promise, or a `console.error` call, in the
page.
`where` is the script's file, line and column when the browser knows
them, and `url` the page. Fix the script, or the template or data it
choked on.

### RT421
In a test (`loomtest.Mount`, `Validate`, `Render`, `Manifest`): a
generated `*_hx.go` or `*_hx.js` file isn't what `loom build` writes from
its template now. `go build` and `go test` compile the generated files as
they are, so an edited `.hx` would otherwise pass every test. Run the
`loom build DIR` the message gives and commit the result. If the
templates have errors, the message lists them instead.

### RT422
At startup, from `App.Live` (or `Group.Live`): the page's template, a
component it calls, or its app layout sends an event (`on:click="delte"`)
that the page's `Events` map has no handler for, so that button would do
nothing. The error gives the template position and the events the page
handles; if a handled name is close, the fix asks whether the template
means it. Fix the name in the template, or add a handler:
`"delete": p.delete` in `Events`, with `func (p *MyPage) delete(c
*loom.Ctx, payload loom.Payload)`. Every page in an app layout handles
the layout's events too, unless the layout is a `loom.LiveLayout`,
which handles them itself; for a layout whose events change state every
page shares (a composer, toasts), the fix says to make it one
(`layouts.md`). With a live layout, the event may be handled by either
the page or the layout.

### RT423
A page got an event its `Events` map has no handler for. RT422 means no
template sends it, so it came from a behavior's `emit`, a script, or a
forged request. The event is ignored and logged; the page keeps running.
In tests `p.Event` with such a name fails the test.

### RT434
At startup, from `App.Live` (or `Group.Live`): a page and its live layout
(`loom.LiveLayout`) both have a handler for the same event. An event goes
to whichever handles it, so one of the two would never run. Remove the
handler from one of them; if the page means something of its own, give
its event another name in its markup and `Events`.

### RT435
`loom.LayoutOf[T](c)` asked for the page's live layout as a `T`, but the
page's app layout isn't a live layout, or is one of another type. It
panics where it was called, so the page restarts (RT402 rules apply) and
`loomtest.Mount` fails. Serve the page in a group with
`loom.LiveLayout(component, &T{})`, or ask for the type the group's
layout has.

### RT436
Too many attempts: a page load, event or handler request is over a
`loom.RateLimit`, so it was refused and nothing ran. A page load or
handler request answers 429 with `Retry-After` and the error page. An
event's ack is 429 with `Retry-After`, and loom.js gives the page's
`<html>` the class `loom-limited` until then, so the page can say so
(`.loom-limited .note { display: block }`). The message names the route,
the event and the limit. This is the limit working: wait, or raise the
limit if real use reaches it. In a test, `loomtest.LimitClock` fakes the
limiter's clock and `p.AssertLimited` checks a refusal.

### RT437
A `loom.RateLimit` can't work: it has no `Key`, its `Max` is under 1, its
`Per` isn't positive, or (on a route) its `On` names an event the page
doesn't handle, so a typo would leave the event unlimited. It panics
where the limit is set. Set `Key` to `loom.ByIP`, `loom.ByUser`,
`loom.ByPayload("field")` or `loom.KeyBy(name, fn)`, and name events
from the page's `Events()`.

### RT438
A page got an event that no element of its current render binds with `on:<event>="name"`, so the event was refused before its handler ran (400, logged, listed by `loom errors`; `loomtest`'s `p.Event` fails the test). A browser only sends events of elements it shows, so the request was made by hand, or for a control the page no longer renders: a button behind `:if={admin}`, a step the person hasn't reached, a menu that isn't open. Hiding a control is enough to keep its handler from running; the handler doesn't need to check again. If the control should work, render it; in a test, open what the person would open first (`p.Click` the menu, then pick). To check that a hidden control is refused, use `p.AssertRefused("event", payload)`.

### RT439
At startup, from `App.Run`: `LOOM_DEV=1` is set but `LOOM_DEV_TOKEN` isn't, so the app was started in dev mode outside `loom dev`, perhaps by a variable left in a production environment. Dev mode shows every session's assigns, sends events to any page and reads the dev mailbox, and on a server behind a reverse proxy on the same machine every request looks local. `loom dev` sets both variables and its proxy adds the token to each request (`X-Loom-Dev-Token`); the dev endpoints refuse requests without it. Run the app with `loom dev`, or unset `LOOM_DEV`. Setting `loom.Config{Dev: loom.DevOn}` in code skips this check, for tests and tools.

### RT440
At startup, from `App.Run`: dev mode is on and the address to listen on isn't the loopback interface (`":4000"` or `"0.0.0.0:4000"` listen on every interface). Dev endpoints answer local requests only, but nothing in dev needs other machines; listen on `"localhost:4000"`, or turn dev mode off. Under `loom dev` the app listens on `127.0.0.1` and the dev proxy on `--addr`.

### RT441
`loom.New` was given an invalid `loom.Config`: a negative duration or limit, a `BaseURL` that isn't `http(s)://host` with no path, an invalid `TrustedProxies` prefix, or a `Dev` that isn't a `loom.DevMode`. It panics at startup naming the field. Leave a field zero for its default.

### RT442
The app's `BaseURL` is `https://...`, but a request arrived over plain http as far as the app can tell: no TLS, and no `X-Forwarded-Proto: https` from a trusted proxy. Logged once, outside dev; the request is served. Cookies are set without `Secure` until it is fixed. Behind a reverse proxy on another machine, add its address to `loom.Config.TrustedProxies` (a proxy on the same machine is trusted already) and have it send `X-Forwarded-Proto`; or serve TLS with `Config.Server`.

### RT443
At startup, from `App.Run`: `LOOM_ADDR` is set but the app isn't in dev mode. Only `loom dev` sets it, together with `LOOM_DEV=1`, and it replaces the address the app gives `Run`, so outside dev it would move the app without saying. Unset it.

### RT444
A page load was refused with 503 and `Retry-After` because `Config.MaxSessions` live pages are open (10,000 by default). Each open page holds a goroutine and its state until its browser has been gone for `Config.SessionTimeout`, so a crawler following links could otherwise exhaust memory. Logged at most once every 10 seconds. Raise `MaxSessions` if the server has the memory, or lower `SessionTimeout`.

### RT445
An event handler (or `HandleInfo`, `HandleParams`, a timer or a watched-table refresh) has run for 10 seconds. Until it returns the page answers nothing else, and its queued events fail (RT407). `loom sessions` shows the page's `busy_seconds`. Usually a query or call with no deadline: pass `c.Context()`, which ends with the page (every statement also stops after `Config.StatementTimeout`), and move slow work into a job (`jobs.md`).

### RT446
`app.ErrorPage` was given a nil component, a whole document (one with `<html>`; the error page renders inside the root layout), or an assigns func that isn't `func(loom.PageError) A` for the component's assigns type `A`. Reported at startup.

### RT447
A broadcast (`app.Broadcast`, a job's `c.Broadcast`) encoded to more than Postgres NOTIFY's 8000 bytes, so it reached only the pages on the node that sent it; `Broadcast` returns the error. Broadcast what changed, such as an id, and have pages load the rest in `HandleInfo`.

### RT448
`c.NotFound()` was called after the page loaded: in an event handler, `HandleInfo`, a later `HandleParams` or a timer. Only Mount (with the `HandleParams` that runs with it) can make the page load answer 404. It is reported like `c.Fail` and the page keeps running. Check for the missing thing in Mount; after that, show the page's own empty state, or `c.Navigate` elsewhere.

### RT449
`c.Meta(...)` changed the page's metadata after the page loaded: in an event handler, `HandleInfo`, a later `HandleParams`, a timer or a re-render's `Assigns`. Metadata is rendered with the document, for link previews and search engines, which only read the first load, so a later change would reach nothing. It is reported like `c.Fail` and the metadata stays as it was. Set it in Mount (or the `HandleParams` that runs with it).

### RT450
`c.Meta(loom.Meta{StructuredData: v})` gave structured data that `encoding/json` can't marshal: a channel, a function, a cycle, or a type whose `MarshalJSON` fails. It is reported like `c.Fail` and the page renders without the JSON-LD. Build it from maps, slices, strings, numbers and structs with json tags.

### RT424
The server didn't run an event and the browser should send it again: the server is shutting down (the answer is 503 with `Retry-After`), or it couldn't read `loom_clients` to tell whether the event already ran. Nothing to fix in the app: loom.js keeps the event in its outbox and resends it, to the restarted server if need be, where it runs once. If it persists, check the database connection.

### RT425
`c.SendAfter(d, "svae", payload)` names an event the page's `Events` map
has no handler for, so the timer would do nothing. It is reported like
`c.Fail` (logged, listed by `loom errors`, and failing the loomtest call
that ran the handler) and no timer is set; the page keeps running. The
error lists the events the page handles and suggests a close one. Fix
the name, or add the handler to `Events`.

### RT426
A page called `c.Patch(url)` with a URL that isn't of its own path
(another page's path, another path value of its route such as
`/team/ENG` from `/team/DES`, a path no live route matches, or a URL
with a host), or kept patching from `HandleParams`. A patch keeps the
page, which Mount loaded for its path, and only changes the query.
The page reports the error (loomtest fails the test) and navigates to the URL
instead. Use `c.Navigate(url)` to go to another page or path value; to
patch, build the URL from the current path, e.g. `c.Path("/team/" + key + "?" + q.Encode())`,
or give a relative one such as `"?view=active"`.

### RT427
A page called `c.Patch(url)`, or `p.Patch(url)` in a test, but has no
`HandleParams` method, so nothing would read the new URL. The page reports
the error and navigates to the URL instead, mounting afresh. Add
`func (p *MyPage) HandleParams(c *loom.Ctx, query url.Values)`, setting the
page's state from the URL, or call `c.Navigate(url)`.

### RT428
At startup, from `App.Live` (or `Group.Live`): the page has a method named
`HandleParams` whose signature isn't `HandleParams(c *loom.Ctx, query
url.Values)`, so the runtime would never call it and the page would ignore
its URL. Fix the signature.

### RT429
A browser sent a POST (or PUT, PATCH, DELETE) from another site, so the app refused it with 403 before any handler ran: cross-site request forgery protection, in front of every route (see `auth.md`). A sign-in grant (`/_loom/signin/...`, where `c.SignIn` sends the browser) followed from a page of another site is refused the same way, so a site can't sign its visitors in to an account of its own. Browsers mark where a request came from with `Sec-Fetch-Site` or `Origin`; requests with neither (curl, webhooks, the CLI) are allowed. If your own page got this, it is served from a different origin than the one it posts to (another subdomain or port): serve the form and its handler from the same origin. `loom errors` lists the refusal without the other site's path or origin.

### RT430
`loom.User[T](c)` was called with no one signed in, or `c.SignIn`, `app.SignIn` or `app.SessionCookie` got an empty user id. Put the route in a group with `loom.SignedIn("/login")`, or on a page anyone may see check `c.UserID() != ""` before calling `loom.User`.

### RT431
`loom.User[T]` was called with a `T` other than the type `loom.Users` loads, or the app has no `loom.Users`, or `loom.Users` was called twice. The message names the registered type: write `loom.User[ThatType](c)`, or register the loader once at startup.

### RT432
The sign-in session store failed, usually the database. With a database, sessions are kept in the `loom_user_sessions` table, one of Loom's migrations. Check the connection; a page load answers 500, and an open page restarts.

### RT433
Loom's own migrations (its tables `loom_clients`, `loom_user_sessions`, `loom_rate_limits` and `loom_jobs`) aren't applied to the app's database, or a mounted sub-app keeps data in the app's database (it implements `loom.HasMigrations`) and its migrations aren't applied, or the app has no database. Run `loom db install <package>` (the error names it: `github.com/moomerman/loom` for Loom's), then `loom migrate`, and commit the copied migrations. `Run` refuses to start, and `loomtest.Mount` fails, until they are applied.

## Jobs (JB)

Jobs errors come from the `jobs` package (see `jobs.md`). Startup errors
(JB502, JB503, JB504, JB510, JB512, JB516) panic in `app.Jobs`, `app.Cron` and
`app.Queues`, or are returned by `app.Run`. A failed attempt is recorded
in the job's errors with its code, logged, and listed by `loom errors`;
`loom jobs inspect ID` shows them all. The dev endpoints answer errors as
`{"code": "JB507", "message": "...", "fix": "..."}`.

### JB501
A job's type isn't registered on this node. At startup: a cron entry names an unregistered type. From `Enqueue`: the app's runner doesn't run that type, so the job would be retried until discarded; the enqueue fails instead, and its transaction with it. When running: a stored job's type is unknown here, perhaps from a newer deploy; the attempt fails and is retried with backoff, so a node that knows the type can run it. From `loom jobs enqueue`: no registered type has that name. Register every job type at startup: `app.Jobs(SendWelcome{})`.

### JB502
`app.Jobs` (or `Enqueue`) got something that isn't a job type: a pointer, a non-struct, nil, or a type registered twice. Job types are structs passed by value, with a value-receiver `Perform` method; register each once.

### JB503
A job's args can't be encoded as JSON: a field is a channel, function or other non-JSON type. Args are the job's exported fields. Use strings, numbers, bools, slices, maps and structs, and tag dependencies such as database handles `json:"-"` so they come from the registered prototype instead.

### JB504
A cron expression is invalid. Use five fields, minute hour day-of-month month day-of-week, in UTC, with numbers, `*`, ranges `a-b`, lists `a,b` and steps `/n`: `"0 3 * * *"` is 03:00 daily. Names (`MON`, `JAN`) and macros (`@daily`) aren't supported.

### JB505
`Perform` panicked. The attempt failed and was recorded with the panic value and stack trace (`loom jobs inspect ID`); the job is retried with backoff like any failure. The process keeps running. Fix the panic at the top non-runtime frame of the stack.

### JB506
`Perform` ran past its timeout (`Options.Timeout`, default one minute). Its `ctx` was cancelled and the attempt failed. Make `Perform` return when `ctx.Done()` closes (pass `ctx` to database and HTTP calls), raise the timeout, or split the work into smaller jobs. A `Perform` that ignores its context keeps running in the background; its outcome is not recorded.

### JB507
No job has that id: it never existed, or it finished and was pruned (after `Runner.Retention`, seven days). List jobs with `loom jobs`.

### JB508
The job's state doesn't allow the operation: retrying a job that is available or executing, or cancelling one that has finished. Also an unknown `--state` for `loom jobs`. The message lists what is allowed.

### JB509
JSON args don't fit the job type: from `loom jobs enqueue`, invalid JSON, an unknown field or a wrong type (field names are the json tag names); when running, stored args that no longer decode into the type after a change to it. Fix the JSON, or make the type accept the stored args, or cancel the old jobs.

### JB510
A registered job type uses a queue that has no workers, so its jobs would never run. Configure it: `app.Queues(jobs.Queue{Name: "mail", Concurrency: 10})`. The `default` queue always exists.

### JB511
A job lost its lease: the node running it stopped renewing the lease (it crashed, hung, or lost its database connection) and the leader rescued it, recording JB511 in its errors. The job runs again after a delay (15s, then 30s), which is why `Perform` must be safe to run twice. Also logged when a node finishes a job after its lease was lost or it was cancelled; that outcome is discarded.

### JB512
Invalid jobs configuration: a queue without a name or with negative concurrency, `app.Queues` after the app started, `app.JobStore` after another jobs method or with a store on a different DB from `app.UseDB`'s, or jobs that kept becoming due in one `Drain` (a job that snoozes for 0 or enqueues itself). The message says which.

### JB513
The job store failed while a dev tool asked about jobs: usually the database is unreachable. The app's log has the underlying error.

### JB514
`jobs.Enqueue` got a transaction whose database has no jobs store bound to it, so the job has nowhere to go. Enqueue in a transaction of the app's DB (`app.DB().Tx(ctx, ...)`) or of the DB your store was created with (`store.DB().Tx`); stores bind themselves to their DB when created (`jobs.NewMemoryStore`, `pgjobs.New`).

### JB515
`RunCron` (cron "run now", in jobsweb) got an index with no cron entry. Use an `index` from `loom cron --json`.

### JB516
A job type has an unexported field without a `json:"-"` tag, from `app.Jobs` or `Enqueue`. JSON leaves unexported fields out of the args, so `Perform` would see the value registered with `app.Jobs`, never the one passed to `Enqueue`: `SendWelcome{userID: id}` would arrive with no user. Export the field if it's an arg; if it's a dependency (a database handle, a client) set on the registered value, tag it `json:"-"` to say so.

### JB517
A job lost its lease three times (`jobs.MaxRescues`), so it was discarded rather than rescued again: a job that runs its node out of memory or hits a fatal error would otherwise take a node down on every attempt. Look at the node's logs from the times in its errors; when the cause is fixed, retry it (`loom jobs retry ID`).

## Data (DB)

Data errors come from `loom.DB` and `loom.Tx` (package `data`; see `data.md`).

### DB601
`loom.OpenDB` couldn't connect: a bad URL, a wrong password, or Postgres not running. URLs look like `postgres://user:password@host:5432/dbname`. The message includes the driver's error.

### DB602
SQL was run on a DB with no database behind it: the in-memory default (`loom.NewLocalDB`, or `app.DB()` without `app.UseDB`). Its transactions still run `AfterCommit` hooks and enqueue jobs, but can't run SQL. Give the app a database with `loom.New(loom.Config{DB: url})`, or open one with `loom.OpenDB(ctx, url)` and pass it to `app.UseDB`.

### DB603
A `loom.Tx` was used after its transaction committed or rolled back: it was kept, or passed to a goroutine, beyond the function given to `db.Tx`. Use the Tx only inside that function; to act after commit, use `tx.AfterCommit`.

### DB604
`loom.Get` found no row. Matches `loom.ErrNotFound`: handle the case with `errors.Is(err, loom.ErrNotFound)`, e.g. by showing a not-found message. The message shows the query and its arguments.

### DB605
`loom.Get` found more than one row; it expects exactly one. Filter on a unique column (usually the primary key), add `LIMIT 1`, or use `loom.Select`.

### DB606
A unique constraint was violated: a row already has this value (the message names the constraint and the key). Matches `loom.ErrUniqueViolation`. Check for the row first, write `INSERT ... ON CONFLICT DO NOTHING` (or `DO UPDATE`), or show the user a "taken" message when `errors.Is(err, loom.ErrUniqueViolation)`.

### DB607
A foreign key was violated: the referenced row doesn't exist, or a deleted row is still referenced. Matches `loom.ErrForeignKeyViolation`. Insert the referenced row first, delete the referencing rows first, or declare the key `ON DELETE CASCADE` in a migration.

### DB608
A `NOT NULL` column got no value or NULL. Matches `loom.ErrNotNullViolation`. Give the column a value in the statement, or a default in a migration.

### DB609
The table doesn't exist. Check the name; if a migration creates it, it hasn't been applied: run `loom migrate` (`loom migrate status` shows what is pending). In tests, `loomtest.DB` builds its database from the nearest `migrations` directory.

### DB610
`loom.Select` or `loom.Get` got a column with no matching field in the struct. The message names the column and the struct. Fields take the column of their `db` tag, or else their name in snake_case (`UserID` takes `user_id`). Add a field (or tag one), or list only the columns you need instead of `select *`.

### DB611
A column's value doesn't fit its field: a type mismatch (text into an `int`), or a NULL into a field that can't hold one. The message names the column, the field, its type and the value's problem. For NULLs, make the field a pointer (`*string`) or a `sql.Null*` type, or use `coalesce` in SQL; for types, change the field or cast in SQL (`id::text`).

### DB612
The query's columns don't fit the type: several columns scanned into a single value (`loom.Select[int64]` needs one column), or the same column name twice (from a join). Select one column, alias duplicates (`b.id AS b_id`), or scan into a struct.

### DB613
A migration file is badly named or numbered. Migrations are `migrations/NNNN_name.sql`: four or more digits, an underscore, then lowercase letters, digits and underscores. Numbers must be unique. Create them with `loom db new <name>`, which numbers them for you. Files not ending in `.sql` are ignored.

### DB614
A migration was edited after it was applied: its checksum no longer matches the one recorded in `loom_migrations`. Migrations are forward-only, so `loom migrate` refuses to run. Restore the file as it was applied (`git checkout` it) and make the change in a new migration (`loom db new <name>`). In development, if you really mean to rewrite history, reset the database.

### DB615
A migration's SQL failed. The message gives the file, line and column Postgres pointed at, with an excerpt. The migration ran in a transaction, so none of it was applied, and later migrations didn't run; fix the SQL and run `loom migrate` again. Also when a migration waited `data.LockTimeout` (10s) for a lock another transaction holds: run it again when that transaction has finished (`pg_stat_activity` shows it), or begin the migration with `SET LOCAL lock_timeout = '1min';`. A `-- loom:no-transaction` migration that fails may have done part of its work: undo it first.

### DB616
A `-- loom:` annotation in a migration is wrong: an unknown annotation (they are `-- loom:track <table>`, `-- loom:no-transaction` and `-- loom:from`, which `loom db install` writes), a malformed one, `no-transaction` and `track` in one migration (track the table in its own), or a tracked table that doesn't exist. The message says which, with the file and line.

### DB618
A statement names a column that doesn't exist (Postgres 42703). The message quotes the statement and points at the name; the fix leads with Postgres's suggestion when there is a close match ("Perhaps you meant to reference the column "todos.title""). Check the name with `loom db schema`; if a migration adds the column, run `loom migrate`.

### DB619
A statement has a syntax error (Postgres 42601). The message quotes the statement and points where Postgres stopped understanding it. Common causes: `?` instead of `$1`, a keyword used as a name without double quotes, a missing comma.

### DB620
A value's type doesn't fit its column (Postgres 42804), such as text into a `bigint` or a `boolean` compared with an integer. Pass a Go value of the column's type, or cast in the SQL (`$1::bigint`).

### DB621
No function or operator takes the given argument types (Postgres 42883), such as `text = integer` or `lower(int)`. Cast an argument (`$1::text`); parameters whose type Postgres can't infer need one.

### DB622
A route's `loom.Watch` names a table that doesn't exist or isn't tracked, or the app has no database to watch. `app.Run` (and `loomtest.Mount`, through `Validate`) checks every watch. Track the table with `-- loom:track <table>` in a new migration (`loom db new track_<table>`) and run `loom migrate`; fix a misspelt name; or give the app a database. `loom db schema` shows which tables are tracked.

### DB623
At startup, from `App.Run`: a LISTEN on one database connection didn't hear a NOTIFY sent from another, as happens behind a connection pooler in transaction mode (pgbouncer's default), where each statement may run on a different server connection. Table watch, the jobs runner's wake-ups and its cron leader lock all need connections that keep their session, and would fail without a word. Connect to Postgres directly, or through a pooler in session mode (`pool_mode = session`).

### DB624
An `AfterCommit` or `AfterRollback` hook panicked. The transaction was already over, so `db.Tx` recovers the panic, reports it as an error event (in the log and `loom errors`, with the stack) and runs the hooks after it; `db.Tx` still returns nil for a commit. Fix the hook: it can't undo the transaction, so it should only announce what happened (broadcast, enqueue nothing slow).

## Forms (FM)

Raised by the `form` package: at startup for the forms in a page's
assigns (`App.Live` and `loomtest.Mount` panic at the route's line),
and otherwise when the form is made or validated. See `forms.md`.

### FM801
A form's input isn't a struct a form can decode: not a struct, a field
of an unsupported type, or no exported fields. `loom check` reports the
same for a template's `form[...]` (TC352). Use the field types listed
there.

### FM802
The input's `Validate` method has a value receiver, or the wrong
signature. Declare it `func (in *Input) Validate(e *form.Errors)`: the
pointer receiver is what lets `e.Add(&in.Amount, ...)` name the form's
own field.

### FM803
`Errors.Add`, `AddKey`, `Has` or `Form.AddError` was given something
that isn't a pointer to a field of the input being validated, such as
the address of a local variable or of a copy of the input; or `Add`
named a map field (use `AddKey`), or `AddKey` a field that isn't one.
Pass `&in.Field`, where `in` is the receiver of `Validate` or the input
`Submit` returned.

## Storage (SG)

Raised by the `storage` package (see `storage.md`). SG002 to SG005 stop
the app at startup: `app.Run` checks the bucket, and `loomtest.Mount`
does through `Validate`.

### SG001
The app stored or read a file, but `loom.Config.Storage` is nil and it
isn't running in dev. Give it a bucket: `storage.S3(storage.S3Config{...})`
for an S3-compatible service, or `storage.Dir(path)` for a directory
on one server. Under `loom dev` an app with none keeps files in
`.loom/storage`, and in other dev mode in memory, so this never stops
it there.

### SG002
A bucket's configuration is incomplete or wrong: an `S3Config` with no
`Bucket`, `AccessKeyID` or `SecretAccessKey`, an `Endpoint` that isn't
a scheme and host (the bucket goes in `Bucket`, not the path), a
`Bucket` with a `/`, a bucket in another region than `Region` (AWS
answers 301), or `storage.Dir("")`. Fill in the field the message
names, from the environment.

### SG003
The storage service couldn't be reached, or answered with an error
that isn't about the key, credentials or bucket; for `storage.Dir`, the
disk failed. Check `S3Config.Endpoint` and the service's status, or
that the directory can be read and written. A job that gets SG003
fails its attempt and retries.

### SG004
The service refused the credentials (`InvalidAccessKeyId`,
`SignatureDoesNotMatch`, `AccessDenied`), or the region in the
signature is wrong; or `storage.Dir` can't create or write its
directory. Check `S3Config.AccessKeyID` and `SecretAccessKey` and that
the key may read, write, delete and list objects in the bucket; R2's
region is `"auto"`.

### SG005
The bucket doesn't exist. Loom never creates buckets: create it in the
service's console (or `mc mb` for MinIO), or fix `S3Config.Bucket`.

### SG006
A key isn't a valid object key: empty, over 1024 bytes, not UTF-8, or
with a backslash, a control character, an empty segment (a leading,
trailing or doubled `/`) or a `.` or `..` segment. Use a path of plain
segments, such as `avatars/42.png`; `storage.ValidKey` checks one.
`storage.Serve` answers it 404.

### SG007
There is no object at the key: `storage.ErrNotFound`, from `Get` and
`Stat`. Test for it with `errors.Is(err, storage.ErrNotFound)` and treat
it as missing; `storage.Serve` answers it 404.

### SG008
`Put`'s body was shorter or longer than its `size`. Nothing is stored.
Pass the body's exact length: `bytes.NewReader(data),
int64(len(data))`, or a file's size from `Stat`.

## Auth (AU)

Raised by the `auth` sub-app (see `auth.md`). AU901 to AU903 and AU906
stop the app at startup: `app.Run`, and `loomtest.Mount` through `Validate`.

### AU901
Auth sends sign-in and password-reset links, but the app has no mailer.
Give it one, `app.UseMailer(m)`, where `m` is a `loom.Mailer` around
your mail service; or turn email off with `auth.Config{NoEmail: true}`
(passwords only, no resets). Under `loom dev` the app keeps mail for
`loom mail` instead, so this never stops it there. In tests,
`loomtest.Mail(t, app)` is a mailer.

### AU902
Auth mails links or signs in with a provider, but the app's
`loom.Config.BaseURL` is empty, outside dev. Set it to the app's public
address, such as `loom.Config{BaseURL: os.Getenv("BASE_URL")}` (a
malformed one is RT441). Links and provider
callbacks are never built from the request's `Host` header outside
dev, since the sender of a request chooses it.

### AU903
An `auth.Config` field is invalid: `AfterSignOut` isn't a path in the
app (it must start with `/`, and not `//`), `LinkFor` is negative, or
a provider in `Providers` wasn't made by `auth.Google` or `auth.GitHub`,
or is there twice, or `TwoFactor` isn't one of `auth.TwoFactorOptional`,
`auth.TwoFactorRequired` and `auth.TwoFactorOff`.

### AU904
A stored password hash isn't one `auth.HashPassword` made, so it can't
be checked. Store its result unchanged in `auth_users.password_hash`.
To recover an account, set the column to NULL: the person signs in by
link, or resets their password.

### AU905
`auth.HashPassword` was given a password shorter than 8 or longer than
1024 bytes. Check the length where the person types it (the sign-up,
reset and account forms do) and ask for another.

### AU906
A provider in `auth.Config.Providers` has an empty client ID or secret,
usually an unset environment variable. Register the app with the
provider (`auth.md` says where) and pass both; or add the provider only
when they are set.

### AU907
Signing in with a provider failed after it sent the browser back: it
refused the code, or its answer couldn't be read. The person sees
"didn't work, try again" on the sign-in page; the error, in the log and
`loom errors`, has the provider's answer. Most often the client secret
is wrong, or the callback URL it names isn't one of the client's
authorized redirect URIs.

### AU908
A stored two-factor secret can't be decrypted: it was sealed with a
different `loom.Config.Secret`, or the app now has none. The person
can't finish signing in with a code. Restore the secret the app had
(every node must have the same one); if it is lost, the person signs
in with a recovery code, or an operator turns their two-factor off
(`DELETE FROM auth_totp WHERE user_id = ...`) so they set it up again.

### AU909
Two-factor sign-in is on, but `loom.Config.Secret` is empty outside
dev. Authenticator secrets can't be hashed like passwords, so auth
encrypts them with the app's secret, and a leaked database or backup
doesn't give away anyone's second factor. Make one with `openssl rand
-base64 32` and pass it from the environment, the same on every node:
`loom.New(loom.Config{Secret: os.Getenv("LOOM_SECRET")})`. Secrets
stored before the app had one are encrypted at the next start.
