# Example: issues, a Linear-style tracker

`examples/issues/` is the larger example app: an issue tracker in the
style of Linear, built to look and feel like a real product and to use
everything Loom has. Read `example-jobsweb.md` first for the basic
idioms (one file per page, plain rows, live without polling); this page
covers what issues adds.

```
examples/issues/
  issues.go           App, Setup, routes, sign-in (the auth sub-app) and joinWorkspace
  store.go            every query and write: transactions, history, enqueues
  rows.go             the rows templates render, and formatting helpers
  shell.go            chrome: the live layout's state (sidebar, composer, toast)
  layouts.hx          Root, Shell (sidebar and overlays), ToastBar, Shortcuts
  components.hx       StatusIcon, PriorityIcon, Avatar, Menu, IssueList,
                      NewIssueDialog, Topbar, ...
  my_issues.go/.hx    /              issues assigned to or created by you
  team.go/.hx         /team/{key}    a team's issues: views, filters, search
  issue.go/.hx        /issue/{id}    one issue: edit in place, history, comments
  inbox.go/.hx        /inbox         notifications from the Notify job
  jobs.go             Notify, the background job behind the inbox
  mail.go             LogMailer, a loom.Mailer that logs instead of sending
  hotkeys.js          the keyboard shortcuts behavior
  toast.js            dismisses the toast after a few seconds
  ui/                 starter kit copies (button, dialog, dropdown, ...)
  migrations/         the schema (tracked tables) and example data
  tokens.toml         a neutral palette, status, priority and label colors
```

Run it with `loom migrate --dir examples/issues/migrations` and `go run
./examples/issues/cmd/issues`, then sign in as `ada@example.com` (or
grace, alan, katherine, margaret) with the password `loom-issues`. Ada
is the admin and sees the jobs dashboard at `/admin/jobs`.

## Live updates across users

Every table is tracked (`-- loom:track`), but the pages listen on PubSub
topics rather than `loom.Watch`, because they want finer grain than a
table: a write publishes on its team's topic after commit (`store.go`),
the layout subscribes to the user's teams and inbox and re-reads its
counts (`shell.go`), the pages re-read what they show in `Assigns`, and
the issue page subscribes to its team and ignores changes to other
issues. A page that
simply shows a table, such as `examples/todo`'s list, watches it instead
(`live.md`).

## Sign-in: the auth sub-app

Sign-in is `auth`, mounted at `/auth` inside the site's root layout, so
its pages wear the tracker's tokens (`auth.md`). Its tables came in
with `loom db install github.com/moomerman/loom/auth` (migration 0003);
0004 gives the example's people accounts with the ids of their `users`
rows, so the id auth signs in is the id the tracker uses. Someone new
who signs up gets a `users` row and joins every team in the sign-up's
own transaction (`joinWorkspace`, auth's `OnSignUp`). With
`GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` (or GitHub's) set, the
sign-in page also has "Continue with Google"; migration 0005 is auth's
`auth_identities`, and 0006 its two-factor tables: anyone can turn on
two-factor sign-in from their account page.

Signed-in routes are in a group with `loom.SignedIn("/auth/sign-in")`,
and `loom.Users(app, store.User)` makes `loom.User[User](c)` the
tracker's own user type in every page and the layout. The dashboard's
group adds `loom.Auth(requireAdmin)`, which checks `c.UserID()`. The
sign-out button in the sidebar is a plain form posting to
`/auth/sign-out`. Mail (sign-in and reset links) goes to `LogMailer`,
or under `loom dev` to `loom mail`.

## The layout's state

The app layout, `Shell`, holds the sidebar and the overlays every page
shares: the new-issue composer, the toast and the shortcuts dialog. It
is a live layout (`layouts.md`): `chrome` in `shell.go` has its own
`Mount`, `Assigns`, `Events` and `HandleInfo`, so the composer's events
go to it and the pages know nothing of them:

```go
signedIn := site.Group(loom.SignedIn("/auth/sign-in"), loom.LiveLayout(Shell, &chrome{app: a}))
```

A page reads the chrome with `shell(c)`, a one-line wrapper of
`loom.LayoutOf[*chrome](c)`, to take the signed-in user and mark its
sidebar item:

```go
func (p *InboxPage) Mount(c *loom.Ctx) {
	ch := shell(c)
	ch.section, p.user = "inbox", ch.user
}
```

A page's markup may send the layout's events: the team page's +
buttons send `compose` with `val-team` and `val-status`, and the
composer opens with them chosen.

## Views and filters in the URL

A team's view, filters and search live in its URL,
`/team/ENG?view=active&priority=1&q=notify`, so a filtered list can be
shared and reloaded, and the back button undoes a filter. `HandleParams`
reads them after Mount and on every URL change; anything it doesn't
understand means the default. The view tabs are plain links to the same
route, with hrefs built in `Assigns` to keep the filters, so following
one patches the page without a page load. A filter's `pick` and the
search build the new URL from the page's state and call `c.Patch`; the
search uses `loom.Replace()` so each keystroke doesn't add a history
entry:

```go
func (p *TeamPage) pick(c *loom.Ctx, payload loom.Payload) {
	p.menu = "" // page state the URL doesn't hold
	switch menu := payload["menu"]; menu {
	case "priority", "assignee", "label":
		p.patch(c, menu, payload["value"]) // c.Patch with the value set in p.query()
	}
}
```

The session stays, so an open composer survives a filter change. My
issues does the same for its two tabs (`/?view=created`). See
`live.md`, "State in the URL".

## Menus

`Menu` (in `components.hx`) is the one control for picking a status,
priority, assignee, label or team: a trigger button and a listbox driven
by the kit's `dropdown` behavior, with an icon per option. Which menu
is open is page state. Every menu on a page sends the same three events
and says which it is with `val-menu`, which travels with each:

```heex
<.menu label="Status" open={menu == "status"} value={issue.status.value} options={issue.status_options}
  on:toggle="menu" on:select="pick" on:close="menu_close" val-menu="status">
  <:trigger><.option_icon option={issue.status} />{issue.status.label}</:trigger>
</.menu>
```

Data picks colors by name, never by value: a label's `color` is `"red"`,
and `Dot` has a rule per color, `.red { background: token(color.red); }`.

## Writes

Each change is one `db.Tx` in `store.go` that updates the row, records
an `issue_events` entry for the history, enqueues `Notify` when someone
else is assigned or commented on, and announces the change with
`AfterCommit`. Pages subscribe to their teams' topics and re-read; the
issue page skips messages about other issues.

`Notify` is idempotent, as every job must be: the notification's
`job_id` column is unique, so a second run inserts nothing.

## Keyboard shortcuts

`hotkeys.js` knows no actions. Elements declare their own with
`data-hotkey`, and a key press clicks that element (or focuses it, for an
input), so a shortcut does exactly what a click does, through the
normal event path:

```heex
<button class="new" data-hotkey="c" on:click="compose">New issue</button>
<a href="/inbox" data-hotkey="g i">Inbox</a>
<input type="search" data-hotkey="/" on:input="search" />
```

J and K move focus through `[data-row]` links, and Enter follows the
focused one. Ctrl or ⌘ Enter submits the form being typed in.

## Testing

`issues_test.go` signs in with `loomtest.SignIn(t, app, "ada")` and
drives each page on Postgres. `q.Attach(app)` makes the test's job queue
the app's, so the Notify job and auth's mail run on `q.Drain`.
`e2e/issues_test.go` signs in through auth's form in headless Chrome
and uses the shortcuts, a menu and the composer from the keyboard.
