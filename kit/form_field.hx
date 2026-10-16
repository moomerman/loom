component FormField(label string @preview("Name"), hint string = "", error string = "" @preview("Required"), inner_block slot @preview("<input name=\"name\">"))

<style>
  .field { display: grid; gap: token(space.1); }
  .label { font-size: token(text.sm); font-weight: 500; }
  .hint { font-size: token(text.sm); color: token(color.muted); }
  .error { font-size: token(text.sm); color: token(color.danger); }
  .field:has(.loom-pending) { cursor: progress; }
  .field:has(.loom-pending) .label { color: token(color.muted); }
</style>
<label class="field">
  <span class="label">{label}</span>
  {render_slot(inner_block)}
  <span class="hint" :if={hint != ""}>{hint}</span>
  <span class="error" role="alert" :if={error != ""}>{error}</span>
</label>
