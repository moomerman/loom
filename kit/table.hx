component Table(caption string = "" @preview("Jobs"), head slot @preview("<tr><th>ID</th><th>State</th></tr>"), inner_block slot @preview("<tr><td>1</td><td>completed</td></tr>"))

<style>
  table { width: 100%; border-collapse: collapse; font-size: token(text.sm); }
  caption { text-align: left; padding: token(space.2) 0; color: token(color.muted); }
  thead { background: token(color.subtle); color: token(color.on-subtle); }
  .table { border: 1px solid token(color.line); border-radius: token(radius.md); overflow-x: auto; }
</style>
<div class="table">
  <table>
    <caption :if={caption != ""}>{caption}</caption>
    <thead>{render_slot(head)}</thead>
    <tbody>{render_slot(inner_block)}</tbody>
  </table>
</div>
