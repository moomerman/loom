component NewTodo(title string, count int)

<form class="new-todo" on:submit="add" val-list="home">
  <input name="title" value={title} on:input="typing" />
  <select name="priority" on:change="prioritise">
    <option value="1">Low</option>
    <option value="2" selected>Normal</option>
  </select>
  <label><input type="checkbox" name="urgent" on:change="urgent" val-list="home" /> Urgent</label>
  <textarea name="notes">none</textarea>
  <button type="submit">Add</button>
</form>
<button class="clear" on:click="clear" val-count={count}><span>Clear {count}</span></button>
