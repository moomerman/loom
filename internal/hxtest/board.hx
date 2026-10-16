component Board(columns [][]Card, title string, show bool)

<h1>{shout(title)}</h1>
<div :if={show} class="board">
  <ul :for={col <- columns}>
    <li :for={card <- col} :key={card.id} data-priority={card.priority}>
      {card.title} <em :for={tag <- card.tags}>{tag}</em>
    </li>
  </ul>
</div>
<.badge :for={col <- columns} label={"Column of " + str(len(col))} />

component Badge(label string)

<span class="badge">{label}</span>

component Counter(label string, events []event)

<span class="badge">{label}</span><menu hidden><button :for={e <- events} on:click={e}>event</button></menu>
