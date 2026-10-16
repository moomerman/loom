import "github.com/moomerman/loom/jobsweb/ui"

component Cron(entries []CronRow, read_only bool, flash string)

<style>
  .content { padding: token(space.6); max-width: 76rem; }
  .intro { margin: 0 0 token(space.4); color: token(color.muted); }
  .cron { border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); overflow-x: auto; }
  thead th { background: token(color.subtle); }
  .expr { display: inline-block; padding: token(space.px) token(space.2); border-radius: token(radius.sm); background: token(color.subtle); color: token(color.on-subtle); font-family: token(font.mono); font-size: token(text.code); white-space: pre; }
  .type { font-weight: 500; }
  .args { display: block; max-width: 22rem; margin-top: token(space.1); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: token(color.muted); }
  .soon { font-weight: 500; white-space: nowrap; }
  .at { display: block; color: token(color.muted); font-size: token(text.sm); white-space: nowrap; }
  .last { display: flex; align-items: center; gap: token(space.2); white-space: nowrap; }
  .empty { color: token(color.faint); }
  .run { text-align: right; }
  @media (max-width: 48rem) { .content { padding: token(space.4); } }
</style>

<.topbar>Cron</.topbar>
<div class="content">
  <.flash message={flash} />
  <p class="intro">Scheduled jobs run once per cluster. Run one now to test it without waiting.</p>
  <.empty_state :if={len(entries) == 0} title="No cron entries." />
  <div :if={len(entries) > 0} class="cron">
    <table>
      <thead>
        <tr><th>Schedule</th><th>Job</th><th>Next run</th><th>Last run</th><th><span :if={!read_only}>Action</span></th></tr>
      </thead>
      <tbody>
        <tr :for={e <- entries} :key={e.index}>
          <td><code class="expr">{e.expr}</code></td>
          <td>
            <span class="type">{e.type}</span> <span :if={e.builtin} class="builtin"><.badge label="built in" /></span>
            <code class="args">{e.args}</code>
          </td>
          <td><span class="soon">{e.next_in}</span><span class="at">{e.next_run}</span></td>
          <td>
            <span class="last">
              <span :if={e.last_tick == ""} class="empty">not yet</span>
              <a :if={e.last_job != ""} href={path("/jobs/" + e.last_job)}>{e.last_tick}</a>
              <.state_badge :if={e.last_state != ""} state={e.last_state} />
            </span>
          </td>
          <td class="run"><.button :if={!read_only} label="Run now" variant="secondary" size="sm" on:click="run_now" val-index={e.index} /></td>
        </tr>
      </tbody>
    </table>
  </div>
</div>
