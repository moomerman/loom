import "github.com/moomerman/loom/examples/tally/components"

component Activity(notifications []Notification, unread int, activity []ActivityRow)

<style>
  .page { max-width: 72rem; margin: 0 auto; padding: token(space.10) token(space.10) token(space.16); }
  .top { display: flex; align-items: flex-end; justify-content: space-between; gap: token(space.4); margin-bottom: token(space.8); }
  h1 { margin: 0; font-family: token(font.display); font-size: token(text.3xl); font-weight: 500; letter-spacing: -0.02em; line-height: 1.15; }
  .sub { margin: token(space.1) 0 0; color: token(color.muted); }
  .cols { display: grid; grid-template-columns: 1fr 1fr; gap: token(space.8); align-items: start; }
  h2 { margin: 0 0 token(space.3); font-size: token(text.lg); font-weight: 600; }
  ul { list-style: none; margin: 0; padding: 0; background: token(color.surface); border: 1px solid token(color.line); border-radius: token(radius.xl); box-shadow: token(shadow.card); overflow: hidden; }
  li + li { border-top: 1px solid token(color.line); }
  .note { display: flex; gap: token(space.3); padding: token(space.3) token(space.5); align-items: flex-start; }
  .note:hover { background: token(color.hover); }
  .dot { flex: none; width: 0.5rem; height: 0.5rem; margin-top: token(space.2); border-radius: token(radius.full); background: transparent; }
  .unread { background: token(color.accent-soft); }
  .unread .dot { background: token(color.accent); }
  .text { display: grid; line-height: 1.4; }
  .text span { font-size: token(text.sm); color: token(color.muted); }
  .entry { display: flex; align-items: center; gap: token(space.3); padding: token(space.3) token(space.5); }
  .entry:hover { background: token(color.hover); }
  .what { flex: 1; min-width: 0; display: grid; line-height: 1.35; }
  .what strong { font-weight: 500; }
  .what span { font-size: token(text.sm); color: token(color.muted); }
  .mine { display: grid; justify-items: end; line-height: 1.3; }
  .mine span { font-size: token(text.xs); color: token(color.muted); }
  @media (max-width: 60rem) { .cols { grid-template-columns: 1fr; } .page { padding: token(space.6) token(space.4); } }
</style>
<div class="page">
  <header class="top">
    <div>
      <h1>Activity</h1>
      <p class="sub">{if unread == 0, "You're all caught up.", unread_text(unread)}</p>
    </div>
    <.button :if={unread > 0} label="Mark all read" variant="secondary" on:click="mark_read" optimistic:disabled />
  </header>
  <div class="cols">
    <section aria-labelledby="notes-heading">
      <h2 id="notes-heading">For you</h2>
      <ul :if={len(notifications) > 0}>
        <li :for={n <- notifications} :key={n.id}>
          <a href={n.href} class={"note" if !n.read, "unread"}>
            <span class="dot" aria-hidden="true"></span>
            <span class="text">{n.body}<span>{n.group} · {n.when}</span></span>
          </a>
        </li>
      </ul>
      <.empty_note :if={len(notifications) == 0} title="No notifications">When friends add expenses or remind you, you'll hear about it here.</.empty_note>
    </section>
    <section aria-labelledby="feed-heading">
      <h2 id="feed-heading">In your groups</h2>
      <ul>
        <li :for={a <- activity} :key={a.key}>
          <a class="entry" href={a.href}>
            <.avatar person={a.actor} />
            <span class="what"><strong>{a.text}</strong><span>{a.group} · {a.when}</span></span>
            <span class="mine"><span>{a.mine.label}</span><.money amount={a.mine} size="sm" /></span>
          </a>
        </li>
      </ul>
    </section>
  </div>
</div>
