component Footer(count int, left slot, inner_block slot)

<footer>
  <span class="left">{render_slot(left)}</span>
  <strong>{count}</strong>
  {render_slot(inner_block)}
</footer>
