component Numbers(xs []float, ns []int, text string, events []event)

<p :for={x <- xs} data-x={x}>{x} {x * 3.0} {x > 0.5}</p>
<p :for={n <- ns} :if={n != 0}>{n / 3} {n % 3} {-n / 2} {n * 1000000}</p>
<p>{len(text)} {text < "b"} {text == ""}</p>
<a href={path("/items?q=1")}>{path("/")} {path("/#top")}</a>
<img src={asset("logo.png")} alt={text} />
<menu :if={len(events) > 0} hidden><button :for={e <- events} on:click={e}>event</button></menu>
