behavior dialog() emits (close) requires [role=dialog]

component Dialog(open bool, title string @preview("Delete this todo?"), on:close event, inner_block slot @preview("<p>This can't be undone.</p>"), actions slot @preview("<button type=\"button\">Delete</button>"))

<style>
  .backdrop { position: fixed; inset: 0; display: grid; place-items: center; background: token(color.backdrop); padding: token(space.4); }
  .dialog:focus { outline: none; }
  .dialog { background: token(color.surface); color: token(color.on-surface); border-radius: token(radius.lg); box-shadow: token(shadow.lg); padding: token(space.6); width: 100%; max-width: 32rem; }
  header { display: flex; justify-content: space-between; align-items: center; gap: token(space.4); margin-bottom: token(space.4); }
  h2 { margin: 0; font-size: token(text.lg); }
  .close { border: 0; background: none; color: inherit; font-size: token(text.lg); cursor: pointer; border-radius: token(radius.sm); }
  .close:focus-visible { outline: 2px solid token(color.focus); }
  .actions { display: flex; justify-content: flex-end; gap: token(space.2); margin-top: token(space.6); }
</style>
<div class="backdrop" :if={open}>
  <div class="dialog" role="dialog" aria-modal="true" aria-label={title} tabindex="-1" use:dialog on:close={on_close}>
    <header>
      <h2>{title}</h2>
      <button class="close" type="button" aria-label="Close" on:click={on_close}>×</button>
    </header>
    {render_slot(inner_block)}
    <div class="actions">{render_slot(actions)}</div>
  </div>
</div>
