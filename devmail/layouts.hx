component Root(title string, inner_block slot)

<style>
  :global(*) { box-sizing: border-box; }
  :global(html), :global(body) { height: 100%; }
  :global(body) { margin: 0; background: token(color.page); color: token(color.on-page); font-family: token(font.body); font-size: token(text.md); line-height: 1.5; -webkit-font-smoothing: antialiased; }
  :global(a) { color: token(color.accent); text-decoration: none; }
  :global(a:hover) { text-decoration: underline; text-underline-offset: 2px; }
  :global(code), :global(pre) { font-family: token(font.mono); font-size: token(text.code); }
  :global(pre) { margin: 0; background: token(color.code); color: token(color.on-code); border: 1px solid token(color.line); border-radius: token(radius.md); padding: token(space.3) token(space.4); overflow: auto; line-height: 1.6; }
  :global(h1), :global(h2) { margin: 0; }
  :global(table) { width: 100%; border-collapse: collapse; }
  :global(th), :global(td) { text-align: left; padding: token(space.2) token(space.3); border-bottom: 1px solid token(color.line); vertical-align: middle; }
  :global(tbody tr:last-child td) { border-bottom: 0; }
  :global(th) { color: token(color.muted); font-weight: 500; font-size: token(text.sm); white-space: nowrap; }
  :global(::selection) { background: token(color.selected); }
  :global(#loom-root) { height: 100%; }
</style>

<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <.live_title default="Mail" suffix=" · Mail">{title}</.live_title>
  <.live_meta />
  <link rel="icon" href={asset("icon.svg")} />
  <.loom_styles />
</head>
<body>
  {render_slot(inner_block)}
  <.loom_scripts />
</body>
</html>
