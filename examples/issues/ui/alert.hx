component Alert(tone: :info | :success | :warning | :danger = :info, title string = "" @preview("Saved"), inner_block slot @preview("Your changes are live."))

<style>
  .alert { padding: token(space.3) token(space.4); border-radius: token(radius.md); border: 1px solid token(color.line); }
  .info { background: token(color.subtle); color: token(color.on-subtle); }
  .success { background: token(color.success); color: token(color.on-success); }
  .warning { background: token(color.warning); color: token(color.on-warning); }
  .danger { background: token(color.danger); color: token(color.on-danger); }
  strong { display: block; margin-bottom: token(space.1); }
</style>
<div class={"alert" tone} role={if tone == "warning" || tone == "danger", "alert", "status"}>
  <strong :if={title != ""}>{title}</strong>
  {render_slot(inner_block)}
</div>
