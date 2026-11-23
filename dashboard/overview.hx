import "github.com/moomerman/loom/dashboard/ui"

component Overview(uptime string, started string, pages int, browsers int, actions_last_minute int, jobs_last_minute int, errors int, errors_hint string, memory string, heap string, goroutines int, gc_runs int, gc_pause string, has_db bool, db_in_use int, db_max int, db_waits int, bars []Bar, peak int, routes []RouteRow, recent_errors []EventRow, has_jobs bool, jobs_url string, queues []QueueRow, jobs_error string)

<style>
  .content { padding: token(space.6); max-width: 76rem; }
  .stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr)); gap: token(space.3); margin-bottom: token(space.6); }
  .stat { display: grid; gap: token(space.1); padding: token(space.4); border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); color: token(color.on-surface); }
  a.stat:hover { border-color: token(color.line-strong); text-decoration: none; }
  .label { display: flex; align-items: center; gap: token(space.1-5); color: token(color.muted); font-size: token(text.sm); font-weight: 500; }
  .value { font-size: token(text.stat); font-weight: 600; letter-spacing: -0.02em; line-height: 1.2; font-variant-numeric: tabular-nums; }
  .hint { color: token(color.faint); font-size: token(text.sm); overflow-wrap: anywhere; }
  .alarm { border-color: token(color.danger); background: token(color.danger-soft); color: token(color.on-danger-soft); }
  .alarm .label, .alarm .hint { color: token(color.on-danger-soft); }
  .dot { width: 0.5rem; height: 0.5rem; flex: none; border-radius: token(radius.full); }
  .page { background: token(color.kind-page); }
  .job { background: token(color.kind-job); }
  .data { background: token(color.kind-data); }
  .error { background: token(color.kind-error); }
  .other { background: token(color.kind-other); }
  section { margin-bottom: token(space.8); }
  .heading { display: flex; align-items: baseline; gap: token(space.1) token(space.3); flex-wrap: wrap; margin-bottom: token(space.3); }
  h2 { font-size: token(text.lg); font-weight: 600; }
  .aside { color: token(color.muted); font-size: token(text.sm); }
  .push { margin-left: auto; }
  .card { border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); overflow-x: auto; }
  .chart { padding: token(space.4) token(space.4) token(space.3); }
  .chart svg { display: block; width: 100%; height: 7rem; }
  .bar-page { fill: token(color.kind-page); }
  .bar-job { fill: token(color.kind-job); }
  .bar-empty { fill: token(color.subtle); }
  .axis { display: flex; justify-content: space-between; margin-top: token(space.2); color: token(color.faint); font-size: token(text.xs); }
  .legend { display: flex; gap: token(space.4); align-items: center; }
  .legend span { display: inline-flex; align-items: center; gap: token(space.1-5); }
  thead th { background: token(color.subtle); }
  tbody tr:hover td { background: token(color.hover); }
  .route { font-family: token(font.mono); font-size: token(text.code); font-weight: 500; color: token(color.on-surface); }
  a.route { color: token(color.accent); }
  .num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .zero { color: token(color.faint); }
  .bad { color: token(color.danger); font-weight: 600; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(22rem, 1fr)); gap: token(space.6) token(space.6); }
  .facts { display: grid; grid-template-columns: auto 1fr; gap: token(space.2) token(space.4); margin: 0; padding: token(space.4); }
  dt { color: token(color.muted); }
  dd { margin: 0; text-align: right; font-variant-numeric: tabular-nums; font-weight: 500; }
  .empty-card { border: 1px dashed token(color.line-strong); border-radius: token(radius.lg); }
  @media (max-width: 48rem) { .content { padding: token(space.4); } .grid { grid-template-columns: 1fr; } }
</style>

<.topbar>
  Overview
  <:actions><.live /></:actions>
</.topbar>
<div class="content">
  <div class="stats">
    <div class="stat">
      <span class="label">Up for</span>
      <span class="value">{uptime}</span>
      <span class="hint">since {started}</span>
    </div>
    <a href={path("/sessions")} class="stat">
      <span class="label"><span class="dot page" aria-hidden="true"></span>Live pages</span>
      <span class="value">{pages}</span>
      <span class="hint">{browsers} browsers connected</span>
    </a>
    <a href={path("/activity?kind=page")} class="stat">
      <span class="label"><span class="dot page" aria-hidden="true"></span>Actions</span>
      <span class="value">{actions_last_minute}</span>
      <span class="hint">page loads and events, last minute</span>
    </a>
    <a href={path("/activity?kind=job")} class="stat">
      <span class="label"><span class="dot job" aria-hidden="true"></span>Jobs</span>
      <span class="value">{jobs_last_minute}</span>
      <span class="hint">finished, last minute</span>
    </a>
    <a href={path("/activity?kind=error")} class={"stat" if errors > 0, "alarm"}>
      <span class="label"><span class="dot error" aria-hidden="true"></span>Errors</span>
      <span class="value">{errors}</span>
      <span class="hint">{errors_hint}</span>
    </a>
    <div class="stat">
      <span class="label">Memory</span>
      <span class="value">{memory}</span>
      <span class="hint">{goroutines} goroutines</span>
    </div>
  </div>

  <section>
    <div class="heading">
      <h2>Activity</h2>
      <span class="aside">The last five minutes, in ten-second bars.</span>
      <span class="push aside legend">
        <span><span class="dot page" aria-hidden="true"></span>page actions</span>
        <span><span class="dot job" aria-hidden="true"></span>jobs</span>
        <span :if={peak > 0}>peak {peak} per 10s</span>
      </span>
    </div>
    <div class="card chart">
      <svg viewBox="0 0 298 96" preserveAspectRatio="none" role="img" aria-label="Page actions and jobs over the last five minutes">
        <g :for={b <- bars} :key={b.x}>
          <title>{b.title}</title>
          <rect class="bar-empty" x={b.x} y="94" width="8" height="2" />
          <rect :if={b.page_h > 0} class="bar-page" x={b.x} y={b.page_y} width="8" height={b.page_h} />
          <rect :if={b.job_h > 0} class="bar-job" x={b.x} y={b.job_y} width="8" height={b.job_h} />
        </g>
      </svg>
      <div class="axis"><span>5 min ago</span><span>now</span></div>
    </div>
  </section>

  <section>
    <div class="heading">
      <h2>Busiest routes</h2>
      <span class="aside">Since the app started.</span>
      <a href={path("/routes")} class="push aside">All routes →</a>
    </div>
    <div :if={len(routes) == 0} class="empty-card"><.empty_state title="No page has been opened yet." /></div>
    <div :if={len(routes) > 0} class="card">
      <table>
        <thead>
          <tr><th>Route</th><th class="num">Loads</th><th class="num">Events</th><th class="num">Last minute</th><th class="num">Event p95</th><th class="num">Errors</th></tr>
        </thead>
        <tbody>
          <tr :for={r <- routes} :key={r.route}>
            <td>
              <a :if={r.href != ""} href={r.href} class="route">{r.route}</a>
              <span :if={r.href == ""} class="route">{r.route}</span>
            </td>
            <td class={"num" if r.loads == 0, "zero"}>{r.loads}</td>
            <td class={"num" if r.events == 0, "zero"}>{r.events}</td>
            <td class={"num" if r.last_minute == 0, "zero"}>{r.last_minute}</td>
            <td class={"num" if r.p95 == "–", "zero"}>{r.p95}</td>
            <td class={"num" if r.errors == 0, "zero" if r.errors > 0, "bad"}>{r.errors}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>

  <section :if={has_jobs}>
    <div class="heading">
      <h2>Job queues</h2>
      <span :if={jobs_error != ""} class="aside bad">{jobs_error}</span>
      <a :if={jobs_url != ""} href={jobs_url} class="push aside">Jobs dashboard →</a>
    </div>
    <div class="card">
      <table>
        <thead>
          <tr><th>Queue</th><th class="num">Running</th><th class="num">Available</th><th class="num">Scheduled</th><th class="num">Retrying</th><th class="num">Failed</th></tr>
        </thead>
        <tbody>
          <tr :for={q <- queues} :key={q.name}>
            <td class="route">{q.name}</td>
            <td class={"num" if q.running == 0, "zero"}>{q.running} of {q.workers}</td>
            <td class={"num" if q.available == 0, "zero"}>{q.available}</td>
            <td class={"num" if q.scheduled == 0, "zero"}>{q.scheduled}</td>
            <td class={"num" if q.retryable == 0, "zero"}>{q.retryable}</td>
            <td class={"num" if q.discarded == 0, "zero" if q.discarded > 0, "bad"}>{q.discarded}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>

  <section>
    <div class="heading">
      <h2>Recent errors</h2>
      <a :if={errors > 0} href={path("/activity?kind=error")} class="push aside">All errors →</a>
    </div>
    <.event_table events={recent_errors} empty="No errors. Everything is running smoothly." />
  </section>

  <section>
    <div class="grid">
      <div>
        <div class="heading"><h2>Process</h2></div>
        <dl class="card facts">
          <dt>Memory in use</dt><dd>{memory}</dd>
          <dt>Go heap</dt><dd>{heap}</dd>
          <dt>Goroutines</dt><dd>{goroutines}</dd>
          <dt>Garbage collections</dt><dd>{gc_runs}</dd>
          <dt>Longest recent GC pause</dt><dd>{gc_pause}</dd>
        </dl>
      </div>
      <div :if={has_db}>
        <div class="heading"><h2>Database</h2><a href={path("/queries")} class="push aside">Queries →</a></div>
        <dl class="card facts">
          <dt>Connections in use</dt><dd>{db_in_use} of {db_max}</dd>
          <dt>Waits for a connection</dt><dd class={if db_waits > 0, "bad", ""}>{db_waits}</dd>
        </dl>
      </div>
    </div>
  </section>
</div>
