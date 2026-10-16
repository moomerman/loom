component Input(field field, name string = "" @preview("email"), label string @preview("Email"), value string = "", type: :text | :email | :password | :search | :number | :url | :date = :text, placeholder string = "", hint string = "", error string = "" @preview("Enter an email address"), required bool = false, autofocus bool = false, hide_label bool = false, on:change event)

<style>
  .field { display: grid; gap: token(space.1); }
  .label { font-size: token(text.sm); font-weight: 500; }
  .hidden { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  .input { font: inherit; padding: token(space.2) token(space.3); border: 1px solid token(color.line); border-radius: token(radius.md); background: token(color.surface); color: token(color.on-surface); }
  .input:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .invalid { border-color: token(color.danger); }
  .hint { font-size: token(text.sm); color: token(color.muted); }
  .error { font-size: token(text.sm); color: token(color.danger); }
</style>
<label class="field">
  <span class={"label" if hide_label, "hidden"}>{label}</span>
  <input class={"input" if field.error != "" || error != "", "invalid"} type={type} name={if field.name != "", field.name, name} value={if field.name != "", field.value, value} placeholder={placeholder} required={required} autofocus={autofocus} aria-invalid={if field.error != "" || error != "", "true", "false"} on:change={on_change} />
  <span class="hint" :if={hint != ""}>{hint}</span>
  <span class="error" :if={field.error != "" || error != ""}>{if field.name != "", field.error, error}</span>
</label>
