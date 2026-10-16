component TodoList(todos []Todo, filter string)

<ul>
  <li :for={t <- todos} :if={t.done || filter == "all"} :key={t.id} class={if t.done, "done", ""}>
    <input type="checkbox" checked={t.done} on:change="toggle" val-id={t.id} />
    {t.title}
  </li>
</ul>

<.footer count={len(todos)}>
  <:left>Items</:left>
</.footer>
