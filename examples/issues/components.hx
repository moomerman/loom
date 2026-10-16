import "github.com/moomerman/loom/examples/issues/ui"

component StatusIcon(status string @preview("backlog", "todo", "in_progress", "in_review", "done", "canceled"))

<style>
  svg { display: block; flex: none; }
  .backlog { color: token(color.status-backlog); }
  .todo { color: token(color.status-todo); }
  .in_progress { color: token(color.status-progress); }
  .in_review { color: token(color.status-review); }
  .done { color: token(color.status-done); }
  .canceled { color: token(color.status-canceled); }
  .mark { stroke: token(color.page); }
</style>
<svg class={"status" status} viewBox="0 0 14 14" width="14" height="14" aria-hidden="true">
  <circle :if={status == "backlog"} cx="7" cy="7" r="6" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="1.4 1.74" />
  <circle :if={status == "todo"} cx="7" cy="7" r="6" fill="none" stroke="currentColor" stroke-width="1.5" />
  <g :if={status == "in_progress"}>
    <circle cx="7" cy="7" r="6" fill="none" stroke="currentColor" stroke-width="1.5" />
    <path d="M7 3.5a3.5 3.5 0 0 1 0 7z" fill="currentColor" />
  </g>
  <g :if={status == "in_review"}>
    <circle cx="7" cy="7" r="6" fill="none" stroke="currentColor" stroke-width="1.5" />
    <path d="M7 3.5a3.5 3.5 0 1 1-3.5 3.5H7z" fill="currentColor" />
  </g>
  <g :if={status == "done"}>
    <circle cx="7" cy="7" r="7" fill="currentColor" />
    <path class="mark" d="M4.2 7.3l1.9 1.9 3.8-4.1" fill="none" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
  </g>
  <g :if={status == "canceled"}>
    <circle cx="7" cy="7" r="7" fill="currentColor" />
    <path class="mark" d="M4.9 4.9l4.2 4.2M9.1 4.9l-4.2 4.2" fill="none" stroke-width="1.5" stroke-linecap="round" />
  </g>
</svg>

component PriorityIcon(priority int @preview(0, 1, 2, 3, 4))

<style>
  svg { display: block; flex: none; color: token(color.muted); }
  .urgent { color: token(color.urgent); }
  .bar { fill: currentColor; opacity: 0.28; }
  .on { opacity: 1; }
  .dash { fill: currentColor; opacity: 0.6; }
  .mark { fill: token(color.page); }
</style>
<svg class={"priority" if priority == 1, "urgent"} viewBox="0 0 14 14" width="14" height="14" aria-hidden="true">
  <g :if={priority == 0}>
    <rect class="dash" x="1.5" y="6.25" width="2.5" height="1.5" rx="0.5" />
    <rect class="dash" x="5.75" y="6.25" width="2.5" height="1.5" rx="0.5" />
    <rect class="dash" x="10" y="6.25" width="2.5" height="1.5" rx="0.5" />
  </g>
  <g :if={priority == 1}>
    <rect x="1" y="1" width="12" height="12" rx="3" fill="currentColor" />
    <rect class="mark" x="6.25" y="3.5" width="1.5" height="4.5" rx="0.5" />
    <rect class="mark" x="6.25" y="9.25" width="1.5" height="1.5" rx="0.5" />
  </g>
  <g :if={priority > 1}>
    <rect class="bar on" x="1.5" y="8" width="3" height="5" rx="1" />
    <rect class={"bar" if priority <= 3, "on"} x="5.5" y="5" width="3" height="8" rx="1" />
    <rect class={"bar" if priority <= 2, "on"} x="9.5" y="2" width="3" height="11" rx="1" />
  </g>
</svg>

component Avatar(user User, size: :sm | :md | :lg = :sm)

<style>
  .avatar { display: inline-grid; place-items: center; flex: none; border-radius: token(radius.full); color: token(color.on-accent); font-weight: 600; letter-spacing: 0.02em; user-select: none; }
  .none { display: block; flex: none; color: token(color.faint); }
  .sm { width: 1.125rem; height: 1.125rem; font-size: token(text.tiny); }
  .md { width: 1.5rem; height: 1.5rem; font-size: token(text.xs); }
  .lg { width: 2rem; height: 2rem; font-size: token(text.sm); }
  .violet { background: token(color.violet); }
  .teal { background: token(color.teal); }
  .amber { background: token(color.amber); }
  .rose { background: token(color.rose); }
  .sky { background: token(color.sky); }
  .green { background: token(color.green); }
  .indigo { background: token(color.indigo); }
  .orange { background: token(color.orange); }
  .pink { background: token(color.pink); }
  .red { background: token(color.red); }
  .slate { background: token(color.slate); }
</style>
<span :if={user.id != ""} class={"avatar" size user.color} role="img" aria-label={user.name} title={user.name}>{user.initials}</span>
<svg :if={user.id == ""} class={"none" size} viewBox="0 0 16 16" role="img" aria-label="Unassigned">
  <circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.2" stroke-dasharray="2.2 1.8" />
  <circle cx="8" cy="6.6" r="2" fill="currentColor" />
  <path d="M4.6 11.8a3.8 3.8 0 0 1 6.8 0" fill="none" stroke="currentColor" stroke-width="1.2" />
</svg>

component TeamIcon(team Team)

<style>
  .team { display: inline-grid; place-items: center; flex: none; width: 1.125rem; height: 1.125rem; border-radius: token(radius.sm); color: token(color.on-accent); font-size: token(text.xs); font-weight: 600; }
  .indigo { background: token(color.indigo); }
  .pink { background: token(color.pink); }
  .teal { background: token(color.teal); }
  .orange { background: token(color.orange); }
  .green { background: token(color.green); }
  .violet { background: token(color.violet); }
  .slate { background: token(color.slate); }
</style>
<span class={"team" team.color} aria-hidden="true">{initial(team.name)}</span>

component LabelChip(label Label)

<style>
  .chip { display: inline-flex; align-items: center; gap: token(space.1-5); height: 1.25rem; padding: 0 token(space.2); border: 1px solid token(color.line-strong); border-radius: token(radius.full); color: token(color.muted); font-size: token(text.sm); font-weight: 500; white-space: nowrap; }
</style>
<span class="chip"><.dot color={label.color} />{label.name}</span>

component Dot(color string)

<style>
  .dot { display: inline-block; flex: none; width: 0.5rem; height: 0.5rem; border-radius: token(radius.full); }
  .red { background: token(color.red); }
  .orange { background: token(color.orange); }
  .amber { background: token(color.amber); }
  .green { background: token(color.green); }
  .teal { background: token(color.teal); }
  .sky { background: token(color.sky); }
  .indigo { background: token(color.indigo); }
  .violet { background: token(color.violet); }
  .pink { background: token(color.pink); }
  .rose { background: token(color.rose); }
  .slate { background: token(color.slate); }
</style>
<span class={"dot" color} aria-hidden="true"></span>

component OptionIcon(option Option)

<.status_icon :if={option.kind == "status"} status={option.status} />
<.priority_icon :if={option.kind == "priority"} priority={option.priority} />
<.avatar :if={option.kind == "user"} user={option.user} />
<.dot :if={option.kind == "label"} color={option.color} />

component Menu(label string, open bool, value string, options []Option, plain bool = false, trigger slot, on:toggle event, on:select event, on:close event)

<style>
  .menu { position: relative; display: inline-block; }
  .trigger { display: inline-flex; align-items: center; gap: token(space.1-5); height: 1.75rem; padding: 0 token(space.2); border: 1px solid token(color.line); border-radius: token(radius.md); background: token(color.surface); color: token(color.on-surface); font-size: token(text.sm); font-weight: 500; cursor: pointer; white-space: nowrap; }
  .trigger:hover { background: token(color.hover); border-color: token(color.line-strong); }
  .trigger:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .plain { border-color: transparent; background: none; height: 2rem; font-size: token(text.md); }
  .plain:hover { border-color: transparent; }
  .options { position: absolute; z-index: 20; top: 100%; left: 0; min-width: 13rem; max-height: 20rem; overflow-y: auto; margin: token(space.1) 0 0; padding: token(space.1); list-style: none; background: token(color.surface); color: token(color.on-surface); border: 1px solid token(color.line); border-radius: token(radius.lg); box-shadow: token(shadow.lg); animation: pop token(duration.fast) ease-out; }
  .option { display: flex; align-items: center; gap: token(space.2); padding: token(space.1-5) token(space.2); border-radius: token(radius.md); cursor: pointer; font-size: token(text.md); }
  .option:hover, .option:focus { background: token(color.hover); outline: none; }
  .text { flex: 1; white-space: nowrap; }
  .check { color: token(color.accent); font-size: token(text.sm); }
  @keyframes pop { from { transform: translateY(-4px); opacity: 0; } }
</style>
<div class="menu">
  <button class={"trigger" if plain, "plain"} type="button" aria-haspopup="listbox" aria-expanded={if open, "true", "false"} aria-label={label} on:click={on_toggle}>{render_slot(trigger)}</button>
  <ul :if={open} class="options" role="listbox" aria-label={label} use:dropdown={open: open, value: value} on:select={on_select} on:close={on_close}>
    <li :for={o <- options} :key={o.value} class="option" role="option" data-value={o.value}>
      <.option_icon option={o} />
      <span class="text">{o.label}</span>
      <span :if={o.selected} class="check" aria-hidden="true">✓</span>
    </li>
  </ul>
</div>

component IssueList(groups []StatusGroup)

<style>
  .group { border-bottom: 1px solid token(color.line); }
  .head { position: sticky; top: 0; z-index: 1; display: flex; align-items: center; gap: token(space.2); height: 2.25rem; padding: 0 token(space.6); background: token(color.group); font-weight: 500; }
  .count { color: token(color.muted); font-variant-numeric: tabular-nums; }
  .add { margin-left: auto; display: grid; place-items: center; width: 1.5rem; height: 1.5rem; border: 0; border-radius: token(radius.sm); background: none; color: token(color.muted); cursor: pointer; font-size: token(text.lg); }
  .add:hover { background: token(color.hover); color: token(color.on-page); }
  .row { display: flex; align-items: center; gap: token(space.3); height: 2.625rem; padding: 0 token(space.6); border-top: 1px solid token(color.line); }
  .row:first-of-type { border-top: 0; }
  .row:hover { background: token(color.hover); }
  .row:focus { outline: none; background: token(color.selected); }
  .row:focus-visible { box-shadow: token(shadow.row-focus); }
  .ident { width: 4rem; flex: none; color: token(color.muted); font-size: token(text.sm); font-variant-numeric: tabular-nums; }
  .title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
  .labels { display: flex; gap: token(space.1); flex: none; }
  .date { width: 3.5rem; flex: none; text-align: right; color: token(color.muted); font-size: token(text.sm); }
  @media (max-width: 48rem) { .labels, .date { display: none; } .row, .head { padding: 0 token(space.4); } }
</style>
<div class="list">
  <section :for={g <- groups} :key={g.status} class="group" aria-label={g.name}>
    <header class="head">
      <.status_icon status={g.status} />
      <span>{g.name}</span>
      <span class="count">{g.count}</span>
      <button class="add" type="button" aria-label={"New issue in " + g.name} title="New issue" on:click="compose" val-status={g.status}>+</button>
    </header>
    <a :for={i <- g.issues} :key={i.id} class="row" href={i.href} data-row>
      <span title={priority_name(i.priority)}><.priority_icon priority={i.priority} /></span>
      <span class="ident">{i.identifier}</span>
      <span title={status_name(i.status)}><.status_icon status={i.status} /></span>
      <span class="title">{i.title}</span>
      <span class="labels"><.label_chip :for={l <- i.labels} :key={l.id} label={l} /></span>
      <span class="date">{i.created}</span>
      <.avatar user={i.assignee} />
    </a>
  </section>
</div>

component NewIssueDialog(c Composer)

<style>
  .backdrop { position: fixed; inset: 0; z-index: 30; display: flex; justify-content: center; align-items: flex-start; padding: token(space.dialog-top) token(space.4) token(space.4); background: token(color.backdrop); }
  .composer { width: 100%; max-width: 46rem; display: grid; gap: token(space.2); padding: token(space.4) token(space.5); border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); color: token(color.on-surface); box-shadow: token(shadow.lg); animation: pop token(duration.fast) ease-out; }
  .composer:focus { outline: none; }
  header { display: flex; align-items: center; gap: token(space.2); }
  .crumb { color: token(color.muted); }
  .close { margin-left: auto; border: 0; background: none; color: token(color.muted); font-size: token(text.xl); line-height: 1; cursor: pointer; border-radius: token(radius.sm); padding: 0 token(space.1); }
  .close:hover { color: token(color.on-surface); background: token(color.hover); }
  .title { border: 0; padding: token(space.2) 0 0; background: none; color: inherit; font: inherit; font-size: token(text.xl); font-weight: 600; outline: none; }
  .desc { border: 0; padding: 0; min-height: 5rem; background: none; color: inherit; font: inherit; font-size: token(text.lg); resize: none; outline: none; }
  .title::placeholder, .desc::placeholder { color: token(color.faint); }
  .chips { display: flex; flex-wrap: wrap; gap: token(space.1-5); }
  .error { margin: 0; color: token(color.danger); font-size: token(text.sm); }
  footer { display: flex; align-items: center; justify-content: flex-end; gap: token(space.3); margin-top: token(space.2); padding-top: token(space.3); border-top: 1px solid token(color.line); }
  .hint { margin-right: auto; color: token(color.faint); font-size: token(text.sm); }
  @keyframes pop { from { transform: translateY(-6px) scale(0.99); opacity: 0; } }
</style>
<div class="backdrop" :if={c.open}>
  <form class="composer" role="dialog" aria-modal="true" aria-label="New issue" tabindex="-1" use:dialog on:close="compose_close" on:submit="compose_submit">
    <header>
      <.menu label="Team" open={c.menu == "team"} value={c.team.value} options={c.team_options} on:toggle="compose_menu" on:select="compose_pick" on:close="compose_menu_close" val-menu="team">
        <:trigger><.option_icon option={c.team} />{c.team.label}</:trigger>
      </.menu>
      <span class="crumb">›&nbsp; New issue</span>
      <button class="close" type="button" aria-label="Close" on:click="compose_close">×</button>
    </header>
    <input class="title" name="title" value={c.title} placeholder="Issue title" aria-label="Issue title" autocomplete="off" autofocus on:change="compose_field" val-field="title" />
    <textarea class="desc" name="description" placeholder="Add description…" aria-label="Description" rows="4" on:change="compose_field" val-field="description">{c.description}</textarea>
    <div class="chips">
      <.menu label="Status" open={c.menu == "status"} value={c.status.value} options={c.status_options} on:toggle="compose_menu" on:select="compose_pick" on:close="compose_menu_close" val-menu="status">
        <:trigger><.option_icon option={c.status} />{c.status.label}</:trigger>
      </.menu>
      <.menu label="Priority" open={c.menu == "priority"} value={c.priority.value} options={c.priority_options} on:toggle="compose_menu" on:select="compose_pick" on:close="compose_menu_close" val-menu="priority">
        <:trigger><.option_icon option={c.priority} />{c.priority.label}</:trigger>
      </.menu>
      <.menu label="Assignee" open={c.menu == "assignee"} value={c.assignee.value} options={c.assignee_options} on:toggle="compose_menu" on:select="compose_pick" on:close="compose_menu_close" val-menu="assignee">
        <:trigger><.option_icon option={c.assignee} />{c.assignee.label}</:trigger>
      </.menu>
      <.menu label="Labels" open={c.menu == "labels"} value="" options={c.label_options} on:toggle="compose_menu" on:select="compose_pick" on:close="compose_menu_close" val-menu="labels">
        <:trigger>
          <.dot :for={l <- c.labels} :key={l.id} color={l.color} />
          {if len(c.labels) == 0, "Labels", c.labels_text}
        </:trigger>
      </.menu>
    </div>
    <p :if={c.error != ""} class="error" role="alert">{c.error}</p>
    <footer>
      <span class="hint"><kbd>⌘</kbd> <kbd>Enter</kbd> to create</span>
      <.button label="Create issue" type="submit" size="sm" />
    </footer>
  </form>
</div>

component Topbar(inner_block slot, actions slot)

<style>
  .topbar { display: flex; align-items: center; gap: token(space.3); height: 3rem; padding: 0 token(space.6); border-bottom: 1px solid token(color.line); }
  .crumbs { display: flex; align-items: center; gap: token(space.2); min-width: 0; font-weight: 500; }
  .actions { margin-left: auto; display: flex; align-items: center; gap: token(space.2); }
  @media (max-width: 48rem) { .topbar { padding: 0 token(space.4); } }
</style>
<header class="topbar">
  <div class="crumbs">{render_slot(inner_block)}</div>
  <div class="actions">{render_slot(actions)}</div>
</header>

component ViewTab(label string, href string, selected bool)

<style>
  .tab { display: inline-flex; align-items: center; height: 1.75rem; padding: 0 token(space.3); text-decoration: none; border: 1px solid transparent; border-radius: token(radius.md); background: none; color: token(color.muted); font-size: token(text.sm); font-weight: 500; cursor: pointer; }
  .tab:hover { color: token(color.on-page); background: token(color.hover); }
  .selected { color: token(color.on-page); background: token(color.subtle); border-color: token(color.line-strong); }
  .tab:focus-visible { outline: 2px solid token(color.focus); }
</style>
<a role="tab" href={href} class={"tab" if selected, "selected"} aria-selected={if selected, "true", "false"}>{label}</a>
