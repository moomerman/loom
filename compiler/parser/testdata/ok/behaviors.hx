behavior dropdown(open bool, value string = "") emits (select(value string), close) requires [role=listbox][aria-label]
behavior tabs() requires [role="tablist"]

component Menu(open bool, value string)

<ul role="listbox" aria-label="Fruit" use:dropdown={open: open || false, value: value} on:select="pick" on:close="close">
  <li role="option">Apple</li>
</ul>
<div role="tablist" use:tabs></div>
