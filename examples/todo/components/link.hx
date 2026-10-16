component Link(href string @preview("/"), label string @preview("Home"), variant: :inline | :nav = :inline, current bool = false)

<style>
  .link { color: token(color.accent); text-underline-offset: 2px; }
  .nav { color: inherit; text-decoration: none; padding: token(space.1) token(space.2); border-radius: token(radius.sm); }
  .nav:hover { background: token(color.subtle); color: token(color.on-subtle); }
  .current { font-weight: 600; text-decoration: underline; }
  .link:focus-visible { outline: 2px solid token(color.focus); outline-offset: 2px; }
</style>
<a href={href} class={"link" variant if current, "current"} aria-current={if current, "page", "false"}>{label}</a>
