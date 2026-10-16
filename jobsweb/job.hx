import "github.com/moomerman/loom/jobsweb/ui"

component JobDetail(id string, found bool, job JobRow, args string, inserted string, scheduled string, attempted string, finished string, wait string, run string, attempts []AttemptRow, history []HistoryRow, can_retry bool, can_cancel bool, can_delete bool, read_only bool, flash string, confirm string)

<style>
  .crumb { color: token(color.muted); }
  .sep { color: token(color.faint); }
  .content { padding: token(space.6); max-width: 76rem; }
  .missing { padding: token(space.12) token(space.6); text-align: center; color: token(color.muted); }
  .heading { display: flex; align-items: center; gap: token(space.3); flex-wrap: wrap; margin-bottom: token(space.1); }
  h1 { font-size: token(text.title); font-weight: 600; letter-spacing: -0.01em; overflow-wrap: anywhere; }
  .sub { margin: 0 0 token(space.6); color: token(color.muted); }
  .grid { display: grid; grid-template-columns: minmax(0, 1fr) 17rem; gap: token(space.8); align-items: start; }
  section { margin-bottom: token(space.8); }
  h2 { margin-bottom: token(space.3); font-size: token(text.lg); font-weight: 600; }
  .count { margin-left: token(space.1); color: token(color.faint); font-weight: 500; }
  .attempt { margin-bottom: token(space.3); border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); overflow: hidden; }
  .attempt-head { display: flex; align-items: center; gap: token(space.2); flex-wrap: wrap; padding: token(space.2) token(space.4); border-bottom: 1px solid token(color.line); background: token(color.subtle); font-size: token(text.sm); color: token(color.muted); }
  .attempt-head strong { color: token(color.on-surface); }
  .code { padding: 0 token(space.1-5); border-radius: token(radius.sm); background: token(color.danger-soft); color: token(color.on-danger-soft); font-family: token(font.mono); font-size: token(text.code); }
  .message { margin: 0; padding: token(space.3) token(space.4); color: token(color.danger); font-family: token(font.mono); font-size: token(text.code); white-space: pre-wrap; overflow-wrap: anywhere; }
  details { border-top: 1px solid token(color.line); }
  summary { padding: token(space.2) token(space.4); color: token(color.muted); font-size: token(text.sm); cursor: pointer; }
  summary:hover { color: token(color.on-surface); }
  details pre { border: 0; border-top: 1px solid token(color.line); border-radius: 0; max-height: 24rem; }
  .history { margin: 0; padding: 0; list-style: none; border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); }
  .step { display: flex; align-items: center; gap: token(space.3); padding: token(space.2) token(space.4); border-bottom: 1px solid token(color.line); }
  .step:last-child { border-bottom: 0; }
  .note { color: token(color.on-surface); min-width: 0; overflow-wrap: anywhere; }
  .at { margin-left: auto; color: token(color.muted); font-size: token(text.sm); font-variant-numeric: tabular-nums; white-space: nowrap; }
  .props { position: sticky; top: token(space.6); border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); }
  .props h2 { margin: 0; padding: token(space.3) token(space.4); border-bottom: 1px solid token(color.line); font-size: token(text.md); }
  dl { display: grid; gap: token(space.3); margin: 0; padding: token(space.4); }
  dt { color: token(color.muted); font-size: token(text.sm); }
  dd { margin: 0; font-variant-numeric: tabular-nums; overflow-wrap: break-word; }
  .queue { padding: 0 token(space.1-5); border-radius: token(radius.sm); background: token(color.subtle); font-family: token(font.mono); font-size: token(text.code); }
  @media (max-width: 60rem) { .grid { grid-template-columns: 1fr; } .props { position: static; } }
  @media (max-width: 48rem) { .content { padding: token(space.4); } }
</style>

<.topbar>
  <a href={path("/jobs")} class="crumb">Jobs</a>
  <span class="sep" aria-hidden="true">›</span>
  <span>#{id}</span>
  <:actions>
    <span :if={found && !read_only} class="actions">
      <.button :if={can_retry} label="Retry" variant="secondary" size="sm" on:click="retry" />
      <.button :if={can_cancel} label="Cancel" variant="secondary" size="sm" on:click="cancel" />
      <.button :if={can_delete} label="Delete" variant="danger" size="sm" on:click="delete" />
    </span>
  </:actions>
</.topbar>
<div class="content">
  <.flash message={flash} />
  <.confirm_bar prompt={confirm} />
  <div :if={!found} class="missing">
    <.empty_state title={"Job " + id + " not found"}>
      <p>There is no job {id}. It may have been deleted, or pruned after finishing. <a href={path("/jobs")}>All jobs</a></p>
    </.empty_state>
  </div>
  <div :if={found}>
    <div class="heading">
      <h1>{job.type}</h1>
      <.state_badge state={job.state} />
    </div>
    <p class="sub">Job #{job.id} on the {job.queue} queue, attempt {job.attempt} of {job.max_attempts}.</p>
    <div class="grid">
      <div>
        <section :if={len(attempts) > 0}>
          <h2>Failed attempts <span class="count">{len(attempts)}</span></h2>
          <div :for={e <- attempts} :key={e.attempt} class="attempt">
            <div class="attempt-head">
              <strong>Attempt {e.attempt}</strong>
              <span>{e.at}</span>
              <span :if={e.code != ""} class="code">{e.code}</span>
            </div>
            <p class="message">{e.error}</p>
            <details :if={e.stack != ""}>
              <summary>Stack trace</summary>
              <pre>{e.stack}</pre>
            </details>
          </div>
        </section>
        <section>
          <h2>Args</h2>
          <pre>{args}</pre>
        </section>
        <section>
          <h2>History</h2>
          <ol class="history">
            <li :for={h <- history} :key={h.seq} class="step">
              <.state_badge state={h.state} />
              <span :if={h.note != ""} class="note">{h.note}</span>
              <span class="at">{h.at}</span>
            </li>
          </ol>
        </section>
      </div>
      <aside class="props">
        <h2>Details</h2>
        <dl>
          <div><dt>Queue</dt><dd><a href={path("/jobs?queue=" + job.queue)} class="queue">{job.queue}</a></dd></div>
          <div><dt>Attempt</dt><dd>{job.attempt} of {job.max_attempts}</dd></div>
          <div><dt>Inserted</dt><dd>{inserted}</dd></div>
          <div><dt>Scheduled</dt><dd>{scheduled}</dd></div>
          <div :if={attempted != ""}><dt>Latest attempt</dt><dd>{attempted}</dd></div>
          <div :if={finished != ""}><dt>Finished</dt><dd>{finished}</dd></div>
          <div :if={wait != ""}><dt>Waited</dt><dd>{wait}</dd></div>
          <div :if={run != ""}><dt>Ran for</dt><dd>{run}</dd></div>
        </dl>
      </aside>
    </div>
  </div>
</div>
