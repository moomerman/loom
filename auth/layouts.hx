component Card(app_name string, inner_block slot)

<style>
  .page { min-height: 100vh; display: grid; place-items: center; padding: token(space.6); background: token(color.subtle); }
  .card { width: 100%; max-width: 24rem; padding: token(space.8); border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); color: token(color.on-surface); box-shadow: token(shadow.sm); }
  .app { margin: 0 0 token(space.2); text-align: center; color: token(color.muted); font-size: token(text.sm); font-weight: 600; }
  .limited { margin: 0 0 token(space.4); padding: token(space.3) token(space.4); border-radius: token(radius.md); background: token(color.danger); color: token(color.on-danger); font-size: token(text.sm); }
</style>
<div class="page">
  <main class="card">
    <p class="app">{app_name}</p>
    <p class="limited" role="status" data-loom-limited hidden>Too many attempts. Wait a minute, then try again.</p>
    {render_slot(inner_block)}
  </main>
</div>
