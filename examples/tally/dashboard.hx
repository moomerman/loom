import "github.com/moomerman/loom/examples/tally/components"

component Dashboard(me Person, greeting string, today string, total Amount, owe string, owed string, groups []GroupCard, activity []ActivityRow, creating bool, form NewGroupForm, toast Toast)

<style>
  .page { max-width: 72rem; margin: 0 auto; padding: token(space.10) token(space.10) token(space.16); display: grid; gap: token(space.8); }
  .top { display: flex; align-items: flex-end; justify-content: space-between; gap: token(space.4); }
  .date { margin: 0; color: token(color.muted); font-size: token(text.sm); font-weight: 500; }
  h1 { margin: 0; font-family: token(font.display); font-size: token(text.3xl); font-weight: 500; letter-spacing: -0.02em; line-height: 1.15; }
  .summary { display: grid; grid-template-columns: 1.4fr 1fr 1fr; gap: token(space.4); }
  .stat { padding: token(space.5) token(space.6); border-radius: token(radius.xl); background: token(color.surface); border: 1px solid token(color.line); box-shadow: token(shadow.card); display: grid; gap: token(space.1); align-content: start; }
  .stat.hero { background: token(color.accent-soft); border-color: transparent; }
  .stat-label { font-size: token(text.sm); color: token(color.muted); font-weight: 500; }
  .hero .stat-label { color: token(color.on-accent-soft); }
  .stat-value { font-family: token(font.display); font-size: token(text.2xl); font-weight: 500; font-variant-numeric: tabular-nums; }
  .owe { color: token(color.owe); }
  .owed { color: token(color.owed); }
  .section-head { display: flex; align-items: baseline; justify-content: space-between; margin-bottom: token(space.3); }
  h2 { margin: 0; font-size: token(text.lg); font-weight: 600; }
  .count { color: token(color.muted); font-size: token(text.sm); }
  .groups { display: grid; grid-template-columns: repeat(auto-fill, minmax(15rem, 1fr)); gap: token(space.4); }
  .group { display: grid; gap: token(space.4); padding: token(space.5); border-radius: token(radius.xl); background: token(color.surface); border: 1px solid token(color.line); box-shadow: token(shadow.card); transition: transform token(duration.fast), border-color token(duration.fast); }
  .group:hover { transform: translateY(-2px); border-color: token(color.line-strong); }
  .group:focus-visible { outline: 2px solid token(color.focus); outline-offset: 2px; }
  .group-top { display: flex; align-items: center; gap: token(space.3); }
  .group-name { flex: 1; min-width: 0; display: grid; line-height: 1.3; }
  .group-name strong { font-size: token(text.md); font-weight: 600; }
  .group-name span { font-size: token(text.sm); color: token(color.muted); }
  .group-foot { display: flex; align-items: flex-end; justify-content: space-between; gap: token(space.2); }
  .group-balance { display: grid; justify-items: end; line-height: 1.2; }
  .group-balance span { font-size: token(text.xs); color: token(color.muted); }
  .new { display: grid; place-items: center; align-content: center; gap: token(space.2); min-height: 9rem; padding: token(space.5); border: 1.5px dashed token(color.line-strong); border-radius: token(radius.xl); background: none; color: token(color.muted); cursor: pointer; font-weight: 500; }
  .new:hover { border-color: token(color.accent); color: token(color.accent); }
  .new:focus-visible { outline: 2px solid token(color.focus); outline-offset: 2px; }
  .plus { font-size: token(text.2xl); line-height: 1; }
  .feed { list-style: none; margin: 0; padding: 0; background: token(color.surface); border: 1px solid token(color.line); border-radius: token(radius.xl); box-shadow: token(shadow.card); overflow: hidden; }
  .feed li + li { border-top: 1px solid token(color.line); }
  .entry { display: flex; align-items: center; gap: token(space.3); padding: token(space.3) token(space.5); }
  .entry:hover { background: token(color.hover); }
  .what { flex: 1; min-width: 0; display: grid; line-height: 1.35; }
  .what strong { font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .what span { font-size: token(text.sm); color: token(color.muted); }
  .mine { display: grid; justify-items: end; line-height: 1.3; }
  .mine span { font-size: token(text.xs); color: token(color.muted); }
  .emojis { display: flex; flex-wrap: wrap; gap: token(space.2); border: 0; padding: 0; margin: 0; }
  legend { font-size: token(text.sm); font-weight: 500; margin-bottom: token(space.2); padding: 0; }
  .emoji input { position: absolute; opacity: 0; pointer-events: none; }
  .emoji span { display: grid; place-items: center; width: 2.5rem; height: 2.5rem; border-radius: token(radius.md); border: 1px solid token(color.line); font-size: token(text.lg); cursor: pointer; }
  .emoji input:checked + span { border-color: token(color.accent); background: token(color.accent-soft); box-shadow: token(shadow.sm); }
  .emoji input:focus-visible + span { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .fields { display: grid; gap: token(space.5); }
  .people { display: grid; grid-template-columns: 1fr 1fr; gap: token(space.2); border: 0; padding: 0; margin: 0; }
  .who { display: flex; align-items: center; gap: token(space.2); padding: token(space.2); border: 1px solid token(color.line); border-radius: token(radius.md); cursor: pointer; font-size: token(text.sm); }
  .who:has(input:checked) { border-color: token(color.accent); background: token(color.accent-soft); }
  .actions { display: flex; justify-content: flex-end; gap: token(space.2); }
  .fields.loom-pending .actions { opacity: 0.6; pointer-events: none; }
  .error { margin: 0; color: token(color.danger); font-size: token(text.sm); }
  @media (max-width: 60rem) { .summary { grid-template-columns: 1fr; } .page { padding: token(space.6) token(space.4); } }
</style>
<div class="page">
  <header class="top">
    <div>
      <p class="date">{today}</p>
      <h1>{greeting}</h1>
    </div>
    <.button label="New group" on:click="new_group" />
  </header>

  <section class="summary" aria-label="Your balance">
    <div class="stat hero">
      <span class="stat-label">{if total.tone == "owe", "Overall, you owe", if total.tone == "owed", "Overall, you are owed", "Overall"}</span>
      <.money amount={total} size="xl" />
      <span class="stat-label" :if={total.tone == "settled"}>You're all settled up. Nice.</span>
    </div>
    <div class="stat">
      <span class="stat-label">You owe</span>
      <span class="stat-value owe">{owe}</span>
    </div>
    <div class="stat">
      <span class="stat-label">You are owed</span>
      <span class="stat-value owed">{owed}</span>
    </div>
  </section>

  <section aria-labelledby="groups-heading">
    <div class="section-head"><h2 id="groups-heading">Your groups</h2><span class="count">{len(groups)} groups</span></div>
    <div class="groups">
      <a :for={g <- groups} :key={g.id} class="group" href={g.href}>
        <div class="group-top">
          <.group_tile emoji={g.emoji} color={g.color} />
          <span class="group-name"><strong>{g.name}</strong><span>{g.expenses} expenses</span></span>
        </div>
        <div class="group-foot">
          <.avatar_stack people={g.members} />
          <span class="group-balance"><.money amount={g.balance} size="lg" /><span>{g.balance.label}</span></span>
        </div>
      </a>
      <button class="new" type="button" on:click="new_group"><span class="plus" aria-hidden="true">+</span>Start a group</button>
    </div>
  </section>

  <section aria-labelledby="activity-heading">
    <div class="section-head"><h2 id="activity-heading">Recent activity</h2><a class="count" href="/activity">See all →</a></div>
    <ul class="feed" :if={len(activity) > 0}>
      <li :for={a <- activity} :key={a.key}>
        <a class="entry" href={a.href}>
          <.avatar person={a.actor} />
          <span class="what"><strong>{a.text}</strong><span>{a.group} · {a.when}</span></span>
          <span class="mine"><span>{a.mine.label}</span><.money amount={a.mine} size="sm" /></span>
        </a>
      </li>
    </ul>
    <.empty_note :if={len(activity) == 0} title="Nothing yet">Start a group and add your first expense.</.empty_note>
  </section>

  <.dialog open={creating} title="Start a group" on:close="close_group">
    <form id="new-group" class="fields" on:change="group_form" on:submit="create_group">
      <.input name="name" label="Name" value={form.name} placeholder="Flatmates, Ski trip, Book club…" autofocus />
      <fieldset class="emojis">
        <legend>Icon</legend>
        <label class="emoji" :for={e <- form.emojis} :key={e.value}>
          <input type="radio" name="emoji" value={e.value} checked={e.selected} aria-label={e.value} />
          <span>{e.label}</span>
        </label>
      </fieldset>
      <fieldset class="people">
        <legend>Who's in it with you</legend>
        <label class="who" :for={m <- form.members} :key={m.person.id}>
          <input type="checkbox" name={"member_" + m.person.id} checked={m.checked} />
          <.avatar person={m.person} size="sm" />
          {m.person.name}
        </label>
      </fieldset>
      <p class="error" role="alert" :if={form.error != ""}>{form.error}</p>
      <div class="actions">
        <.button label="Cancel" variant="ghost" on:click="close_group" />
        <.button label="Create group" type="submit" />
      </div>
    </form>
  </.dialog>
  <.toast_bar toast={toast} />
</div>
