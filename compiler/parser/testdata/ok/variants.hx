import "example.com/app/components"

component Button(variant: :primary | :secondary | :danger = :primary, size: :sm | :md = :md, label string, on:click event, busy bool = false, count int = -1, ratio float = 0.5, title string = "")
<button class={variant size if busy, "busy"} on:click={on_click}>{label}</button>

component Row(done bool)
<li class={if done, "done", "open"}>x</li>
<li class={"row" if done, "done"}>x</li>
