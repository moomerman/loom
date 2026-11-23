import "github.com/moomerman/loom/dashboard/ui"

component Sessions(sessions []SessionRow, browsers int, summary string)

<style>
  .content { padding: token(space.5) token(space.6) token(space.6); max-width: 90rem; }
  .note { margin-bottom: token(space.4); color: token(color.muted); font-size: token(text.sm); }
  .card { border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); overflow-x: auto; }
  thead th { background: token(color.subtle); }
  tbody tr:hover td { background: token(color.hover); }
  .own td { color: token(color.faint); }
  .url { font-family: token(font.mono); font-size: token(text.code); font-weight: 500; white-space: nowrap; }
  .route { display: block; color: token(color.faint); font-family: token(font.mono); font-size: token(text.xs); }
  .page { color: token(color.muted); font-family: token(font.mono); font-size: token(text.code); white-space: nowrap; }
  .sid { color: token(color.faint); font-family: token(font.mono); font-size: token(text.xs); white-space: nowrap; }
  .state { display: inline-flex; align-items: center; gap: token(space.1-5); white-space: nowrap; font-weight: 500; }
  .dot { width: 0.5rem; height: 0.5rem; border-radius: token(radius.full); background: token(color.kind-other); }
  .connected .dot { background: token(color.kind-job); }
  .busy .dot { background: token(color.kind-error); }
  .num { width: 1%; text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .zero { color: token(color.faint); }
  .bad { color: token(color.danger); font-weight: 600; }
  .when { color: token(color.muted); white-space: nowrap; }
  .empty-card { border: 1px dashed token(color.line-strong); border-radius: token(radius.lg); }
  @media (max-width: 48rem) { .content { padding: token(space.4); } .page, .sid { display: none; } }
</style>

<.topbar>
  Sessions
  <:actions><.live /></:actions>
</.topbar>
<div class="content">
  <p class="note">{summary}. Each open tab is a session on the server, which keeps its state while the browser is away.</p>
  <div :if={len(sessions) == 0} class="empty-card"><.empty_state title="No page is open." /></div>
  <div :if={len(sessions) > 0} class="card">
    <table>
      <thead>
        <tr><th>Page</th><th>Handled by</th><th>State</th><th class="num">Browsers</th><th>Idle</th><th>Opened</th><th class="num">Renders</th><th class="num">Restarts</th><th>Session</th></tr>
      </thead>
      <tbody>
        <tr :for={s <- sessions} :key={s.sid} class={if s.own, "own", ""}>
          <td><a href={s.url} class="url">{s.url}</a><span :if={s.path != s.url} class="route">{s.path}</span></td>
          <td class="page">{s.page}</td>
          <td><span class={"state" if s.state == "connected", "connected" if s.state != "connected" && s.state != "idle", "busy"}><span class="dot" aria-hidden="true"></span>{s.state}</span></td>
          <td class={"num" if s.connected == 0, "zero"}>{s.connected}</td>
          <td class="when">{s.idle}</td>
          <td class="when">{s.started}</td>
          <td class="num">{s.version}</td>
          <td class={"num" if s.restarts == 0, "zero" if s.restarts > 0, "bad"}>{s.restarts}</td>
          <td class="sid">{s.sid}</td>
        </tr>
      </tbody>
    </table>
  </div>
</div>
