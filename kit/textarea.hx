component Textarea(field field, name string = "" @preview("notes"), label string @preview("Notes"), value string = "", rows int = 3, placeholder string = "", hint string = "", on:change event)

<style>
  .field { display: grid; gap: token(space.1); }
  .label { font-size: token(text.sm); font-weight: 500; }
  textarea { font: inherit; padding: token(space.2) token(space.3); border: 1px solid token(color.line); border-radius: token(radius.md); background: token(color.surface); color: token(color.on-surface); resize: vertical; }
  textarea:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .invalid { border-color: token(color.danger); }
  .hint { font-size: token(text.sm); color: token(color.muted); }
  .error { font-size: token(text.sm); color: token(color.danger); }
</style>
<label class="field">
  <span class="label">{label}</span>
  <textarea class={if field.error != "", "invalid"} name={if field.name != "", field.name, name} rows={rows} placeholder={placeholder} aria-invalid={if field.error != "", "true", "false"} on:change={on_change}>{if field.name != "", field.value, value}</textarea>
  <span class="hint" :if={hint != ""}>{hint}</span>
  <span class="error" :if={field.error != ""}>{field.error}</span>
</label>
