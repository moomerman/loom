behavior dropdown(open bool, value string) emits (select(value string), close) requires [role=listbox]

component Dropdown(label string @preview("Fruit"), open bool, value string = "", on:toggle event, on:select event, on:close event, inner_block slot @preview("<li role=\"option\" data-value=\"apple\">Apple</li><li role=\"option\" data-value=\"pear\">Pear</li>"))

<style>
  .dropdown { position: relative; display: inline-block; }
  .trigger { font: inherit; cursor: pointer; padding: token(space.2) token(space.3); border: 1px solid token(color.line); border-radius: token(radius.md); background: token(color.surface); color: token(color.on-surface); }
  .trigger:focus-visible { outline: 2px solid token(color.focus); }
  .menu { position: absolute; z-index: 10; min-width: 100%; margin: token(space.1) 0 0; padding: token(space.1); list-style: none; background: token(color.surface); color: token(color.on-surface); border: 1px solid token(color.line); border-radius: token(radius.md); box-shadow: token(shadow.lg); }
</style>
<div class="dropdown">
  <button class="trigger" type="button" aria-haspopup="listbox" aria-expanded={if open, "true", "false"} on:click={on_toggle}>{label}</button>
  <ul class="menu" role="listbox" aria-label={label} hidden={!open} use:dropdown={open: open, value: value} on:select={on_select} on:close={on_close}>{render_slot(inner_block)}</ul>
</div>

component DropdownOption(value string @preview("pear"), label string @preview("Pear"))

<style>
  .option { padding: token(space.2) token(space.3); border-radius: token(radius.sm); cursor: pointer; }
  .option:hover, .option:focus { background: token(color.subtle); outline: none; }
  .option[aria-selected="true"] { font-weight: 600; }
</style>
<li class="option" role="option" data-value={value}>{label}</li>
