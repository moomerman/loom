component EmptyState(title string @preview("No todos yet"), inner_block slot @preview("<p>Add one above.</p>"))

<style>
  .empty { text-align: center; padding: token(space.8) token(space.4); color: token(color.muted); }
  h2 { margin: 0 0 token(space.2); font-size: token(text.lg); color: token(color.on-surface); }
</style>
<div class="empty">
  <h2>{title}</h2>
  {render_slot(inner_block)}
</div>
