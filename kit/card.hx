component Card(title string = "" @preview("Profile"), inner_block slot @preview("<p>Card content.</p>"))

<style>
  .card { background: token(color.surface); color: token(color.on-surface); border: 1px solid token(color.line); border-radius: token(radius.lg); padding: token(space.6); box-shadow: token(shadow.sm); }
  h2 { margin: 0 0 token(space.4); font-size: token(text.lg); }
</style>
<section class="card">
  <h2 :if={title != ""}>{title}</h2>
  {render_slot(inner_block)}
</section>
