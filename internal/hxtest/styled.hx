component Panel(title string @preview("Profile"), featured bool, inner_block slot)

<style>
  .panel { padding: token(space.2); border-radius: token(radius.md); background: token(color.surface); color: token(color.on-surface); }
  .panel.featured h2::before { content: "★ "; color: token(color.accent); }
  h2 { font-size: token(text.lg); margin: 0; }
  @media (min-width: 40rem) { .panel { padding: token(space.4); } }
  @keyframes pop { from { opacity: 0 } to { opacity: 1 } }
  .featured { animation: pop 0.2s; }
</style>
<article class={if featured, "panel featured", "panel"}>
  <h2>{title}</h2>
  {render_slot(inner_block)}
</article>

component PanelList(titles []string)

<style>
  ul { display: grid; gap: token(space.2); list-style: none; padding: 0; }
  p { margin: 0; }
</style>
<ul>
  <li :for={t <- titles}><.panel title={t} featured={t == "first"}><p>{t}</p></.panel></li>
</ul>
