behavior menu()

component Menu(label string @preview("a.example"), aria_label string = "" @preview("Switch domain, now a.example"), inner_block slot @preview("<a href=\"/a\" class=\"item\">a.example</a><a href=\"/b\" class=\"item\">b.example</a>"))

<style>
  .menu { position: relative; display: inline-block; }
  .trigger { list-style: none; cursor: pointer; padding: token(space.2) token(space.3); border: 1px solid token(color.line); border-radius: token(radius.md); background: token(color.surface); color: token(color.on-surface); }
  .trigger::-webkit-details-marker { display: none; }
  .trigger:focus-visible { outline: 2px solid token(color.focus); }
  .items { position: absolute; z-index: 10; min-width: 100%; margin-top: token(space.1); padding: token(space.1); display: flex; flex-direction: column; background: token(color.surface); color: token(color.on-surface); border: 1px solid token(color.line); border-radius: token(radius.md); box-shadow: token(shadow.lg); }
</style>
<details class="menu" use:menu>
  <summary class="trigger" aria-label={if aria_label == "", label, aria_label}>{label}</summary>
  <div class="items">{render_slot(inner_block)}</div>
</details>

component MenuItem(href string @preview("/a"), label string @preview("a.example"), current bool = false)

<style>
  .item { display: block; padding: token(space.2) token(space.3); border-radius: token(radius.sm); color: inherit; text-decoration: none; white-space: nowrap; }
  .item:hover, .item:focus-visible { background: token(color.subtle); outline: none; }
  .current { font-weight: 600; }
</style>
<a class={"item" if current, "current"} href={href} aria-current={if current, "page", "false"}>{label}</a>
