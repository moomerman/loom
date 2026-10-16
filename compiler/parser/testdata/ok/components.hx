component Footer(count int, left slot, inner_block slot)

<footer>
  <span>{render_slot(left)}</span>
  {count} {render_slot(inner_block)}
</footer>

component Page(items []Item)

<.footer count={len(items)}>
  <:left>Items</:left>
  left
</.footer>
<.footer count={0}>
  <:left class="x">A</:left>
  <:left>B</:left>
</.footer>
<.footer count={1} />
<section :for={i <- items} :key={i.id}>
  <.badge :if={i.new} label={i.name} />
</section>
