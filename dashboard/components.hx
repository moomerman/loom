import "github.com/moomerman/loom/dashboard/ui"

component Topbar(inner_block slot, actions slot)

<style>
  .topbar { display: flex; align-items: center; gap: token(space.3); min-height: 3rem; padding: token(space.2) token(space.6); border-bottom: 1px solid token(color.line); flex-wrap: wrap; }
  .crumbs { display: flex; align-items: center; gap: token(space.2); min-width: 0; font-weight: 500; color: token(color.on-page); }
  .actions { margin-left: auto; display: flex; align-items: center; gap: token(space.2); color: token(color.muted); font-size: token(text.sm); }
  @media (max-width: 48rem) { .topbar { padding: token(space.2) token(space.4); } }
</style>
<header class="topbar">
  <div class="crumbs">{render_slot(inner_block)}</div>
  <div class="actions">{render_slot(actions)}</div>
</header>

component Live()

<style>
  .live { display: inline-flex; align-items: center; gap: token(space.1-5); color: token(color.muted); font-size: token(text.sm); }
  .dot { width: 0.4375rem; height: 0.4375rem; border-radius: token(radius.full); background: token(color.kind-job); animation: pulse token(duration.pulse) ease-in-out infinite; }
  @keyframes pulse { 50% { opacity: 0.35; } }
  @media (prefers-reduced-motion: reduce) { .dot { animation: none; } }
</style>
<span class="live"><span class="dot" aria-hidden="true"></span>Live</span>

component EventTable(events []EventRow, empty string)

<style>
  .events { border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); overflow-x: auto; }
  thead th { background: token(color.subtle); }
  tbody tr:hover td { background: token(color.hover); }
  .when { width: 1%; white-space: nowrap; color: token(color.muted); font-family: token(font.mono); font-size: token(text.code); font-variant-numeric: tabular-nums; }
  .name { width: 1%; white-space: nowrap; }
  .kind { display: inline-flex; align-items: center; gap: token(space.1-5); font-family: token(font.mono); font-size: token(text.code); color: token(color.on-surface); }
  .dot { width: 0.5rem; height: 0.5rem; flex: none; border-radius: token(radius.full); background: token(color.kind-other); }
  .page .dot { background: token(color.kind-page); }
  .job .dot { background: token(color.kind-job); }
  .data .dot { background: token(color.kind-data); }
  .error .dot { background: token(color.kind-error); }
  .subject { font-weight: 500; color: token(color.on-surface); white-space: nowrap; }
  .detail { max-width: 36rem; color: token(color.muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .bad .detail { color: token(color.danger); }
  .ms { width: 1%; text-align: right; white-space: nowrap; color: token(color.muted); font-variant-numeric: tabular-nums; }
  .empty-card { border: 1px dashed token(color.line-strong); border-radius: token(radius.lg); }
</style>

<div :if={len(events) == 0} class="empty-card"><.empty_state title={empty} /></div>
<div :if={len(events) > 0} class="events">
  <table>
    <thead>
      <tr><th>Time</th><th>Event</th><th>About</th><th>Detail</th><th class="ms">Took</th></tr>
    </thead>
    <tbody>
      <tr :for={e <- events} :key={e.seq} class={if e.error, "bad", ""}>
        <td class="when" title={e.ago}>{e.time}</td>
        <td class="name"><span class={"kind" e.kind}><span class="dot" aria-hidden="true"></span>{e.name}</span></td>
        <td class="subject">{e.subject}</td>
        <td class="detail" title={e.detail}>{e.detail}</td>
        <td class="ms">{e.ms}</td>
      </tr>
    </tbody>
  </table>
</div>
