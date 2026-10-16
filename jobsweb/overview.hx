import "github.com/moomerman/loom/jobsweb/ui"

component Overview(queues []QueueRow, nodes []NodeRow, node string, failures []JobRow, running int, available int, scheduled int, retryable int, discarded int, done int, read_only bool, flash string, confirm string)

<style>
  .content { padding: token(space.6); max-width: 76rem; }
  .stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr)); gap: token(space.3); margin-bottom: token(space.8); }
  .stat { display: grid; gap: token(space.1); padding: token(space.4); border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); color: token(color.on-surface); }
  a.stat:hover { border-color: token(color.line-strong); text-decoration: none; }
  .label { display: flex; align-items: center; gap: token(space.1-5); color: token(color.muted); font-size: token(text.sm); font-weight: 500; }
  .dot { width: 0.5rem; height: 0.5rem; border-radius: token(radius.full); }
  .executing { background: token(color.state-executing); }
  .available { background: token(color.state-available); }
  .scheduled { background: token(color.state-scheduled); }
  .retryable { background: token(color.state-retryable); }
  .discarded { background: token(color.state-discarded); }
  .completed { background: token(color.state-completed); }
  .value { font-size: token(text.stat); font-weight: 600; letter-spacing: -0.02em; line-height: 1.2; font-variant-numeric: tabular-nums; }
  .alarm { border-color: token(color.danger); background: token(color.danger-soft); color: token(color.on-danger-soft); }
  .alarm .label { color: token(color.on-danger-soft); }
  .hint { color: token(color.faint); font-size: token(text.sm); overflow-wrap: anywhere; }
  .alarm .hint { color: token(color.on-danger-soft); }
  section { margin-bottom: token(space.8); }
  .heading { display: flex; align-items: baseline; gap: token(space.1) token(space.3); flex-wrap: wrap; margin-bottom: token(space.3); }
  h2 { font-size: token(text.lg); font-weight: 600; }
  .aside { color: token(color.muted); font-size: token(text.sm); }
  .push { margin-left: auto; }
  .queues { border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); overflow-x: auto; }
  thead th { background: token(color.subtle); }
  .name { font-weight: 600; color: token(color.on-surface); }
  .num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .zero { color: token(color.faint); }
  .bad { color: token(color.danger); font-weight: 600; }
  .load { display: flex; align-items: center; gap: token(space.2); min-width: 9rem; }
  progress { appearance: none; flex: 1; height: 0.375rem; border: 0; border-radius: token(radius.full); background: token(color.subtle); overflow: hidden; }
  progress::-webkit-progress-bar { background: token(color.subtle); border-radius: token(radius.full); }
  progress::-webkit-progress-value { background: token(color.accent); border-radius: token(radius.full); }
  progress::-moz-progress-bar { background: token(color.accent); border-radius: token(radius.full); }
  .of { color: token(color.muted); font-size: token(text.sm); font-variant-numeric: tabular-nums; white-space: nowrap; }
  .node { display: flex; align-items: center; gap: token(space.2); flex-wrap: wrap; }
  .health { display: inline-flex; align-items: center; gap: token(space.1-5); font-weight: 500; white-space: nowrap; }
  .health .dot { background: token(color.state-completed); }
  .health.stale { color: token(color.danger); }
  .health.stale .dot { background: token(color.state-discarded); }
  .workers { display: flex; gap: token(space.1) token(space.3); flex-wrap: wrap; }
  .worker { white-space: nowrap; font-variant-numeric: tabular-nums; }
  .worker code { color: token(color.muted); }
  .when { color: token(color.muted); white-space: nowrap; }
  .empty-card { border: 1px dashed token(color.line-strong); border-radius: token(radius.lg); }
  @media (max-width: 48rem) { .content { padding: token(space.4); } }
</style>

<.topbar>Overview</.topbar>
<div class="content">
  <.flash message={flash} />
  <.confirm_bar prompt={confirm} />
  <div class="stats">
    <div class="stat">
      <span class="label"><span class="dot executing" aria-hidden="true"></span>Running</span>
      <span class="value">{running}</span>
      <span class="hint">executing now</span>
    </div>
    <div class="stat">
      <span class="label"><span class="dot available" aria-hidden="true"></span>Available</span>
      <span class="value">{available}</span>
      <span class="hint">waiting for a worker</span>
    </div>
    <div class="stat">
      <span class="label"><span class="dot scheduled" aria-hidden="true"></span>Scheduled</span>
      <span class="value">{scheduled}</span>
      <span class="hint">due later</span>
    </div>
    <div class="stat">
      <span class="label"><span class="dot retryable" aria-hidden="true"></span>Retrying</span>
      <span class="value">{retryable}</span>
      <span class="hint">failed, will retry</span>
    </div>
    <a href={path("/jobs?state=discarded")} class={"stat" if discarded > 0, "alarm"}>
      <span class="label"><span class="dot discarded" aria-hidden="true"></span>Failed</span>
      <span class="value">{discarded}</span>
      <span class="hint">{if discarded > 0, "out of attempts", "none discarded"}</span>
    </a>
    <div class="stat">
      <span class="label"><span class="dot completed" aria-hidden="true"></span>Done</span>
      <span class="value">{done}</span>
      <span class="hint">{node}, last minute</span>
    </div>
  </div>

  <section>
    <div class="heading">
      <h2>Queues</h2>
      <span class="aside">Throughput and error rate cover the last minute on {node}.</span>
    </div>
    <div class="queues">
      <table>
        <thead>
          <tr>
            <th>Queue</th><th class="num">Available</th><th class="num">Scheduled</th><th class="num">Retryable</th><th class="num">Oldest wait</th>
            <th>Running</th><th class="num">Done/min</th><th class="num">Failed/min</th><th class="num">Error rate</th>
          </tr>
        </thead>
        <tbody>
          <tr :for={q <- queues} :key={q.name}>
            <td><a href={path("/jobs?queue=" + q.name)} class="name">{q.name}</a></td>
            <td class={"num" if q.available == 0, "zero"}>{q.available}</td>
            <td class={"num" if q.scheduled == 0, "zero"}>{q.scheduled}</td>
            <td class={"num" if q.retryable == 0, "zero"}>{q.retryable}</td>
            <td class={"num" if q.waiting == "–", "zero" if q.stuck, "bad"}>{q.waiting}</td>
            <td class="running">
              <span class="load">
                <progress value={q.running} max={q.concurrency} aria-label={q.name + " workers busy"}>{q.running}</progress>
                <span class="of">{q.running} of {q.concurrency}</span>
              </span>
            </td>
            <td class={"num" "done" if q.completed == 0, "zero"}>{q.completed}</td>
            <td class={"num" "failed" if q.failed == 0, "zero" if q.failed > 0, "bad"}>{q.failed}</td>
            <td class={"num" if q.failed > 0, "bad" if q.failed == 0, "zero"}>{q.error_rate}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>

  <section>
    <div class="heading">
      <h2>Nodes</h2>
      <span class="aside">Each node reports every few seconds. One that stops goes stale when its lease ends, and its jobs are rescued.</span>
    </div>
    <div :if={len(nodes) == 0} class="empty-card"><.empty_state title="No node is running jobs." /></div>
    <div :if={len(nodes) > 0} class="queues">
      <table>
        <thead>
          <tr><th>Node</th><th>State</th><th>Workers busy</th><th>Started</th><th>Last seen</th></tr>
        </thead>
        <tbody>
          <tr :for={n <- nodes} :key={n.name} class={"node-row" n.state}>
            <td>
              <span class="node">
                <span class="name">{n.name}</span>
                <.badge :if={n.leader} label="leader" tone="accent" />
                <span :if={n.self} class="aside">serving this page</span>
              </span>
            </td>
            <td><span class={"health" if n.state == "stale", "stale"}><span class="dot" aria-hidden="true"></span>{n.state}</span></td>
            <td>
              <span class="workers">
                <span :for={q <- n.queues} :key={q.name} class="worker"><code>{q.name}</code> {q.running}/{q.concurrency}</span>
              </span>
            </td>
            <td class="when">{n.started}</td>
            <td class="when">{n.seen}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>

  <section>
    <div class="heading">
      <h2>Recent failures</h2>
      <a :if={discarded > 0} href={path("/jobs?state=discarded")} class="aside">{discarded} discarded</a>
      <span class="push"><.button :if={!read_only && discarded > 0} label="Retry all discarded" variant="secondary" size="sm" on:click="retry_discarded" /></span>
    </div>
    <.job_table jobs={failures} empty="No failed jobs. Everything is running smoothly." />
  </section>
</div>
