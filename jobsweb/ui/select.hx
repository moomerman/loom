component Select(name string @preview("size"), label string @preview("Size"), hint string = "", hide_label bool = false, on:change event, inner_block slot @preview("<option>Small</option><option>Large</option>"))

<style>
  .field { display: grid; gap: token(space.1); }
  .label { font-size: token(text.sm); font-weight: 500; }
  .hidden { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  .select { font: inherit; padding: token(space.2) token(space.3); border: 1px solid token(color.line); border-radius: token(radius.md); background: token(color.surface); color: token(color.on-surface); }
  .select:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .hint { font-size: token(text.sm); color: token(color.muted); }
</style>
<label class="field">
  <span class={"label" if hide_label, "hidden"}>{label}</span>
  <select class="select" name={name} on:change={on_change}>{render_slot(inner_block)}</select>
  <span class="hint" :if={hint != ""}>{hint}</span>
</label>
