component Fields(n int, choice string, added int)

<p class="n">{n}</p>
<button class="bump" on:click="bump">Bump</button>
<button class="pick" on:click="pick">Pick b</button>
<details><summary>More</summary><p>Details</p></details>
<input type="checkbox" name="agree" />
<input class="note" name="note" />
<select name="choice">
  <option value="a" selected={choice == "a"}>A</option>
  <option value="b" selected={choice == "b"}>B</option>
  <option value="c" selected={choice == "c"}>C</option>
</select>
<form class="add" on:submit="add"><input name="title" /><button type="submit">Add</button></form>
<p class="added">{added}</p>
