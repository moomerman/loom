component Root(title string, inner_block slot)

<style>
  :global(*) { box-sizing: border-box; }
  :global(html), :global(body) { height: 100%; }
  :global(body) { margin: 0; font-family: token(font.body); font-size: token(text.md); line-height: 1.5; background: token(color.page); color: token(color.on-page); -webkit-font-smoothing: antialiased; }
  :global(a) { color: inherit; text-decoration: none; }
  :global(button) { font: inherit; color: inherit; }
  :global(::selection) { background: token(color.accent-soft); }
  :global(#loom-root) { height: 100%; }
</style>

<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <.live_title default="Tally" suffix=" · Tally">{title}</.live_title>
  <.live_meta />
  <link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Crect width='32' height='32' rx='8' fill='%230d7a60'/%3E%3Cpath d='M9 10h14M16 10v13' stroke='white' stroke-width='3.5' stroke-linecap='round'/%3E%3C/svg%3E" />
  <.loom_styles />
</head>
<body>
  {render_slot(inner_block)}
  <.loom_scripts />
</body>
</html>

component Shell(me Person, groups []GroupCard, section string, unread int, admin bool, inner_block slot)

<style>
  .app { display: grid; grid-template-columns: 16.5rem 1fr; height: 100%; }
  .sidebar { display: flex; flex-direction: column; gap: token(space.6); padding: token(space.5) token(space.3); background: token(color.sidebar); color: token(color.on-sidebar); border-right: 1px solid token(color.line); overflow-y: auto; }
  .brand { display: flex; align-items: center; gap: token(space.2); padding: 0 token(space.2); }
  .logo { display: grid; place-items: center; width: 1.75rem; height: 1.75rem; border-radius: token(radius.md); background: token(color.accent); color: token(color.on-accent); font-family: token(font.display); font-weight: 700; font-size: token(text.lg); }
  .wordmark { font-family: token(font.display); font-size: token(text.xl); font-weight: 600; color: token(color.on-page); letter-spacing: -0.01em; }
  nav { display: grid; gap: token(space.px); }
  .heading { display: flex; justify-content: space-between; padding: token(space.1) token(space.2); font-size: token(text.xs); font-weight: 600; letter-spacing: 0.06em; text-transform: uppercase; color: token(color.faint); }
  .item { display: flex; align-items: center; gap: token(space.3); padding: token(space.1-5) token(space.2); border-radius: token(radius.md); font-weight: 500; }
  .item:hover { background: token(color.hover); }
  .item:focus-visible, .signout:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .current { background: token(color.selected); color: token(color.on-page); }
  .glyph { display: grid; place-items: center; width: 1.5rem; height: 1.5rem; color: token(color.muted); }
  .glyph svg { width: 1.125rem; height: 1.125rem; }
  .label { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .unread { min-width: 1.375rem; padding: 0 token(space.1-5); border-radius: token(radius.full); background: token(color.accent); color: token(color.on-accent); font-size: token(text.xs); font-weight: 700; text-align: center; }
  .foot { margin-top: auto; display: flex; align-items: center; gap: token(space.2); padding: token(space.3) token(space.2) 0; border-top: 1px solid token(color.line); }
  .who { flex: 1; min-width: 0; line-height: 1.25; }
  .who strong { display: block; color: token(color.on-page); font-weight: 600; font-size: token(text.sm); }
  .who span { font-size: token(text.xs); color: token(color.muted); }
  .foot form { margin: 0; }
  .signout { display: grid; place-items: center; padding: token(space.1-5); border: 0; border-radius: token(radius.sm); background: none; color: token(color.muted); cursor: pointer; }
  .signout:hover { background: token(color.hover); color: token(color.on-page); }
  .signout svg { width: 1rem; height: 1rem; }
  .main { min-width: 0; overflow-y: auto; }
  @media (max-width: 52rem) {
    .app { grid-template-columns: 1fr; grid-template-rows: auto 1fr; }
    .sidebar { flex-direction: row; align-items: center; gap: token(space.2); padding: token(space.2) token(space.3); border-right: 0; border-bottom: 1px solid token(color.line); overflow-x: auto; }
    .sidebar nav { display: flex; }
    .groups, .foot, .label-wide { display: none; }
    .wordmark { display: none; }
  }
</style>

<div class="app">
  <aside class="sidebar">
    <a class="brand" href="/">
      <span class="logo" aria-hidden="true">T</span>
      <span class="wordmark">Tally</span>
    </a>
    <nav aria-label="Main">
      <a href="/" class={"item" if section == "dashboard", "current"}>
        <span class="glyph"><svg viewBox="0 0 20 20" aria-hidden="true"><path d="M3 9.5 10 4l7 5.5V16a1 1 0 0 1-1 1h-3.5v-4.5h-5V17H4a1 1 0 0 1-1-1z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" /></svg></span>
        <span class="label">Dashboard</span>
      </a>
      <a href="/activity" class={"item" if section == "activity", "current"}>
        <span class="glyph"><svg viewBox="0 0 20 20" aria-hidden="true"><path d="M10 3.5a4.5 4.5 0 0 0-4.5 4.5v3L4 13.5h12L14.5 11V8A4.5 4.5 0 0 0 10 3.5zM8.25 16a1.75 1.75 0 0 0 3.5 0" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" /></svg></span>
        <span class="label">Activity</span>
        <span :if={unread > 0} class="unread">{unread}</span>
      </a>
      <a :if={admin} href="/admin/jobs" class="item">
        <span class="glyph"><svg viewBox="0 0 20 20" aria-hidden="true"><path d="M4 5h12M4 10h12M4 15h7" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" /></svg></span>
        <span class="label">Background jobs</span>
      </a>
    </nav>
    <nav class="groups" aria-label="Groups">
      <span class="heading">Groups</span>
      <a :for={g <- groups} :key={g.id} href={g.href} class={"item" if g.current, "current"}>
        <.group_tile emoji={g.emoji} color={g.color} size="sm" />
        <span class="label">{g.name}</span>
        <.money amount={g.balance} size="sm" />
      </a>
    </nav>
    <div class="foot">
      <.avatar person={me} size="md" />
      <span class="who"><strong>{me.name}</strong><span>{me.email}</span></span>
      <form method="post" action="/logout"><button class="signout" type="submit" aria-label="Sign out" title="Sign out"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M6 2.5H3.5a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1H6M10.5 11l3-3-3-3M13.5 8H6" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" /></svg></button></form>
    </div>
  </aside>
  <main class="main">{render_slot(inner_block)}</main>
</div>
