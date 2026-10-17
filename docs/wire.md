# Wire format

A live page's HTML is described by a render tree: static strings, sent
once, with dynamic values between them. The server sends the whole tree on
the first render (and after a reconnect), then only sparse diffs. This
document is the contract between the server (`diff` package) and
`client/loom.js`; `diff.Apply` is a reference implementation of the
client's side, and property tests check that applying any diff to the old
tree yields the new one.

All payloads are JSON. Strings are HTML, already escaped by the server.

## Full tree

A block (a template, an `:if` block, a component call, a slot):

```json
{ "s": ["<p>Hello ", "!</p>"], "d": ["world"] }
```

- `s`: the static parts, in order. There is always exactly one more part
  than there are dynamics.
- `d`: the dynamic values, in order, interleaved with `s`. Omitted when
  there are none.

Rendering is `s[0] + d[0] + s[1] + d[1] + ... + s[n]`.

A dynamic value is one of:

| Value | Meaning |
|---|---|
| `"text"` | HTML text. A dynamic attribute is a whole attribute, e.g. `" class=\"done\""`, or `""` when absent. An `:if` that is false is `""`. |
| `{"s": [...], "d": [...]}` | A nested block. |
| `{"s": [...], "k": [[key, [dynamics]], ...]}` | A list from `:for`. Every item shares the statics `s` and has its own dynamics. Keys are strings and unique within the list. |

Example: a todo list.

```json
{
  "s": ["<ul>\n  ", "\n</ul>"],
  "d": [
    { "s": ["<li>", "</li>"], "k": [["a", ["Buy milk"]], ["b", ["Walk dog"]]] }
  ]
}
```

## Diffs

A diff of a block lists only the dynamics that changed, by index:

```json
{ "d": { "0": "new text", "3": { ... } } }
```

No diff is sent when nothing changed. Each change is one of:

| Change | Meaning |
|---|---|
| `"text"` | Replace the slot with this text. |
| `{"s": ...}` | Replace the slot with this full block or list (it has statics). Sent when a slot changes kind, e.g. an `:if` becomes true. |
| `{"d": {...}}` | Patch the nested block in the slot. |
| `{"k": [[key, {index: change}], ...]}` | Patch list items in place. Sent when the list's keys are unchanged and in the same order; only changed items appear. |
| `{"o": [entry, ...]}` | The list's complete new order. Sent when items were inserted, deleted or moved. |

An `o` entry is one of:

- `[key]`: an existing item, unchanged.
- `[key, {index: change}]`: an existing item, patched.
- `[key, [dynamics]]`: a new item, with full dynamics. Its statics are the
  list's.

Items whose keys don't appear are deleted. The client keeps the DOM nodes
of existing items, moving them as needed, so they keep their identity and
focus.

The client tells changes apart by shape: a string; an object with `s`; an
object with `d`, `k` or `o`. Item patches (`{index: change}`) are only
found inside `k` and `o` entries.

Example: toggling the first todo of the list above sends only that item's
changed slots:

```json
{ "d": { "0": { "k": [["a", { "0": " class=\"done\"", "1": " checked" }]] } } }
```

Statics are sent when a block or list first appears in a slot. A diff never
resends the statics of a block that stays in place.

## Endpoints

- `GET /path`: the root layout's document. Its `{render_slot(inner_block)}`
  is the live tree's HTML inside `<div id="loom-root" data-sid="...">`,
  then the encoded tree in
  `<script id="loom-tree" type="application/json">{"v": 1, "tree": ..., "root": "...", "p": 1}</script>`
  for the client to take over from. `root` fingerprints the root
  layout's markup and the scripts' URLs, and `p` is the wire protocol's
  version (this document's: 1); a remount whose new tree differs in
  either reloads the page, so a tab never runs an old `loom.js` against
  a server that speaks differently. `<.loom_scripts />` loads `loom.js`
  and `navigate.js` from `/_loom/js/<hash>.js`, URLs that change with
  their content, cached for good. The live tree is the app layout with
  the page inside.
  The first page load sets the `loom_browser` cookie (HttpOnly,
  until the browser closes); the session keeps its hash, and the
  stream, events and navigations below are refused as an unknown
  session (RT404) without the same cookie, so a `sid` alone acts on
  nothing.
- `GET /_loom/stream?sid=...&v=...`: Server-Sent Events. `v` is the
  version the browser has.
- `POST /_loom/event`: `{"sid": "...", "client": "tab id", "seq": 4,
  "event": "name", "payload": {"k": "v"}}`. The server runs the handler,
  then acks `200 {"v": 7}`, the page's version after it (its diff
  arrives on the stream). It runs each `(client, seq)` once: a resend of
  one already handled is acked `{"v": 7, "handled": true}` without
  running again, even on another node or after a restart when the app
  has a database (`loom_clients`). The browser keeps each event in an
  outbox until it is acked and sends them in order, one at a time,
  resending after a network error or a 5xx with backoff (1s doubling to
  10s). Errors are JSON `{"code", "message", "fix"}`: 400 RT405, RT423
  or RT438 and 403 (the event is dropped), 429 RT436 with `Retry-After`
  (a rate limit: dropped, and the page marked `loom-limited` until
  then), 404 RT404 (the session is gone: the browser remounts and
  resends), 503 RT407 or RT424 (resent). A resend to the page's new
  session, after a remount or a reload, has `"moved": true`: if the new
  render doesn't bind the event, its RT438 is recorded as
  `event.refused`, not as an error.

Every page render that changes the tree or the title increments the
session's version. Stream events:

| Event | Data | Meaning |
|---|---|---|
| `diff` | `{"v": 3, "diff": {...}, "title": "..."}` | apply to version 2 to get version 3 |
| `full` | `{"v": 3, "tree": {...}, "title": "..."}` | replace the tree: sent first when the browser's `v` is stale, and after the page restarts |
| `reload` | `{}` | the session is gone; remount the page (`layouts.md`) |
| `navigate` | `{"to": "/issues/7"}` | the page called `c.Navigate`: follow it as a link |
| `patch` | `{"to": "/team/ENG?view=active", "replace": false}` | the page called `c.Patch`: push the URL to the history (replace the current entry if `replace`), after the `diff` before it |

A live navigation (`layouts.md`) is a `GET` of the page with the header
`X-Loom-Navigate: <sid>`, the session the browser leaves (`-` for none).
The server answers `204` with `X-Loom-Navigate: reload` when it can't
swap (no such page, another root layout, an unknown session), without
running the path's handler, and otherwise serves the page as usual with
`X-Loom-Navigate: swap`, ending the old session. The browser connects a
stream for the new page's `sid`. A remount navigates to the current URL
with `X-Loom-Navigate: -` (no session to leave): the server swaps any
page, and the browser reloads instead if the new page's `root` differs
from its own.

A navigation to another URL of the session's own route, on a page with
`HandleParams`, patches instead: the session runs `HandleParams` with the
URL and streams the `diff`, and the server answers `200` with
`X-Loom-Navigate: patch` and `{"v": 4, "url": "/team/ENG?view=active"}`,
the URL `HandleParams` settled on. The browser keeps its page and stream
and pushes `url`. Until the answer comes the browser ignores a `reload`
on its stream, which a swap causes by ending the old session.

`title` is the page's title (set with `c.Title`; `""` for none). A `diff`
has it only when the title changed, and has no `diff` when only the title
changed; a `full` always has it. The client sets `document.title` to the
`<title>` element's `data-prefix` + (title, or `data-default` when empty)
+ `data-suffix`, as rendered by `<.live_title>` (see `layouts.md`).

A client that receives a diff whose `v` isn't one more than its own has
missed an update: it reconnects with its version and gets a `full`. The
server sends `: ping` comments every 25 seconds to keep connections open.
After an error the client reconnects, and resends unacknowledged
events, after a wait that starts at a second and doubles to 30 seconds,
at random within half of it either way, so the tabs of a restarted
server don't all return at once. (It reconnects itself rather than by
EventSource's own retry, so an SSE `retry:` field has no effect.)
