component Board(columns [][]Card, empty bool)

<div :if={!empty}>
  <ul :for={col <- columns}>
    <li :for={card <- col} :key={card.id}>{card.title}</li>
  </ul>
</div>
