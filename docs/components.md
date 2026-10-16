# Components and styles

How Loom components look, vary and behave: design tokens, scoped
`<style>` blocks, the per-route stylesheet, typed variants, client
behaviors, and the starter kit. Previews come in a later step.

## Design tokens

Every color, spacing, radius, font size, font and shadow an app uses is a
token, declared once in `tokens.toml` next to the templates (or in the
nearest parent directory, up to the one with `go.mod`):

```toml
[color]
accent    = { light = "#b83f45", dark = "#f08a8f" }
on-accent = { light = "#ffffff", dark = "#1f1f1f" }
surface   = "#ffffff"

[space]
2 = "0.5rem"
4 = "1rem"

[radius]
sm = "3px"

[text]
lg = "1.125rem"

[font]
body = "system-ui, sans-serif"

[shadow]
card = "0 2px 8px rgb(0 0 0 / 0.15)"

[duration]
fast = "150ms"
```

- Groups are `color`, `space`, `radius`, `text` (font sizes), `font`
  (families), `shadow` and `duration`; other groups are allowed and
  unchecked. Names are lower-case letters, digits and `-`.
- Values are checked against their group: a `color` must be a color, a
  `space` a length (ST702).
- A `color` or `shadow` can take `{ light = "...", dark = "..." }`.
- For each pair `X` and `on-X` (text on a background), `loom check` warns
  when the contrast is below WCAG AA's 4.5:1, in either mode, with the
  measured ratio (ST706).

Tokens compile to CSS custom properties, `--group-name`, on `:root`. Dark
values apply under `prefers-color-scheme: dark`, and
`<html data-theme="dark">` or `data-theme="light"` overrides the system
setting either way.

### Themes

A theme is a named set of overrides, for part of a page that should
look different: a section per brand, or per machine in a music
catalog. Each `[theme.NAME.GROUP]` table overrides tokens the base
declares, with the same value rules:

```toml
[theme.amiga.color]
page    = { light = "#0055aa", dark = "#002a55" }
on-page = "#ffffff"

[theme.amiga.font]
pixel = "'Topaz', monospace"
```

```heex
<article data-theme={if tune.platform == "amiga", "amiga", ""}>...</article>
```

- An element with `data-theme="NAME"` takes the theme's values for
  itself and everything inside; tokens the theme leaves out keep the
  base values. Light and dark still apply: a two-valued token follows
  the mode.
- `light` and `dark` are the modes, not themes, and can't be theme
  names; names are lower-case letters, digits and `-` (ST711).
- A theme overriding a token or group the base doesn't declare is
  ST711, with the closest names.
- `data-theme` must be written out: a string, or an `if` of strings, so
  `loom check` knows each name. An unknown name, or any other
  expression, is ST712. `""` sets no theme.
- Contrast warnings (ST706) run for each theme too.

In a reusable package (`//loom:package`, see `subapps.md`) tokens are
defaults, wrapped in `:where()`: when a mounted package's page also
carries the host app's tokens, the host's values win, so a sub-app such
as `auth` takes the app's colours and fonts for any token both define.

## Scoped styles

A component may have one `<style>` block at its top level:

```heex
component TodoApp(todos []Todo)

<style>
  .todoapp { margin: token(space.12) auto; padding: token(space.6); box-shadow: token(shadow.card); }
  h1 { font-size: token(text.title); color: token(color.accent); }
  @media (min-width: 40rem) { .todoapp { padding: token(space.12); } }
</style>
<section class="todoapp"><h1>todos</h1></section>
```

- `token(group.name)` is resolved at compile time to `var(--group-name)`.
  An unknown token is ST703 with the closest names; a token from the wrong
  group for the property (e.g. `padding: token(color.accent)`) is ST710.
- **Raw values are errors** (ST704): colors (`#fff`, `rgb()`, `red`),
  spacing (`margin`, `padding`, `gap`), radii, font sizes and shadows.
  The fix lists the closest tokens. Layout values need no token: `0`,
  `auto`, `100%`, `1fr`, `display`, `width`, `1px` borders, `inherit`,
  `transparent`, `none`.
- **Styles are scoped.** Every element the component renders gets its
  scope class (`l-` and a hash of its package and name), and every
  selector requires it, so `h1 { ... }` styles this component's `<h1>`
  only: not its callers', not its callees', not the slot content it is
  passed. Style a child component in the child.
- `@keyframes` names are scoped too. `@media`, `@supports`, `@container`
  and `@layer` nest; `@font-face` is allowed. Nested rules and `&` are not
  supported (ST708).
- `:global(selector)` styles elements anywhere, and is allowed only in
  the root layout (ST709), e.g. `:global(body) { ... }`. `<html>`,
  `<head>` and what's in `<head>` never get the scope class.
- `<style unsafe>` skips the raw-value checks for a genuine one-off.
  `loom check` reports each one as warning ST705, so it stays visible.

## The stylesheet

Root layouts must have `<.loom_styles />` in `<head>` (TC337). It links
the page's stylesheet: the tokens, then the CSS of every component the
route can render (its root layout, app layout, page and everything they
call), each once. Components a route never renders aren't in it.

The stylesheet is computed once per route at startup and served at
`/_loom/css/<hash>.css` with `Cache-Control: immutable`; a changed style
gets a new URL. A route whose components have no styles links nothing.

## Variants

A component's options are typed parameters. An enum lists its values; a
default makes a parameter optional; an event parameter lets the caller
pick the server event:

```heex
component Button(label string, variant: :primary | :ghost = :primary, size: :sm | :md = :md, busy bool = false, on:click event)

<style>
  .button { border-radius: token(radius.md); }
  .primary { background: token(color.accent); color: token(color.on-accent); }
  .ghost { background: transparent; }
  .sm { padding: token(space.1) token(space.2); }
  .md { padding: token(space.2) token(space.4); }
</style>
<button class={"button" variant size if busy, "busy"} disabled={busy} on:click={on_click}>{label}</button>
```

```heex
<.button label="Delete" variant="ghost" size="sm" on:click="delete" val-id={t.id} />
```

- An enum value is used as a class name, so each variant is a plain rule
  in the component's style. Class names are listed, never built with `+`
  (TC341), so every class in the HTML appears whole in the `<style>`.
- `variant="primry"` is an error naming `"primary"` (TC338). So is a
  default or comparison that isn't one of the values.
- Defaults live only in the signature. Template calls fill them in; in Go,
  the assigns struct has a typed constant per enum value
  (`ButtonVariantGhost`), and an empty enum renders as its default.
- Slots and events are optional; everything else without a default is
  required (TC317).

## Behaviors

A behavior is the one way to run code in the browser: a typed
declaration in a template plus an ES module beside it (syntax in
`syntax.md`).

```heex
behavior tabs() requires [role=tablist]
```

```js
// tabs.js
export function mount(el, props, emit) {
  const keydown = (e) => { /* move focus between [role=tab] */ };
  el.addEventListener("keydown", keydown);
  return {
    update(props) {},  // after every patch, with the current props
    destroy() {},      // when the element leaves the page
  };
}
```

The compiler checks every `use:`: the behavior exists, props have the
right types, the element has the required attributes (TC343 to TC346).
A requirement whose attribute is dynamic is checked in the browser in
dev (RT417), and so is `emit` with an undeclared name (RT418).

At runtime:

- Each route serves one module, `/_loom/js/<hash>.js`, from
  `<.loom_scripts />`. It imports `behaviors.js` and the modules of the
  behaviors the route's components can attach, all embedded in the
  binary and served as written, content-hashed. No bundler, no npm.
  Routes without behaviors load nothing more than `loom.js`.
- An element with `use:` renders `data-loom-use` (the behavior),
  `data-loom-props` (JSON) and, for dynamic requirements,
  `data-loom-requires`. After the page loads and after each patch, new
  elements are mounted, kept ones get `update(props)`, and removed ones
  `destroy()`.
- The patcher keeps elements, including keyed ones that move, so a
  behavior keeps its state across updates and reordering. It does reset
  attributes to the server's HTML, so set attributes again in `update`.
- A behavior is one module. Code it loads by relative URL (its own
  imports, a worker, an audio worklet, WebAssembly) goes in a directory
  of embedded assets, passed as a prop: `use:player={base:
  asset("player/")}`, then `import(props.base + "engine.js")`. WebAssembly
  also needs `'wasm-unsafe-eval'` in `script-src` (`Config.CSP`). A
  worker's policy is the one its script is served with; to loosen it for
  one worker only, set the header in middleware for that path.
- `emit` goes through the same path as `on:click`. Nothing in a behavior
  needs the server: with the connection down, behaviors keep working and
  events are dropped.

Test pages with `p.Emit(selector, "select", map[string]any{"value": "x"})`
in `loomtest`, which checks the element has the behavior, the behavior
declares the event and the element binds it, then sends it like the
browser would. Test the module itself in a browser (see `e2e/`).

`loom dev` rebuilds when a module changes.

## Previews

Every component gets previews: one per combination of its options,
rendered in light and dark, then in each theme of its tokens. Enum and bool parameters vary on their own;
other parameters need sample values with `@preview`, after the type and
any default:

```heex
component Button(label string @preview("Save", "Delete forever"), variant: :primary | :danger = :primary, busy bool = false, on:click event)
component Card(title string @preview("Profile"), inner_block slot @preview("<p>Some content</p>"))
```

- A variant takes each enum value (default first), each bool (default,
  then the other) and each `@preview` value (after the default, if
  any). Events are left unset; slots without samples are empty.
- Up to 24 combinations are all shown. Beyond that, the first
  combination (the "default" variant) plus each other value on its own.
- A variant is named by what differs from the default: `variant=danger
  busy`, `label="Delete forever"`.
- A component with a required string or number without `@preview`, or a
  list or struct parameter, has no previews and says why; preview a
  component that builds the value instead. `@preview` on an enum, bool,
  list or struct is TC347.
- Previews are static: no `loom.js`, no behaviors, no events.

In dev, `/_loom/preview` lists every component; each one's page shows
its variants side by side in each mode and theme, and
`/_loom/preview/{component}?variant=...&theme=dark` (or a theme's name)
is one alone.
`loom preview` lists the variants and `loom preview screenshot` saves
PNGs from headless Chrome (`$LOOM_CHROME`, or Chrome or Chromium on the
`PATH`), so an agent can look at what it built (see `cli.md`).

### Accessibility

`loomtest` checks rendered HTML against accessibility rules, without a
browser:

```go
loomtest.AssertPreviewsAccessible(t, components.Button) // every variant
loomtest.AssertAccessible(t, html)                      // any HTML
p.AssertAccessible()                                    // a mounted page
```

| Rule | Checks |
|---|---|
| `image-alt`, `input-image-alt` | images have `alt` (`alt=""` for decoration) |
| `button-name`, `link-name` | buttons and links have a name: text, `aria-label`, `aria-labelledby` or `title` |
| `label` | inputs, selects and textareas have a label (`<label for>`, wrapping, or `aria-*`) |
| `dialog-name` | dialogs have `aria-labelledby` or `aria-label` |
| `duplicate-id` | no two elements share an `id` |
| `aria-reference` | `aria-labelledby`, `aria-describedby`, `aria-controls` and `label for` name existing IDs |
| `aria-roles`, `aria-valid-attr`, `aria-valid-attr-value` | roles, `aria-*` names and values are valid |
| `aria-required-parent` | `option`, `tab`, `menuitem` and the like are inside their container |
| `html-lang` | documents set `<html lang>` |

Each problem names the rule, the element and the fix. A component adds
its own rules, scoped by a selector:

```go
rule := loomtest.A11yRule{ID: "menu-items", Selector: "[role=menu]",
	Fix: "give the menu at least one [role=menuitem]",
	Check: func(e *loomtest.Element) string {
		if len(loomtest.Select(e, "[role=menuitem]")) == 0 {
			return "menu has no items"
		}
		return ""
	}}
loomtest.AssertPreviewsAccessible(t, components.Menu, rule)
```

## The starter kit

Loom ships 16 components to copy into an app: alert, badge, button, card,
checkbox, dialog, dropdown, empty_state, form_field, input, link, select,
spinner, table, tabs (Tabs and Tab) and textarea. They are styled only
with tokens, accessible by default (labels, roles, `aria-*`), and written
as the reference for idiomatic components. Dialog (focus trap, Escape),
dropdown (a listbox driven by the keyboard, with DropdownOption)
and tabs (arrow keys) use behaviors, so each comes with its `.js` module. The
button and form field show a pending event (`loom-pending`, `live.md`).

```
loom components                 # what's there, with every parameter
loom components add button card # copy into components/ and build it
```

The copies are the app's own code: change them freely. Use them from any
template with an import:

```heex
import "example.com/app/components"

component Settings()

<.card title="Profile">
  <.input name="email" label="Email" type="email" />
  <.button label="Save" type="submit" />
</.card>
```

In a form, `input`, `select`, `textarea` and `checkbox` take a form's
field instead, `<.input field={profile.email} label="Email" />`, for its
name, value and error (`forms.md`).

Nothing records where a copy came from, and there is no upgrade
command. To pick up a later kit change, diff your copy against the
kit's file in `kit/` and apply what you want. `add` never overwrites: a
file that already exists is an error.

The kit's components use these tokens: colors `accent`, `surface`,
`subtle`, `danger`, `success`, `warning` (each with its `on-` pair),
`muted`, `line`, `focus` and `backdrop`; `space.1`–`space.8`; radii
`sm`, `md`, `lg`, `full`; text `sm`–`xl`; fonts `body`, `mono`; shadows
`sm`, `lg`; `duration.fast`. If the app has no `tokens.toml`, `add`
copies the kit's next to `components/`; otherwise `loom check` names any
token the app's file is missing (ST703). The kit's sources live in `kit/`
in this repo, and an app never imports that package. Every kit component
has previews, and the kit's tests check them for accessibility.

## Testing

`loomtest.Render` output includes the scope classes, so assert on your own
classes with selectors (`li.done`) rather than whole `class` attributes.
`go run ./cmd/loom check --json` lists warnings with `"severity": "warning"`.
