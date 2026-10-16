import "github.com/moomerman/loom/internal/hxtest/parts"

behavior item(id int)
behavior sortable(axis: :x | :y = :y, ids []string) emits (moved(id string, to int), cancel) requires [role=list]

component Sortable(cards []Card, label string, axis: :x | :y = :y)

<ul role="list" aria-label={label} use:sortable={axis: axis, ids: keys_of(cards)} on:moved="move" on:cancel="cancel" on:click="pick" val-board="plan">
  <li :for={card <- cards} :key={card.id} use:item={id: card.id}>{card.title}</li>
</ul>
<p role="status" use:announce={polite: true}>{len(cards)} cards</p>
<span role="note" aria-label={label} use:hint></span>
