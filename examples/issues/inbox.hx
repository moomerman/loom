import "github.com/moomerman/loom/examples/issues/ui"

component Inbox(items []NotificationRow, unread int)

<style>
  h1 { margin: 0; font-size: token(text.md); font-weight: 600; }
  .read { border: 0; background: none; color: token(color.muted); font-size: token(text.sm); cursor: pointer; padding: token(space.1) token(space.2); border-radius: token(radius.sm); }
  .read:hover { color: token(color.on-page); background: token(color.hover); }
  ul { list-style: none; margin: 0; padding: 0; }
  .item { display: flex; align-items: center; gap: token(space.3); padding: token(space.3) token(space.6); border-bottom: 1px solid token(color.line); }
  .item:hover { background: token(color.hover); }
  .item:focus { outline: none; background: token(color.selected); }
  .what { flex: 1; min-width: 0; }
  .line { display: flex; align-items: center; gap: token(space.2); }
  .title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
  .unread .title { font-weight: 600; }
  .ident { color: token(color.muted); font-size: token(text.sm); flex: none; }
  .sub { color: token(color.muted); font-size: token(text.sm); }
  .when { color: token(color.muted); font-size: token(text.sm); flex: none; }
  .dot { width: 0.5rem; height: 0.5rem; flex: none; border-radius: token(radius.full); background: token(color.accent); }
  .seen { width: 0.5rem; flex: none; }
</style>
<.topbar>
  <h1>Inbox</h1>
  <:actions>
    <button :if={unread > 0} class="read" type="button" on:click="read_all">Mark all as read</button>
  </:actions>
</.topbar>
<ul>
  <li :for={n <- items} :key={n.id}>
    <a class={"item" if n.unread, "unread"} href={"/issue/" + n.identifier} data-row>
      <span :if={n.unread} class="dot" aria-label="Unread"></span>
      <span :if={!n.unread} class="seen"></span>
      <.avatar user={n.actor} size="lg" />
      <span class="what">
        <span class="line"><span class="ident">{n.identifier}</span><span class="title">{n.title}</span></span>
        <span class="sub">{n.actor.name} {if n.kind == "assigned", "assigned the issue to you", "commented on the issue"}</span>
      </span>
      <.status_icon status={n.status} />
      <span class="when">{n.when}</span>
    </a>
  </li>
</ul>
<.empty_state :if={len(items) == 0} title="You're all caught up">
  <p>When someone assigns you an issue or comments on yours, it lands here.</p>
</.empty_state>
