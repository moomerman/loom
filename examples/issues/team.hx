import "github.com/moomerman/loom/examples/issues/ui"

component TeamIssues(found bool, team Team, tabs []Tab, search string, groups []StatusGroup, count int, filters Filters)

<style>
  h1 { display: flex; align-items: center; gap: token(space.2); margin: 0; font-size: token(text.md); font-weight: 600; }
  .tabs { display: flex; gap: token(space.1); margin-left: token(space.3); }
  .search { width: 14rem; height: 1.75rem; padding: 0 token(space.2); border: 1px solid token(color.line); border-radius: token(radius.md); background: token(color.surface); color: token(color.on-surface); font: inherit; font-size: token(text.sm); }
  .search:focus { outline: 2px solid token(color.focus); outline-offset: -1px; border-color: transparent; }
  .search::placeholder { color: token(color.faint); }
  .filters { display: flex; align-items: center; gap: token(space.2); height: 2.75rem; padding: 0 token(space.6); border-bottom: 1px solid token(color.line); }
  .count { margin-left: auto; color: token(color.muted); font-size: token(text.sm); }
  .clear { border: 0; background: none; color: token(color.muted); font-size: token(text.sm); cursor: pointer; padding: token(space.1) token(space.2); border-radius: token(radius.sm); }
  .clear:hover { color: token(color.on-page); background: token(color.hover); }
  .missing { padding: token(space.12) token(space.6); text-align: center; color: token(color.muted); }
  @media (max-width: 48rem) { .search { width: 9rem; } .filters { padding: 0 token(space.4); } }
</style>
<div :if={!found} class="missing">
  <.empty_state title="Team not found">
    <p>It doesn't exist, or you're not a member. <a href="/">Back to your issues</a></p>
  </.empty_state>
</div>
<div :if={found}>
  <.topbar>
    <h1><.team_icon team={team} />{team.name}</h1>
    <div class="tabs" role="tablist" aria-label="View">
      <.view_tab :for={t <- tabs} :key={t.label} label={t.label} href={t.href} selected={t.selected} />
    </div>
    <:actions>
      <input class="search" type="search" placeholder="Search…  /" aria-label="Search issues" value={search} autocomplete="off" data-hotkey="/" on:input="search" />
      <.button label="New issue" size="sm" on:click="compose" val-team={team.id} />
    </:actions>
  </.topbar>
  <div class="filters">
    <.menu label="Priority" open={filters.menu == "priority"} value={filters.priority.value} options={filters.priority_options} on:toggle="menu" on:select="pick" on:close="menu_close" val-menu="priority">
      <:trigger><.option_icon option={filters.priority} />{filters.priority.label}</:trigger>
    </.menu>
    <.menu label="Assignee" open={filters.menu == "assignee"} value={filters.assignee.value} options={filters.assignee_options} on:toggle="menu" on:select="pick" on:close="menu_close" val-menu="assignee">
      <:trigger><.option_icon option={filters.assignee} />{filters.assignee.label}</:trigger>
    </.menu>
    <.menu label="Label" open={filters.menu == "label"} value={filters.label.value} options={filters.label_options} on:toggle="menu" on:select="pick" on:close="menu_close" val-menu="label">
      <:trigger><.option_icon option={filters.label} />{filters.label.label}</:trigger>
    </.menu>
    <button :if={filters.active} class="clear" type="button" on:click="clear_filters">Clear filters</button>
    <span class="count">{count} {if count == 1, "issue", "issues"}</span>
  </div>
  <.issue_list groups={groups} />
  <.empty_state :if={count == 0 && !filters.active} title="No issues here yet">
    <p>Press <kbd>C</kbd> to create the first one.</p>
  </.empty_state>
  <.empty_state :if={count == 0 && filters.active} title="No issues match">
    <p>Try another filter, or clear them.</p>
  </.empty_state>
</div>
