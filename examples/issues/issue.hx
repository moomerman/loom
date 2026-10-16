import "github.com/moomerman/loom/examples/issues/ui"

component IssueDetail(found bool, issue IssueView, menu string)

<style>
  .crumbs { display: flex; align-items: center; gap: token(space.2); }
  .crumbs a { display: flex; align-items: center; gap: token(space.2); color: token(color.muted); }
  .crumbs a:hover { color: token(color.on-page); }
  .sep { color: token(color.faint); }
  .page { display: flex; flex-direction: column; min-height: 100%; }
  .layout { flex: 1; display: grid; grid-template-columns: minmax(0, 1fr) 17rem; }
  .body { padding: token(space.8) token(space.10) token(space.12); max-width: 52rem; width: 100%; margin: 0 auto; }
  .title { width: 100%; margin: 0 0 token(space.2); padding: token(space.1) 0; border: 0; background: none; color: token(color.on-page); font: inherit; font-size: token(text.title); font-weight: 600; letter-spacing: -0.01em; outline: none; }
  .desc { width: 100%; min-height: 8rem; padding: 0; border: 0; background: none; color: token(color.on-page); font: inherit; font-size: token(text.lg); line-height: 1.65; resize: none; field-sizing: content; outline: none; }
  .title::placeholder, .desc::placeholder { color: token(color.faint); }
  h2 { margin: token(space.10) 0 token(space.4); padding-top: token(space.6); border-top: 1px solid token(color.line); font-size: token(text.md); font-weight: 600; }
  .side { padding: token(space.4) token(space.3); border-left: 1px solid token(color.line); background: token(color.page); }
  .prop { display: grid; grid-template-columns: 5.5rem 1fr; align-items: center; min-height: 2.25rem; }
  .key { padding-left: token(space.2); color: token(color.muted); font-size: token(text.sm); }
  .value { display: flex; align-items: center; gap: token(space.2); padding: 0 token(space.2); }
  .chips { display: flex; flex-wrap: wrap; gap: token(space.1); }
  .divider { height: 1px; margin: token(space.3) 0; background: token(color.line); }
  .muted { color: token(color.muted); font-size: token(text.sm); }
  .missing { padding: token(space.12) token(space.6); }
  @media (max-width: 60rem) { .layout { grid-template-columns: 1fr; } .side { border-left: 0; border-top: 1px solid token(color.line); } .body { padding: token(space.6) token(space.4); } }
</style>
<div :if={!found} class="missing">
  <.empty_state title="Issue not found">
    <p>It doesn't exist, or it belongs to a team you're not in. <a href="/">Back to your issues</a></p>
  </.empty_state>
</div>
<div :if={found} class="page">
  <.topbar>
    <nav class="crumbs" aria-label="Breadcrumb">
      <a href={"/team/" + issue.team.key}><.team_icon team={issue.team} />{issue.team.name}</a>
      <span class="sep">›</span>
      <span>{issue.identifier}</span>
    </nav>
  </.topbar>
  <div class="layout">
    <article class="body">
      <input class="title" value={issue.title} aria-label="Title" autocomplete="off" on:change="set" val-field="title" />
      <textarea class="desc" aria-label="Description" placeholder="Add a description…" rows="6" on:change="set" val-field="description">{issue.description}</textarea>
      <h2>Activity</h2>
      <.timeline items={issue.timeline} />
    </article>
    <aside class="side" aria-label="Properties">
      <div class="prop">
        <span class="key">Status</span>
        <.menu label="Status" plain open={menu == "status"} value={issue.status.value} options={issue.status_options} on:toggle="menu" on:select="pick" on:close="menu_close" val-menu="status">
          <:trigger><.option_icon option={issue.status} />{issue.status.label}</:trigger>
        </.menu>
      </div>
      <div class="prop">
        <span class="key">Priority</span>
        <.menu label="Priority" plain open={menu == "priority"} value={issue.priority.value} options={issue.priority_options} on:toggle="menu" on:select="pick" on:close="menu_close" val-menu="priority">
          <:trigger><.option_icon option={issue.priority} />{issue.priority.label}</:trigger>
        </.menu>
      </div>
      <div class="prop">
        <span class="key">Assignee</span>
        <.menu label="Assignee" plain open={menu == "assignee"} value={issue.assignee.value} options={issue.assignee_options} on:toggle="menu" on:select="pick" on:close="menu_close" val-menu="assignee">
          <:trigger><.option_icon option={issue.assignee} />{issue.assignee.label}</:trigger>
        </.menu>
      </div>
      <div class="prop">
        <span class="key">Labels</span>
        <.menu label="Labels" plain open={menu == "labels"} value="" options={issue.label_options} on:toggle="menu" on:select="pick" on:close="menu_close" val-menu="labels">
          <:trigger>
            <span class="chips">
              <.label_chip :for={l <- issue.labels} :key={l.id} label={l} />
              <span :if={len(issue.labels) == 0} class="muted">Add label</span>
            </span>
          </:trigger>
        </.menu>
      </div>
      <div class="divider"></div>
      <div class="prop">
        <span class="key">Created by</span>
        <span class="value"><.avatar user={issue.creator} />{issue.creator.name}</span>
      </div>
      <div class="prop">
        <span class="key">Created</span>
        <span class="value">{issue.created}</span>
      </div>
      <div class="prop">
        <span class="key">Updated</span>
        <span class="value">{issue.updated}</span>
      </div>
    </aside>
  </div>
</div>

component Timeline(items []TimelineItem)

<style>
  ol { list-style: none; margin: 0; padding: 0; display: grid; gap: token(space.1); }
  .event { display: flex; align-items: center; gap: token(space.2); padding: token(space.1) token(space.2); color: token(color.muted); font-size: token(text.sm); }
  .event strong { color: token(color.on-page); font-weight: 500; }
  .comment { margin: token(space.2) 0; padding: token(space.3) token(space.4); border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.sm); }
  .who { display: flex; align-items: center; gap: token(space.2); margin-bottom: token(space.1-5); font-size: token(text.sm); }
  .who strong { font-weight: 600; }
  .when { color: token(color.muted); }
  .text { margin: 0; white-space: pre-wrap; font-size: token(text.md); line-height: 1.6; }
  .reply { margin-top: token(space.4); border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); padding: token(space.3) token(space.4); }
  .reply:focus-within { border-color: token(color.line-strong); box-shadow: token(shadow.md); }
  .reply textarea { width: 100%; min-height: 3.5rem; border: 0; padding: 0; background: none; color: inherit; font: inherit; font-size: token(text.md); resize: none; field-sizing: content; outline: none; }
  .reply textarea::placeholder { color: token(color.faint); }
  .send { display: flex; justify-content: flex-end; align-items: center; gap: token(space.3); }
  .hint { color: token(color.faint); font-size: token(text.sm); }
</style>
<ol>
  <li :for={e <- items} :key={e.key}>
    <div :if={e.kind == "comment"} class="comment">
      <div class="who"><.avatar user={e.actor} size="md" /><strong>{e.actor.name}</strong><span class="when">{e.when}</span></div>
      <p class="text">{e.body}</p>
    </div>
    <div :if={e.kind != "comment"} class="event">
      <.avatar user={e.actor} />
      <span><strong>{e.actor.name}</strong> {e.text}</span>
      <span>·</span>
      <span>{e.when}</span>
    </div>
  </li>
</ol>
<form class="reply" on:submit="comment">
  <textarea name="body" aria-label="Comment" placeholder="Leave a comment…" rows="2"></textarea>
  <div class="send">
    <span class="hint"><kbd>⌘</kbd> <kbd>Enter</kbd></span>
    <.button label="Comment" type="submit" size="sm" variant="secondary" />
  </div>
</form>
