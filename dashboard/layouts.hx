import "github.com/moomerman/loom/dashboard/ui"

component Root(title string, inner_block slot)

<style>
  :global(*) { box-sizing: border-box; }
  :global(html), :global(body) { height: 100%; }
  :global(body) { margin: 0; background: token(color.page); color: token(color.on-page); font-family: token(font.body); font-size: token(text.md); line-height: 1.5; -webkit-font-smoothing: antialiased; }
  :global(a) { color: token(color.accent); text-decoration: none; }
  :global(a:hover) { text-decoration: underline; text-underline-offset: 2px; }
  :global(code), :global(pre) { font-family: token(font.mono); font-size: token(text.code); }
  :global(h1), :global(h2) { margin: 0; }
  :global(table) { width: 100%; border-collapse: collapse; }
  :global(th), :global(td) { text-align: left; padding: token(space.2) token(space.3); border-bottom: 1px solid token(color.line); vertical-align: middle; }
  :global(tbody tr:last-child td) { border-bottom: 0; }
  :global(th) { color: token(color.muted); font-weight: 500; font-size: token(text.sm); white-space: nowrap; }
  :global(::selection) { background: token(color.selected); }
  :global(#loom-root) { height: 100%; }
</style>

<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <.live_title default="Loom" suffix=" · Loom">{title}</.live_title>
  <.live_meta />
  <link rel="icon" href={asset("icon.svg")} />
  <.loom_styles />
</head>
<body>
  {render_slot(inner_block)}
  <.loom_scripts />
</body>
</html>

component Shell(section string, app string, dev bool, links []Link, inner_block slot)

<style>
  .app { display: grid; grid-template-columns: 14rem 1fr; height: 100%; }
  .sidebar { display: flex; flex-direction: column; gap: token(space.5); padding: token(space.3) token(space.2); background: token(color.sidebar); color: token(color.on-sidebar); border-right: 1px solid token(color.line); overflow-y: auto; }
  .brand { display: flex; align-items: center; gap: token(space.2); padding: token(space.1) token(space.2); color: token(color.on-page); }
  .logo { display: grid; place-items: center; width: 1.375rem; height: 1.375rem; border-radius: token(radius.md); background: token(color.accent); color: token(color.on-accent); }
  .logo svg { width: 0.875rem; height: 0.875rem; }
  .name { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  nav { display: grid; gap: token(space.px); }
  .heading { padding: token(space.1) token(space.2); font-size: token(text.sm); font-weight: 500; color: token(color.faint); }
  .item { display: flex; align-items: center; gap: token(space.2); padding: token(space.1) token(space.2); border-radius: token(radius.md); color: inherit; font-weight: 500; }
  .item:hover { background: token(color.hover); text-decoration: none; }
  .item:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .current { background: token(color.selected); color: token(color.on-page); }
  .icon { width: 1rem; height: 1rem; flex: none; color: token(color.muted); }
  .path { margin-left: auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: token(color.faint); font-family: token(font.mono); font-size: token(text.xs); font-weight: 400; }
  .mode { margin-top: auto; padding: token(space.2); border-top: 1px solid token(color.line); color: token(color.muted); font-size: token(text.sm); }
  .main { min-width: 0; overflow-y: auto; background: token(color.page); }
  @media (max-width: 48rem) {
    .app { grid-template-columns: 1fr; grid-template-rows: auto 1fr; }
    .sidebar { flex-direction: row; align-items: center; gap: token(space.2); padding: token(space.2) token(space.3); border-right: 0; border-bottom: 1px solid token(color.line); overflow-x: auto; overflow-y: visible; }
    nav { display: flex; gap: token(space.1); }
    .elsewhere, .name, .label { display: none; }
    .mode { margin: 0 0 0 auto; padding: 0; border: 0; }
    .mode span { display: none; }
  }
</style>

<div class="app">
  <aside class="sidebar">
    <div class="brand">
      <span class="logo" aria-hidden="true"><svg viewBox="0 0 16 16"><path d="M1.5 8.5h2.5l1.75-4.5 3.5 8 1.75-3.5h3.5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg></span>
      <span class="name">{app}</span>
    </div>
    <nav aria-label="Dashboard">
      <a href={path("/")} class={"item" if section == "overview", "current"} aria-current={if section == "overview", "page", "false"}>
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="2" width="5" height="5" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.3" /><rect x="9" y="2" width="5" height="5" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.3" /><rect x="2" y="9" width="5" height="5" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.3" /><rect x="9" y="9" width="5" height="5" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.3" /></svg>
        <span class="label">Overview</span>
      </a>
      <a href={path("/routes")} class={"item" if section == "routes", "current"} aria-current={if section == "routes", "page", "false"}>
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><path d="M2.5 4h4.5M2.5 8h7M2.5 12h5" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" /><path d="M11 10.5l2.5 1.5-2.5 1.5" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" /></svg>
        <span class="label">Routes</span>
      </a>
      <a href={path("/activity")} class={"item" if section == "activity", "current"} aria-current={if section == "activity", "page", "false"}>
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><path d="M1.5 8.5h2.5l1.75-4.5 3.5 8 1.75-3.5h3.5" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" /></svg>
        <span class="label">Activity</span>
      </a>
      <a href={path("/queries")} class={"item" if section == "queries", "current"} aria-current={if section == "queries", "page", "false"}>
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><ellipse cx="8" cy="4" rx="5.5" ry="2" fill="none" stroke="currentColor" stroke-width="1.3" /><path d="M2.5 4v8c0 1.1 2.5 2 5.5 2s5.5-.9 5.5-2V4M2.5 8c0 1.1 2.5 2 5.5 2s5.5-.9 5.5-2" fill="none" stroke="currentColor" stroke-width="1.3" /></svg>
        <span class="label">Queries</span>
      </a>
      <a href={path("/sessions")} class={"item" if section == "sessions", "current"} aria-current={if section == "sessions", "page", "false"}>
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><rect x="1.75" y="2.75" width="12.5" height="9" rx="1.5" fill="none" stroke="currentColor" stroke-width="1.3" /><path d="M5.5 14h5" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" /></svg>
        <span class="label">Sessions</span>
      </a>
    </nav>
    <nav :if={len(links) > 0} class="elsewhere" aria-label="Elsewhere">
      <span class="heading">Elsewhere</span>
      <a :for={l <- links} :key={l.href} href={l.href} class="item">
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><path d="M9 3h4v4M13 3L7.5 8.5M11 9.5V13H3V5h3.5" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" /></svg>
        {l.label}
        <span class="path">{l.href}</span>
      </a>
    </nav>
    <div class="mode">
      <.badge :if={dev} label="dev" tone="warning" />
      <.badge :if={!dev} label="production" />
      <span>{if dev, "Running under loom dev.", "Numbers since the app started."}</span>
    </div>
  </aside>
  <main class="main">{render_slot(inner_block)}</main>
</div>
