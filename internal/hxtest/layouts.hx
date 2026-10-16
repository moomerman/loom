component Root(title string, app_name string, inner_block slot)

<style>
  body { margin: 0; font-family: token(font.body); }
  :global(a) { color: token(color.accent); }
</style>
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <.live_title default={app_name} suffix=" · Loom">{title}</.live_title>
  <.live_meta />
  <.loom_styles />
</head>
<body>
  {render_slot(inner_block)}
  <.loom_scripts />
</body>
</html>

component Shell(user User, inner_block slot)

<nav><span class="user">{user.name}</span></nav>
<main>{render_slot(inner_block)}</main>

component AdminShell(user User, inner_block slot)

<.shell user={user}>
  <aside>Admin</aside>
  {render_slot(inner_block)}
</.shell>

component Chrome(count int, toast string, inner_block slot)

<header><button class="bump" on:click="bump">{count}</button><span :if={toast != ""} class="toast" on:click="dismiss">{toast}</span></header>
<main>{render_slot(inner_block)}</main>
