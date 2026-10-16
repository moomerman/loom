component Pill(label string, tone: :plain | :warm = :plain, on:click event)

<style>
  .pill { border-radius: token(radius.md); padding: 0 token(space.2); }
  .warm { background: token(color.accent); color: token(color.on-accent); }
</style>
<button class={"pill" tone} on:click={on_click}>{label}</button>
