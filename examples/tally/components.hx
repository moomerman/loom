behavior toast(id int) emits (dismiss)

component Avatar(person Person, size: :sm | :md | :lg | :xl = :md)

<style>
  .avatar { display: inline-grid; place-items: center; flex: none; border-radius: token(radius.full); color: token(color.on-accent); font-weight: 600; letter-spacing: 0.02em; user-select: none; }
  .sm { width: 1.375rem; height: 1.375rem; font-size: token(text.2xs); letter-spacing: 0; }
  .md { width: 1.875rem; height: 1.875rem; font-size: token(text.xs); }
  .lg { width: 2.5rem; height: 2.5rem; font-size: token(text.md); }
  .xl { width: 3.5rem; height: 3.5rem; font-size: token(text.lg); }
  .red { background: token(color.red); }
  .orange { background: token(color.orange); }
  .amber { background: token(color.amber); }
  .green { background: token(color.green); }
  .teal { background: token(color.teal); }
  .sky { background: token(color.sky); }
  .indigo { background: token(color.indigo); }
  .violet { background: token(color.violet); }
  .pink { background: token(color.pink); }
  .slate { background: token(color.slate); }
</style>
<span class={"avatar" size person.color} title={person.name} aria-hidden="true">{person.initials}</span>

component AvatarStack(people []Person, large bool = false)

<style>
  .stack { display: inline-flex; align-items: center; }
  .item { margin-left: calc(-1 * token(space.1-5)); border-radius: token(radius.full); border: 2px solid token(color.surface); display: inline-flex; }
  .item:first-child { margin-left: 0; }
</style>
<span class="stack">
  <span class="item" :for={p <- people} :key={p.id}><.avatar :if={!large} person={p} size="sm" /><.avatar :if={large} person={p} size="md" /></span>
</span>

component Money(amount Amount, size: :sm | :md | :lg | :xl = :md)

<style>
  .money { font-variant-numeric: tabular-nums; font-weight: 600; white-space: nowrap; }
  .owed { color: token(color.owed); }
  .owe { color: token(color.owe); }
  .settled { color: token(color.muted); font-weight: 500; }
  .sm { font-size: token(text.sm); }
  .md { font-size: token(text.md); }
  .lg { font-size: token(text.lg); }
  .xl { font-size: token(text.3xl); font-family: token(font.display); font-weight: 500; letter-spacing: -0.01em; }
</style>
<span class={"money" amount.tone size}>{if amount.tone == "settled", "—", amount.text}</span>

component CategoryIcon(category Category, size: :md | :lg = :md)

<style>
  .icon { display: inline-grid; place-items: center; flex: none; border-radius: token(radius.md); }
  .md { width: 2.25rem; height: 2.25rem; font-size: token(text.lg); }
  .lg { width: 3.5rem; height: 3.5rem; font-size: token(text.2xl); border-radius: token(radius.lg); }
  .red { background: color-mix(in srgb, token(color.red) 16%, transparent); }
  .orange { background: color-mix(in srgb, token(color.orange) 16%, transparent); }
  .amber { background: color-mix(in srgb, token(color.amber) 18%, transparent); }
  .green { background: color-mix(in srgb, token(color.green) 16%, transparent); }
  .teal { background: color-mix(in srgb, token(color.teal) 16%, transparent); }
  .sky { background: color-mix(in srgb, token(color.sky) 16%, transparent); }
  .indigo { background: color-mix(in srgb, token(color.indigo) 16%, transparent); }
  .violet { background: color-mix(in srgb, token(color.violet) 16%, transparent); }
  .pink { background: color-mix(in srgb, token(color.pink) 16%, transparent); }
  .slate { background: color-mix(in srgb, token(color.slate) 18%, transparent); }
</style>
<span class={"icon" size category.color} title={category.label} aria-hidden="true">{category.icon}</span>

component GroupTile(emoji string, color string, size: :sm | :md | :lg = :md)

<style>
  .tile { display: inline-grid; place-items: center; flex: none; border-radius: token(radius.md); }
  .sm { width: 1.5rem; height: 1.5rem; font-size: token(text.sm); border-radius: token(radius.sm); }
  .md { width: 2.5rem; height: 2.5rem; font-size: token(text.lg); }
  .lg { width: 3.5rem; height: 3.5rem; font-size: token(text.2xl); border-radius: token(radius.lg); }
  .red { background: color-mix(in srgb, token(color.red) 20%, transparent); }
  .orange { background: color-mix(in srgb, token(color.orange) 20%, transparent); }
  .amber { background: color-mix(in srgb, token(color.amber) 22%, transparent); }
  .green { background: color-mix(in srgb, token(color.green) 20%, transparent); }
  .teal { background: color-mix(in srgb, token(color.teal) 20%, transparent); }
  .sky { background: color-mix(in srgb, token(color.sky) 20%, transparent); }
  .indigo { background: color-mix(in srgb, token(color.indigo) 20%, transparent); }
  .violet { background: color-mix(in srgb, token(color.violet) 20%, transparent); }
  .pink { background: color-mix(in srgb, token(color.pink) 20%, transparent); }
  .slate { background: color-mix(in srgb, token(color.slate) 22%, transparent); }
</style>
<span class={"tile" size color} aria-hidden="true">{emoji}</span>

component Panel(title string = "", inner_block slot, action slot)

<style>
  .panel { background: token(color.surface); color: token(color.on-surface); border: 1px solid token(color.line); border-radius: token(radius.lg); box-shadow: token(shadow.card); }
  header { display: flex; align-items: center; justify-content: space-between; gap: token(space.3); padding: token(space.4) token(space.5) 0; }
  h2 { margin: 0; font-size: token(text.sm); font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase; color: token(color.muted); }
  .body { padding: token(space.4) token(space.5) token(space.5); }
</style>
<section class="panel">
  <header :if={title != ""}><h2>{title}</h2>{render_slot(action)}</header>
  <div class="body">{render_slot(inner_block)}</div>
</section>

component ToastBar(toast Toast)

<style>
  .toast { position: fixed; left: 50%; bottom: token(space.6); transform: translateX(-50%); display: flex; align-items: center; gap: token(space.3); padding: token(space.2) token(space.2) token(space.2) token(space.4); border-radius: token(radius.full); background: token(color.on-page); color: token(color.page); box-shadow: token(shadow.lg); animation: rise token(duration.fast) ease-out; z-index: 20; font-weight: 500; }
  .dot { width: 0.5rem; height: 0.5rem; border-radius: token(radius.full); background: token(color.owed); }
  .info .dot { background: token(color.amber); }
  button { border: 0; background: none; color: inherit; opacity: 0.7; cursor: pointer; border-radius: token(radius.full); padding: 0 token(space.2); font-size: token(text.lg); }
  button:hover { opacity: 1; }
  @keyframes rise { from { transform: translate(-50%, 8px); opacity: 0; } }
</style>
<div class={"toast" toast.tone} role="status" :if={toast.text != ""} use:toast={id: toast.id} on:dismiss="toast_close">
  <span class="dot" aria-hidden="true"></span>
  <span>{toast.text}</span>
  <button type="button" aria-label="Dismiss" on:click="toast_close">×</button>
</div>

component EmptyNote(title string, inner_block slot)

<style>
  .empty { padding: token(space.10) token(space.6); text-align: center; color: token(color.muted); }
  strong { display: block; color: token(color.on-surface); font-size: token(text.lg); margin-bottom: token(space.1); font-family: token(font.display); font-weight: 500; }
</style>
<div class="empty"><strong>{title}</strong>{render_slot(inner_block)}</div>
