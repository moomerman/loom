component Button(label string @preview("Save"), variant: :primary | :secondary | :danger | :ghost = :primary, size: :sm | :md = :md, type: :button | :submit = :button, busy bool = false, disabled bool = false, on:click event)

<style>
  .button { display: inline-flex; align-items: center; justify-content: center; gap: token(space.2); border: 1px solid transparent; border-radius: token(radius.md); font: inherit; font-weight: 500; cursor: pointer; transition: background-color token(duration.fast), filter token(duration.fast); }
  .md { padding: token(space.2) token(space.4); font-size: token(text.md); }
  .sm { padding: token(space.1) token(space.2); font-size: token(text.sm); }
  .primary { background: token(color.accent); color: token(color.on-accent); }
  .secondary { background: token(color.subtle); color: token(color.on-subtle); border-color: token(color.line); }
  .danger { background: token(color.danger); color: token(color.on-danger); }
  .ghost { background: transparent; color: inherit; }
  .button:hover { filter: brightness(1.08); }
  .ghost:hover { background: token(color.subtle); }
  .button:focus-visible { outline: 2px solid token(color.focus); outline-offset: 2px; }
  .button:disabled { opacity: 0.6; cursor: not-allowed; }
  .busy { cursor: progress; }
  .button.loom-pending { cursor: progress; opacity: 0.8; }
</style>
<button type={type} class={"button" variant size if busy, "busy"} disabled={disabled || busy} on:click={on_click}>{label}</button>
