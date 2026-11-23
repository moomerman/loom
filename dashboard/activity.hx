component Activity(kind string, tabs []Tab, events []EventRow, kept int)

<style>
  .content { padding: token(space.5) token(space.6) token(space.6); max-width: 90rem; }
  .filters { display: flex; align-items: center; gap: token(space.3); flex-wrap: wrap; margin-bottom: token(space.4); }
  .tabs { display: flex; gap: token(space.1); flex-wrap: wrap; padding: token(space.1); border-radius: token(radius.lg); background: token(color.subtle); }
  .tab { display: inline-flex; align-items: center; gap: token(space.1-5); height: 1.75rem; padding: 0 token(space.3); border: 1px solid transparent; border-radius: token(radius.md); color: token(color.muted); font-size: token(text.sm); font-weight: 500; text-decoration: none; }
  .tab:hover { color: token(color.on-page); text-decoration: none; }
  .tab:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .current { background: token(color.surface); border-color: token(color.line); color: token(color.on-page); box-shadow: token(shadow.sm); }
  .count { color: token(color.faint); font-variant-numeric: tabular-nums; }
  .dot { width: 0.4375rem; height: 0.4375rem; border-radius: token(radius.full); background: token(color.kind-other); }
  .page .dot { background: token(color.kind-page); }
  .job .dot { background: token(color.kind-job); }
  .data .dot { background: token(color.kind-data); }
  .error .dot { background: token(color.kind-error); }
  .note { margin-left: auto; color: token(color.muted); font-size: token(text.sm); }
  @media (max-width: 48rem) { .content { padding: token(space.4); } .note { margin-left: 0; } }
</style>

<.topbar>
  Activity
  <:actions><.live /></:actions>
</.topbar>
<div class="content">
  <div class="filters">
    <nav class="tabs" aria-label="Kind">
      <a :for={t <- tabs} :key={t.label} href={t.href} class={"tab" t.kind if t.current, "current"} aria-current={if t.current, "page", "false"}>
        <span :if={t.kind != ""} class="dot" aria-hidden="true"></span>{t.label} <span class="count">{t.count}</span>
      </a>
    </nav>
    <span class="note">The newest of the last {kept} events the app kept in memory.</span>
  </div>
  <.event_table events={events} empty="Nothing has happened yet." />
</div>
