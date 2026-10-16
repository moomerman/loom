behavior tabs() requires [role=tablist]

component Tabs(label string @preview("Show"), inner_block slot @preview("<button type=\"button\" role=\"tab\" aria-selected=\"true\">All</button><button type=\"button\" role=\"tab\" aria-selected=\"false\" tabindex=\"-1\">Done</button>"))

<style>
  .tabs { display: flex; gap: token(space.1); border-bottom: 1px solid token(color.line); }
</style>
<div class="tabs" role="tablist" aria-label={label} use:tabs>{render_slot(inner_block)}</div>

component Tab(label string @preview("All"), selected bool = false, on:click event)

<style>
  .tab { border: 0; border-bottom: 2px solid transparent; background: none; color: token(color.muted); font: inherit; padding: token(space.2) token(space.3); cursor: pointer; }
  .selected { color: inherit; border-bottom-color: token(color.accent); }
  .tab:focus-visible { outline: 2px solid token(color.focus); }
</style>
<button type="button" role="tab" class={"tab" if selected, "selected"} aria-selected={if selected, "true", "false"} tabindex={if selected, "0", "-1"} on:click={on_click}>{label}</button>
