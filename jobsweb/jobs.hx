import "github.com/moomerman/loom/jobsweb/ui"

component JobList(queues []Option, states []Option, types []Option, state string, all_states string, search string, can_search bool, jobs []JobRow, newer bool, older bool, flash string)

<style>
  .content { padding: token(space.5) token(space.6) token(space.6); max-width: 90rem; }
  .filters { display: flex; align-items: center; gap: token(space.3); flex-wrap: wrap; margin-bottom: token(space.4); }
  .tabs { display: flex; gap: token(space.1); flex-wrap: wrap; padding: token(space.1); border-radius: token(radius.lg); background: token(color.subtle); }
  .tab { display: inline-flex; align-items: center; gap: token(space.1-5); height: 1.75rem; padding: 0 token(space.3); border: 1px solid transparent; border-radius: token(radius.md); background: none; color: token(color.muted); font: inherit; font-size: token(text.sm); font-weight: 500; text-transform: capitalize; text-decoration: none; }
  .tab:hover { color: token(color.on-page); text-decoration: none; }
  .tab:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .current { background: token(color.surface); border-color: token(color.line); color: token(color.on-page); box-shadow: token(shadow.sm); }
  .dot { width: 0.4375rem; height: 0.4375rem; border-radius: token(radius.full); background: token(color.state-cancelled); }
  .available .dot { background: token(color.state-available); }
  .executing .dot { background: token(color.state-executing); }
  .scheduled .dot { background: token(color.state-scheduled); }
  .retryable .dot { background: token(color.state-retryable); }
  .completed .dot { background: token(color.state-completed); }
  .discarded .dot { background: token(color.state-discarded); }
  .selects { display: flex; gap: token(space.2); margin-left: auto; }
  .search { width: 16rem; height: 1.75rem; padding: 0 token(space.2); border: 1px solid token(color.line); border-radius: token(radius.md); background: token(color.surface); color: token(color.on-surface); font: inherit; font-size: token(text.sm); }
  .search:focus { outline: 2px solid token(color.focus); outline-offset: -1px; border-color: transparent; }
  .search::placeholder { color: token(color.faint); }
  .pager { display: flex; justify-content: flex-end; gap: token(space.2); margin-top: token(space.4); }
  @media (max-width: 48rem) { .content { padding: token(space.4); } .selects { margin-left: 0; } .search { width: 10rem; } }
</style>

<.topbar>
  Jobs
  <:actions>
    <input :if={can_search} class="search" type="search" name="search" placeholder="Search args…" aria-label="Search args" value={search} autocomplete="off" on:change="filter" val-field="search" />
  </:actions>
</.topbar>
<div class="content">
  <.flash message={flash} />
  <div class="filters">
    <nav class="tabs" aria-label="State">
      <a href={all_states} class={"tab" if state == "", "current"} aria-current={if state == "", "page", "false"}>All</a>
      <a :for={o <- states} :key={o.value} href={o.href} class={"tab" o.value if o.selected, "current"} aria-current={if o.selected, "page", "false"}><span class="dot" aria-hidden="true"></span>{o.label}</a>
    </nav>
    <div class="selects">
      <.select name="queue" label="Queue" hide_label={true} on:change="filter" val-field="queue">
        <option value="">All queues</option>
        <option :for={o <- queues} :key={o.value} value={o.value} selected={o.selected}>{o.label}</option>
      </.select>
      <.select name="type" label="Type" hide_label={true} on:change="filter" val-field="type">
        <option value="">All types</option>
        <option :for={o <- types} :key={o.value} value={o.value} selected={o.selected}>{o.label}</option>
      </.select>
    </div>
  </div>
  <.job_table jobs={jobs} empty="No jobs match these filters." />
  <div :if={newer || older} class="pager">
    <.button :if={newer} label="Newest" variant="secondary" size="sm" on:click="newest" />
    <.button :if={newer} label="← Newer" variant="secondary" size="sm" on:click="newer" />
    <.button :if={older} label="Older →" variant="secondary" size="sm" on:click="older" />
  </div>
</div>
