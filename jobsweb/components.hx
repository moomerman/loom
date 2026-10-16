import "github.com/moomerman/loom/jobsweb/ui"

component Topbar(inner_block slot, actions slot)

<style>
  .topbar { display: flex; align-items: center; gap: token(space.3); min-height: 3rem; padding: token(space.2) token(space.6); border-bottom: 1px solid token(color.line); flex-wrap: wrap; }
  .crumbs { display: flex; align-items: center; gap: token(space.2); min-width: 0; font-weight: 500; color: token(color.on-page); }
  .actions { margin-left: auto; display: flex; align-items: center; gap: token(space.2); }
  @media (max-width: 48rem) { .topbar { padding: token(space.2) token(space.4); } }
</style>
<header class="topbar">
  <div class="crumbs">{render_slot(inner_block)}</div>
  <div class="actions">{render_slot(actions)}</div>
</header>

component StateBadge(state string)

<style>
  .state { display: inline-flex; align-items: center; gap: token(space.1-5); height: 1.375rem; padding: 0 token(space.2); border: 1px solid token(color.line); border-radius: token(radius.full); background: token(color.surface); color: token(color.on-surface); font-size: token(text.sm); font-weight: 500; white-space: nowrap; }
  .dot { width: 0.5rem; height: 0.5rem; border-radius: token(radius.full); background: token(color.state-cancelled); }
  .available .dot { background: token(color.state-available); }
  .executing .dot { background: token(color.state-executing); animation: pulse token(duration.pulse) ease-in-out infinite; }
  .scheduled .dot { background: token(color.state-scheduled); }
  .retryable .dot { background: token(color.state-retryable); }
  .completed .dot { background: token(color.state-completed); }
  .discarded .dot { background: token(color.state-discarded); }
  .cancelled .dot { background: token(color.state-cancelled); }
  @keyframes pulse { 50% { opacity: 0.35; } }
  @media (prefers-reduced-motion: reduce) { .executing .dot { animation: none; } }
</style>
<span class={"state" state}><span class="dot" aria-hidden="true"></span>{state}</span>

component JobTable(jobs []JobRow, empty string)

<style>
  .jobs { border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); overflow-x: auto; }
  thead th { background: token(color.subtle); }
  tbody tr:hover td { background: token(color.hover); }
  .id { width: 1%; white-space: nowrap; color: token(color.muted); font-family: token(font.mono); font-size: token(text.code); }
  .type { font-weight: 500; color: token(color.on-surface); }
  .error { margin-top: token(space.1); max-width: 34rem; color: token(color.danger); font-size: token(text.sm); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .queue { display: inline-block; padding: 0 token(space.1-5); border-radius: token(radius.sm); background: token(color.subtle); color: token(color.on-subtle); font-family: token(font.mono); font-size: token(text.code); }
  .num { text-align: right; font-variant-numeric: tabular-nums; color: token(color.muted); white-space: nowrap; }
  .when { color: token(color.muted); white-space: nowrap; }
  .args code { display: block; max-width: 20rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: token(color.muted); }
  .empty-card { border: 1px dashed token(color.line-strong); border-radius: token(radius.lg); }
</style>

<div :if={len(jobs) == 0} class="empty-card"><.empty_state title={empty} /></div>
<div :if={len(jobs) > 0} class="jobs">
  <table>
    <thead>
      <tr><th>ID</th><th>Job</th><th>Queue</th><th>State</th><th class="num">Attempt</th><th>When</th><th>Args</th></tr>
    </thead>
    <tbody>
      <tr :for={j <- jobs} :key={j.id}>
        <td class="id">#{j.id}</td>
        <td>
          <a href={path("/jobs/" + j.id)} class="type">{j.type}</a>
          <div :if={j.error != ""} class="error" title={j.error}>{j.error}</div>
        </td>
        <td><span class="queue">{j.queue}</span></td>
        <td><.state_badge state={j.state} /></td>
        <td class="num">{j.attempt}/{j.max_attempts}</td>
        <td class="when">{j.when}</td>
        <td class="args"><code>{j.args}</code></td>
      </tr>
    </tbody>
  </table>
</div>

component ConfirmBar(prompt string)

<style>
  .confirm { display: flex; align-items: center; gap: token(space.3); flex-wrap: wrap; margin-bottom: token(space.5); padding: token(space.3) token(space.3) token(space.3) token(space.4); border: 1px solid token(color.line-strong); border-radius: token(radius.lg); background: token(color.warning); color: token(color.on-warning); box-shadow: token(shadow.card); }
  .mark { display: grid; place-items: center; width: 1.25rem; height: 1.25rem; flex: none; border-radius: token(radius.full); background: token(color.on-warning); color: token(color.warning); font-size: token(text.sm); font-weight: 700; }
  .prompt { flex: 1; font-weight: 500; }
  .buttons { display: flex; gap: token(space.2); }
</style>

<div :if={prompt != ""} class="confirm" role="alert">
  <span class="mark" aria-hidden="true">!</span>
  <span class="prompt">{prompt}</span>
  <span class="buttons">
    <.button label="Never mind" variant="secondary" size="sm" on:click="dismiss" />
    <.button label="Yes, do it" variant="danger" size="sm" on:click="confirm" />
  </span>
</div>

component Flash(message string)

<style>
  .flash { display: flex; align-items: center; gap: token(space.2); margin-bottom: token(space.5); padding: token(space.2) token(space.3); border-radius: token(radius.lg); background: token(color.accent-soft); color: token(color.on-accent-soft); font-weight: 500; }
  .mark { display: grid; place-items: center; width: 1.125rem; height: 1.125rem; border-radius: token(radius.full); background: token(color.accent); color: token(color.on-accent); font-size: token(text.xs); }
</style>

<div :if={message != ""} class="flash" role="status">
  <span class="mark" aria-hidden="true">i</span>
  <span>{message}</span>
</div>
