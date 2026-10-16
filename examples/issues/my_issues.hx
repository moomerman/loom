import "github.com/moomerman/loom/examples/issues/ui"

component MyIssues(view string, tabs []Tab, groups []StatusGroup, count int)

<style>
  h1 { margin: 0; font-size: token(text.md); font-weight: 600; }
  .tabs { display: flex; gap: token(space.1); margin-left: token(space.3); }
  .count { color: token(color.muted); font-size: token(text.sm); }
</style>
<.topbar>
  <h1>My issues</h1>
  <div class="tabs" role="tablist" aria-label="View">
    <.view_tab :for={t <- tabs} :key={t.label} label={t.label} href={t.href} selected={t.selected} />
  </div>
  <:actions><span class="count">{count} {if count == 1, "issue", "issues"}</span></:actions>
</.topbar>
<.issue_list groups={groups} />
<.empty_state :if={count == 0 && view == "assigned"} title="Nothing assigned to you">
  <p>Issues assigned to you in any of your teams show up here. Press <kbd>C</kbd> to create one.</p>
</.empty_state>
<.empty_state :if={count == 0 && view == "created"} title="You haven't created any issues">
  <p>Press <kbd>C</kbd> to create one.</p>
</.empty_state>
