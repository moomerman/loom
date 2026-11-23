import "github.com/moomerman/loom/dashboard/ui"

component Queries(has_db bool, queries []QueryRow, statements int, summary string)

<style>
  .content { padding: token(space.5) token(space.6) token(space.6); max-width: 90rem; }
  .note { margin-bottom: token(space.4); color: token(color.muted); font-size: token(text.sm); }
  .card { border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); overflow-x: auto; }
  thead th { background: token(color.subtle); }
  tbody tr:hover td { background: token(color.hover); }
  .query { min-width: 20rem; }
  .query code { display: block; max-width: 34rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: token(color.on-surface); }
  .num { width: 1%; text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .zero { color: token(color.faint); }
  .bad { color: token(color.danger); font-weight: 600; }
  .share { display: flex; align-items: center; gap: token(space.2); min-width: 10rem; white-space: nowrap; }
  progress { appearance: none; flex: 1; height: 0.375rem; border: 0; border-radius: token(radius.full); background: token(color.subtle); overflow: hidden; }
  progress::-webkit-progress-bar { background: token(color.subtle); border-radius: token(radius.full); }
  progress::-webkit-progress-value { background: token(color.kind-data); border-radius: token(radius.full); }
  progress::-moz-progress-bar { background: token(color.kind-data); border-radius: token(radius.full); }
  .pct { color: token(color.muted); font-size: token(text.sm); font-variant-numeric: tabular-nums; }
  .empty-card { border: 1px dashed token(color.line-strong); border-radius: token(radius.lg); }
  @media (max-width: 48rem) { .content { padding: token(space.4); } }
</style>

<.topbar>
  Queries
  <:actions><.live /></:actions>
</.topbar>
<div class="content">
  <div :if={!has_db} class="empty-card"><.empty_state title="This app has no database." /></div>
  <div :if={has_db && len(queries) == 0} class="empty-card"><.empty_state title="No statements yet." /></div>
  <p :if={len(queries) > 0} class="note">{summary}, since the app started. The busiest first, by total time. Arguments are never kept.</p>
  <div :if={len(queries) > 0} class="card">
    <table>
      <thead>
        <tr><th>Statement</th><th>Share of time</th><th class="num">Runs</th><th class="num">Last minute</th><th class="num">Mean</th><th class="num">p95</th><th class="num">Max</th><th class="num">Rows</th><th class="num">Errors</th></tr>
      </thead>
      <tbody>
        <tr :for={q <- queries} :key={q.query}>
          <td class="query"><code title={q.query}>{q.query}</code></td>
          <td><span class="share"><progress value={q.share} max="100" aria-label="share of time">{q.share}</progress><span class="pct">{q.total}</span></span></td>
          <td class="num">{q.count}</td>
          <td class={"num" if q.last_minute == 0, "zero"}>{q.last_minute}</td>
          <td class="num">{q.mean}</td>
          <td class="num">{q.p95}</td>
          <td class="num">{q.max}</td>
          <td class={"num" if q.rows == 0, "zero"}>{q.rows}</td>
          <td class={"num" if q.errors == 0, "zero" if q.errors > 0, "bad"}>{q.errors}</td>
        </tr>
      </tbody>
    </table>
  </div>
</div>
