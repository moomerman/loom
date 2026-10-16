import "github.com/moomerman/loom/examples/todo/components"

component Root(title string, inner_block slot)

<style>
  :global(body) { font-family: token(font.body); font-size: token(text.md); background: token(color.page); color: token(color.on-surface); margin: 0; }
</style>

<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <.live_title default="Todos">{title}</.live_title>
  <.live_meta />
  <.loom_styles />
</head>
<body>
  {render_slot(inner_block)}
  <.loom_scripts />
</body>
</html>

component App(user User, inner_block slot)

<style>
  .topnav { display: flex; gap: token(space.2); align-items: center; padding: token(space.3) token(space.6); background: token(color.accent); color: token(color.on-accent); }
  .user { margin-left: auto; opacity: 0.8; }
</style>

<nav class="topnav">
  <.link href="/" label="Todos" variant="nav" />
  <.link href="/about" label="About" variant="nav" />
  <span class="user">{user.name}</span>
</nav>
<main>{render_slot(inner_block)}</main>

component About()

<style>
  .about { max-width: 32rem; margin: token(space.12) auto; }
</style>

<section class="about">
  <h1>About</h1>
  <p>This page is outside the app layout's group, so it has the root layout only.</p>
  <p><.link href="/" label="Back to the todos" /></p>
</section>
