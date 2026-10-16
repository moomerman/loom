import "github.com/moomerman/loom/internal/hxtest/parts"

component Button(variant: :primary | :secondary | :danger = :primary, size: :sm | :md = :md, label string @preview("Save", "Delete forever"), on:click event, busy bool = false)

<button class={variant size if busy, "busy"} disabled={busy} on:click={on_click}>{label}</button>

component Toolbar(id string, danger bool, on:click event)

<div class={if danger, "danger", "safe"}>
  <.button label="Save" on:click="save" val-id={id} optimistic:disabled />
  <.button label="Delete" variant="danger" size="sm" busy={danger} on:click={on_click} val-confirm="yes" optimistic:class="leaving gone" />
  <.pill label="tag" tone="warm" on:click="tag" val-id={id} />
  <.pill label="plain" />
  <span :if={danger} on:click="dismiss" optimistic:hidden>careful</span>
</div>
