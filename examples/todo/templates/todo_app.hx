import "github.com/moomerman/loom/examples/todo/components"

component TodoApp(todos []Todo, filter string, remaining int, completed int)

<style>
  .todoapp { max-width: 32rem; margin: token(space.12) auto; }
  h1 { margin: 0 0 token(space.4); font-weight: 300; font-size: token(text.title); color: token(color.accent); text-align: center; }
  .new-todo { margin: 0 0 token(space.4); }
  .todo-list { list-style: none; padding: 0; margin: 0; }
  .todo-list li { display: flex; align-items: center; justify-content: space-between; gap: token(space.2); padding: token(space.2) 0; border-bottom: 1px solid token(color.line); }
  .todo-list li > :first-child { flex: 1; }
  .done { text-decoration: line-through; color: token(color.muted); }
</style>
<div class="todoapp">
  <.card>
    <h1>todos</h1>
    <form class="new-todo" on:submit="add_todo">
      <.input name="title" label="New todo" hide_label={true} placeholder="What needs to be done?" required={true} autofocus={true} />
    </form>
    <.empty_state :if={len(todos) == 0} title="Nothing to do">Add a todo above.</.empty_state>
    <ul class="todo-list">
      <li :for={t <- todos} :if={filter == "" || filter == "all" || (filter == "done" && t.done) || (filter == "active" && !t.done)} :key={t.id} class={if t.done, "done"}>
        <.checkbox label={t.title} checked={t.done} on:change="toggle_todo" val-id={t.id} optimistic:disabled />
        <.link href={todo_path(t.id)} label="Details" />
        <.button label="Delete" variant="ghost" size="sm" on:click="delete_todo" val-id={t.id} optimistic:disabled />
      </li>
    </ul>
    <.footer remaining={remaining} completed={completed} filter={filter} />
  </.card>
</div>

component Footer(remaining int, completed int, filter string)

<style>
  footer { display: flex; justify-content: space-between; align-items: center; gap: token(space.2); margin-top: token(space.4); color: token(color.muted); }
  .filters { display: flex; gap: token(space.1); }
</style>

<footer>
  <span class="count">{remaining} {if remaining == 1, "item", "items"} left</span>
  <nav class="filters" aria-label="Show">
    <.link href="/" label="All" variant="nav" current={filter == "" || filter == "all"} />
    <.link href="/?filter=active" label="Active" variant="nav" current={filter == "active"} />
    <.link href="/?filter=done" label="Done" variant="nav" current={filter == "done"} />
  </nav>
  <.button :if={completed > 0} label="Clear completed" variant="secondary" size="sm" on:click="clear_completed" />
</footer>

component TodoDetail(todo Todo, id string)

<style>
  .detail { max-width: 32rem; margin: token(space.12) auto; }
  dl { display: grid; grid-template-columns: auto 1fr; gap: token(space.2) token(space.4); margin: 0 0 token(space.4); }
  dt { color: token(color.muted); }
  dd { margin: 0; }
</style>
<div class="detail">
  <.card>
    <.empty_state :if={todo.id == ""} title="No such todo">It may have been deleted.</.empty_state>
    <section :if={todo.id != ""}>
      <h1>{todo.title}</h1>
      <dl>
        <dt>Status</dt><dd class="state">{if todo.done, "Done", "To do"}</dd>
        <dt>Added</dt><dd><time datetime={todo.inserted_at}>{todo.inserted_at}</time></dd>
      </dl>
      <.button label={if todo.done, "Mark as to do", "Mark as done"} variant="secondary" on:click="toggle_todo" val-id={todo.id} />
    </section>
    <p><.link href="/" label="Back to the list" /></p>
  </.card>
</div>
