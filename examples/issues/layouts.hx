import "github.com/moomerman/loom/examples/issues/ui"

behavior hotkeys()

behavior toast(id int) emits (dismiss)

component Root(title string, inner_block slot)

<style>
  :global(*) { box-sizing: border-box; }
  :global(html), :global(body) { height: 100%; }
  :global(body) { margin: 0; background: token(color.page); color: token(color.on-page); font-family: token(font.body); font-size: token(text.md); line-height: 1.5; -webkit-font-smoothing: antialiased; }
  :global(a) { color: inherit; text-decoration: none; }
  :global(button) { font: inherit; color: inherit; }
  :global(kbd) { display: inline-grid; place-items: center; min-width: 1.4em; padding: 0 token(space.1); border: 1px solid token(color.line-strong); border-bottom-width: 2px; border-radius: token(radius.sm); background: token(color.surface); color: token(color.muted); font-family: token(font.body); font-size: token(text.xs); line-height: 1.5; }
  :global(::selection) { background: token(color.selected); }
  :global(#loom-root) { height: 100%; }
</style>

<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <.live_title default="Loom Issues" suffix=" · Loom Issues">{title}</.live_title>
  <.live_meta />
  <link rel="icon" href={asset("icon.svg")} />
  <.loom_styles />
</head>
<body>
  {render_slot(inner_block)}
  <.loom_scripts />
</body>
</html>

component Shell(user User, teams []Team, section string, unread int, composer Composer, toast Toast, help bool, inner_block slot)

<style>
  .app { display: grid; grid-template-columns: 15rem 1fr; height: 100%; }
  .sidebar { display: flex; flex-direction: column; gap: token(space.4); padding: token(space.3) token(space.2); background: token(color.sidebar); color: token(color.on-sidebar); border-right: 1px solid token(color.line); overflow-y: auto; }
  .workspace { display: flex; align-items: center; gap: token(space.2); padding: token(space.1) token(space.2); }
  .logo { display: grid; place-items: center; width: 1.375rem; height: 1.375rem; border-radius: token(radius.md); background: token(color.accent); color: token(color.on-accent); font-weight: 700; font-size: token(text.sm); }
  .name { font-weight: 600; color: token(color.on-page); flex: 1; }
  .new { display: flex; align-items: center; gap: token(space.2); width: 100%; padding: token(space.1-5) token(space.2); border: 1px solid token(color.line-strong); border-radius: token(radius.md); background: token(color.surface); box-shadow: token(shadow.sm); cursor: pointer; font-weight: 500; color: token(color.on-page); }
  .new:hover { background: token(color.hover); }
  .new kbd { margin-left: auto; }
  .new:focus-visible, .item:focus-visible, .foot-button:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  nav { display: grid; gap: token(space.px); }
  .heading { padding: token(space.1) token(space.2); font-size: token(text.sm); font-weight: 500; color: token(color.faint); }
  .item { display: flex; align-items: center; gap: token(space.2); padding: token(space.1) token(space.2); border-radius: token(radius.md); font-weight: 500; }
  .item:hover { background: token(color.hover); }
  .current { background: token(color.selected); color: token(color.on-page); }
  .icon { width: 1rem; height: 1rem; color: token(color.muted); flex: none; }
  .count { margin-left: auto; font-size: token(text.sm); color: token(color.faint); font-variant-numeric: tabular-nums; }
  .unread { margin-left: auto; min-width: 1.25rem; padding: 0 token(space.1-5); border-radius: token(radius.full); background: token(color.accent); color: token(color.on-accent); font-size: token(text.xs); font-weight: 600; text-align: center; }
  .foot { margin-top: auto; display: flex; align-items: center; gap: token(space.2); padding: token(space.2); border-top: 1px solid token(color.line); }
  .who { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
  .foot form { margin: 0; }
  .foot-button { display: grid; place-items: center; padding: token(space.1); border: 0; border-radius: token(radius.sm); background: none; color: token(color.muted); font-size: token(text.sm); cursor: pointer; }
  .foot-button:hover { background: token(color.hover); color: token(color.on-page); }
  .main { min-width: 0; overflow-y: auto; background: token(color.page); }
  @media (max-width: 48rem) { .app { grid-template-columns: 1fr; } .sidebar { display: none; } }
</style>

<div class="app" use:hotkeys>
  <aside class="sidebar">
    <div class="workspace">
      <span class="logo" aria-hidden="true">L</span>
      <span class="name">Loom</span>
      <.avatar user={user} />
    </div>
    <button class="new" type="button" data-hotkey="c" on:click="compose">
      <svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><path d="M11.5 1.5l3 3L6 13H3v-3z" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round" /></svg>
      New issue <kbd>C</kbd>
    </button>
    <nav aria-label="Workspace">
      <a href="/inbox" class={"item" if section == "inbox", "current"} data-hotkey="g i">
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><path d="M2 9.5l1.6-6A1 1 0 0 1 4.6 2.8h6.8a1 1 0 0 1 1 .7l1.6 6V13a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1z M2 9.5h3.5l1 1.5h3l1-1.5H14" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round" /></svg>
        Inbox
        <span :if={unread > 0} class="unread">{unread}</span>
      </a>
      <a href="/" class={"item" if section == "mine", "current"} data-hotkey="g m">
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="1.3" /><circle cx="8" cy="6.5" r="2" fill="currentColor" /><path d="M4.5 12.2a4 4 0 0 1 7 0" fill="none" stroke="currentColor" stroke-width="1.3" /></svg>
        My issues
      </a>
    </nav>
    <nav aria-label="Teams">
      <span class="heading">Your teams</span>
      <a :for={t <- teams} :key={t.id} href={"/team/" + t.key} class={"item" if section == "team:" + t.key, "current"} data-hotkey={t.hotkey}>
        <.team_icon team={t} />
        {t.name}
        <span class="count">{t.open}</span>
      </a>
    </nav>
    <div class="foot">
      <.avatar user={user} />
      <span class="who">{user.name}</span>
      <button class="foot-button" type="button" data-hotkey="?" on:click="help" aria-label="Keyboard shortcuts"><kbd>?</kbd></button>
      <form method="post" action="/auth/sign-out"><button class="foot-button" type="submit" aria-label="Sign out" title="Sign out"><svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><path d="M6 2.5H3.5a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1H6M10.5 11l3-3-3-3M13.5 8H6" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" /></svg></button></form>
    </div>
  </aside>
  <main class="main">{render_slot(inner_block)}</main>
  <.new_issue_dialog c={composer} />
  <.toast_bar toast={toast} />
  <.shortcuts open={help} />
</div>

component ToastBar(toast Toast)

<style>
  .toast { position: fixed; right: token(space.5); bottom: token(space.5); display: flex; align-items: center; gap: token(space.3); padding: token(space.2) token(space.2) token(space.2) token(space.3); border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); color: token(color.on-surface); box-shadow: token(shadow.lg); animation: rise token(duration.fast) ease-out; }
  .check { display: grid; place-items: center; width: 1.125rem; height: 1.125rem; border-radius: token(radius.full); background: token(color.status-done); color: token(color.on-accent); font-size: token(text.xs); }
  a { color: token(color.accent); font-weight: 500; }
  button { border: 0; background: none; color: token(color.muted); cursor: pointer; border-radius: token(radius.sm); padding: 0 token(space.1); font-size: token(text.lg); }
  button:hover { color: token(color.on-surface); }
  @keyframes rise { from { transform: translateY(8px); opacity: 0; } }
</style>

<div class="toast" role="status" :if={toast.text != ""} use:toast={id: toast.id} on:dismiss="toast_close">
  <span class="check" aria-hidden="true">✓</span>
  <span>{toast.text}</span>
  <a :if={toast.href != ""} href={toast.href}>{toast.link}</a>
  <button type="button" aria-label="Dismiss" on:click="toast_close">×</button>
</div>

component Shortcuts(open bool)

<style>
  dl { display: grid; grid-template-columns: 1fr auto; gap: token(space.2) token(space.6); margin: 0; }
  dt { color: token(color.on-surface); }
  dd { margin: 0; display: flex; gap: token(space.1); justify-content: flex-end; }
</style>

<.dialog open={open} title="Keyboard shortcuts" on:close="help_close">
  <dl>
    <dt>New issue</dt><dd><kbd>C</kbd></dd>
    <dt>Search</dt><dd><kbd>/</kbd></dd>
    <dt>Next or previous issue</dt><dd><kbd>J</kbd><kbd>K</kbd></dd>
    <dt>Open the focused issue</dt><dd><kbd>Enter</kbd></dd>
    <dt>Go to inbox</dt><dd><kbd>G</kbd><kbd>I</kbd></dd>
    <dt>Go to my issues</dt><dd><kbd>G</kbd><kbd>M</kbd></dd>
    <dt>Go to a team</dt><dd><kbd>G</kbd><kbd>1</kbd> … <kbd>9</kbd></dd>
    <dt>Close a dialog or menu</dt><dd><kbd>Esc</kbd></dd>
    <dt>This list</dt><dd><kbd>?</kbd></dd>
  </dl>
</.dialog>
