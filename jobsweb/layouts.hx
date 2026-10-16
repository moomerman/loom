import "github.com/moomerman/loom/jobsweb/ui"

component Root(title string, inner_block slot)

<style>
  :global(*) { box-sizing: border-box; }
  :global(html), :global(body) { height: 100%; }
  :global(body) { margin: 0; background: token(color.page); color: token(color.on-page); font-family: token(font.body); font-size: token(text.md); line-height: 1.5; -webkit-font-smoothing: antialiased; }
  :global(a) { color: token(color.accent); text-decoration: none; }
  :global(a:hover) { text-decoration: underline; text-underline-offset: 2px; }
  :global(code), :global(pre) { font-family: token(font.mono); font-size: token(text.code); }
  :global(pre) { margin: 0; background: token(color.code); color: token(color.on-code); border: 1px solid token(color.line); border-radius: token(radius.md); padding: token(space.3) token(space.4); overflow: auto; line-height: 1.6; }
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
  <.live_title default="Jobs" suffix=" · Jobs">{title}</.live_title>
  <.live_meta />
  <link rel="icon" href={asset("icon.svg")} />
  <.loom_styles />
</head>
<body>
  {render_slot(inner_block)}
  <.loom_scripts />
</body>
</html>

component Shell(section string, read_only bool, inner_block slot)

<style>
  .app { display: grid; grid-template-columns: 14rem 1fr; height: 100%; }
  .sidebar { display: flex; flex-direction: column; gap: token(space.5); padding: token(space.3) token(space.2); background: token(color.sidebar); color: token(color.on-sidebar); border-right: 1px solid token(color.line); overflow-y: auto; }
  .brand { display: flex; align-items: center; gap: token(space.2); padding: token(space.1) token(space.2); color: token(color.on-page); }
  .logo { display: grid; place-items: center; width: 1.375rem; height: 1.375rem; border-radius: token(radius.md); background: token(color.accent); color: token(color.on-accent); }
  .logo svg { width: 0.875rem; height: 0.875rem; }
  .name { font-weight: 600; }
  nav { display: grid; gap: token(space.px); }
  .heading { padding: token(space.1) token(space.2); font-size: token(text.sm); font-weight: 500; color: token(color.faint); }
  .item { display: flex; align-items: center; gap: token(space.2); padding: token(space.1) token(space.2); border-radius: token(radius.md); color: inherit; font-weight: 500; }
  .item:hover { background: token(color.hover); text-decoration: none; }
  .item:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .current { background: token(color.selected); color: token(color.on-page); }
  .icon { width: 1rem; height: 1rem; flex: none; color: token(color.muted); }
  .dot { width: 0.5rem; height: 0.5rem; margin: 0 token(space.1); flex: none; border-radius: token(radius.full); }
  .executing { background: token(color.state-executing); }
  .retryable { background: token(color.state-retryable); }
  .discarded { background: token(color.state-discarded); }
  .scheduled { background: token(color.state-scheduled); }
  .mode { margin-top: auto; padding: token(space.2); border-top: 1px solid token(color.line); color: token(color.muted); font-size: token(text.sm); }
  .main { min-width: 0; overflow-y: auto; background: token(color.page); }
  @media (max-width: 48rem) {
    .app { grid-template-columns: 1fr; grid-template-rows: auto 1fr; }
    .sidebar { flex-direction: row; align-items: center; gap: token(space.2); padding: token(space.2) token(space.3); border-right: 0; border-bottom: 1px solid token(color.line); overflow: visible; }
    nav { display: flex; gap: token(space.1); }
    .views, .name { display: none; }
    .mode { margin: 0 0 0 auto; padding: 0; border: 0; }
    .mode span { display: none; }
  }
</style>

<div class="app">
  <aside class="sidebar">
    <div class="brand">
      <span class="logo" aria-hidden="true"><svg viewBox="0 0 16 16"><path d="M3 4.5h10M3 8h10M3 11.5h6" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" /></svg></span>
      <span class="name">Jobs</span>
    </div>
    <nav aria-label="Dashboard">
      <a href={path("/")} class={"item" if section == "overview", "current"} aria-current={if section == "overview", "page", "false"}>
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="2" width="5" height="5" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.3" /><rect x="9" y="2" width="5" height="5" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.3" /><rect x="2" y="9" width="5" height="5" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.3" /><rect x="9" y="9" width="5" height="5" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.3" /></svg>
        Overview
      </a>
      <a href={path("/jobs")} class={"item" if section == "jobs", "current"} aria-current={if section == "jobs", "page", "false"}>
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><path d="M5.5 4h8M5.5 8h8M5.5 12h8" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" /><circle cx="2.75" cy="4" r="0.9" fill="currentColor" /><circle cx="2.75" cy="8" r="0.9" fill="currentColor" /><circle cx="2.75" cy="12" r="0.9" fill="currentColor" /></svg>
        Jobs
      </a>
      <a href={path("/cron")} class={"item" if section == "cron", "current"} aria-current={if section == "cron", "page", "false"}>
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="1.3" /><path d="M8 4.5V8l2.5 1.5" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" /></svg>
        Cron
      </a>
    </nav>
    <nav class="views" aria-label="Views">
      <span class="heading">Views</span>
      <a href={path("/jobs?state=executing")} class="item"><span class="dot executing" aria-hidden="true"></span>Running</a>
      <a href={path("/jobs?state=retryable")} class="item"><span class="dot retryable" aria-hidden="true"></span>Retrying</a>
      <a href={path("/jobs?state=discarded")} class="item"><span class="dot discarded" aria-hidden="true"></span>Failed</a>
      <a href={path("/jobs?state=scheduled")} class="item"><span class="dot scheduled" aria-hidden="true"></span>Scheduled</a>
    </nav>
    <div :if={read_only} class="mode"><.badge label="read only" /> <span>You can look, not change.</span></div>
  </aside>
  <main class="main">{render_slot(inner_block)}</main>
</div>
