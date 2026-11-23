import "github.com/moomerman/loom/dashboard/ui"

component RouteList(sections []RouteSection, filter string, summary string)

<style>
  .content { padding: token(space.5) token(space.6) token(space.6); max-width: 90rem; }
  .search { width: 18rem; height: 1.75rem; padding: 0 token(space.2); border: 1px solid token(color.line); border-radius: token(radius.md); background: token(color.surface); color: token(color.on-surface); font: inherit; font-size: token(text.sm); }
  .search:focus { outline: 2px solid token(color.focus); outline-offset: -1px; border-color: transparent; }
  .search::placeholder { color: token(color.faint); }
  section { margin-bottom: token(space.8); }
  .heading { display: flex; align-items: baseline; gap: token(space.1) token(space.3); flex-wrap: wrap; margin-bottom: token(space.3); }
  h2 { font-size: token(text.lg); font-weight: 600; }
  .aside { color: token(color.muted); font-size: token(text.sm); }
  .mono { font-family: token(font.mono); font-size: token(text.code); }
  .card { border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); overflow-x: auto; }
  thead th { background: token(color.subtle); }
  tbody tr:hover td { background: token(color.hover); }
  .method { width: 1%; white-space: nowrap; }
  .verb { display: inline-block; min-width: 3rem; padding: 0 token(space.1-5); border-radius: token(radius.sm); background: token(color.subtle); color: token(color.on-subtle); font-family: token(font.mono); font-size: token(text.xs); font-weight: 600; text-align: center; }
  .verb.LIVE { background: token(color.accent-soft); color: token(color.on-accent-soft); }
  .path { font-family: token(font.mono); font-size: token(text.code); font-weight: 500; color: token(color.on-surface); white-space: nowrap; }
  a.path { color: token(color.accent); }
  .example { display: block; font-size: token(text.xs); }
  .component { color: token(color.muted); font-family: token(font.mono); font-size: token(text.code); white-space: nowrap; }
  .access { white-space: nowrap; }
  .extra { min-width: 14rem; max-width: 26rem; color: token(color.muted); font-size: token(text.sm); }
  .extra span { display: block; }
  .num { width: 1%; text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .zero { color: token(color.faint); }
  .empty-card { border: 1px dashed token(color.line-strong); border-radius: token(radius.lg); }
  @media (max-width: 48rem) { .content { padding: token(space.4); } .search { width: 10rem; } .component, .extra { display: none; } }
</style>

<.topbar>
  Routes
  <span class="aside">{summary}</span>
  <:actions>
    <input class="search" type="search" name="filter" placeholder="Find a route…" aria-label="Find a route" value={filter} autocomplete="off" on:input="filter" />
  </:actions>
</.topbar>
<div class="content">
  <div :if={len(sections) == 0} class="empty-card"><.empty_state title="No route matches." /></div>
  <section :for={s <- sections} :key={s.title}>
    <div class="heading">
      <h2>{s.title}</h2>
      <span :if={s.prefix != ""} class="aside mono">{s.prefix}</span>
      <span :if={s.type != ""} class="aside">{s.type} · {s.access}</span>
    </div>
    <div class="card">
      <table>
        <thead>
          <tr><th>Kind</th><th>Path</th><th>Renders</th><th>Who</th><th>Notes</th><th class="num">Loads</th></tr>
        </thead>
        <tbody>
          <tr :for={r <- s.routes} :key={r.method + " " + r.path}>
            <td class="method"><span class={"verb" r.method}>{r.method}</span></td>
            <td>
              <a :if={r.href != "" && !r.example} href={r.href} class="path">{r.path}</a>
              <span :if={r.href == "" || r.example} class="path">{r.path}</span>
              <a :if={r.example} href={r.href} class="example">open now: {r.href}</a>
            </td>
            <td class="component">{r.component}</td>
            <td class="access" title={r.checks}>
              <.badge :if={r.access == "public"} label="public" />
              <.badge :if={r.access == "signed in"} label="signed in" tone="accent" />
              <.badge :if={r.access == "restricted"} label="restricted" tone="warning" />
              <.badge :if={r.dev_only} label="dev only" tone="warning" />
            </td>
            <td class="extra">
              <span :if={r.watch != ""}>watches {r.watch}</span>
              <span :if={r.limits != ""}>{r.limits}</span>
            </td>
            <td class={"num" if r.views == 0, "zero"}><span :if={r.method == "LIVE"}>{r.views}</span></td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</div>
