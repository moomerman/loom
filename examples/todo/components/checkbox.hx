component Checkbox(label string @preview("Done"), name string = "", checked bool = false, disabled bool = false, on:change event)

<style>
  .checkbox { display: inline-flex; align-items: center; gap: token(space.2); cursor: pointer; }
  input { width: 1.1em; height: 1.1em; margin: 0; accent-color: token(color.accent); }
  input:focus-visible { outline: 2px solid token(color.focus); outline-offset: 2px; }
</style>
<label class="checkbox">
  <input type="checkbox" name={name} checked={checked} disabled={disabled} on:change={on_change} />
  <span>{label}</span>
</label>
