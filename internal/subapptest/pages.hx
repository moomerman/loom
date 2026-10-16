component Root(title string, inner_block slot)

<!DOCTYPE html>
<html>
<head>
  <.live_title default="Sub">{title}</.live_title>
  <.live_meta />
  <link rel="stylesheet" href={asset("style.css")} />
  <.loom_styles />
</head>
<body>
  {render_slot(inner_block)}
  <.loom_scripts />
</body>
</html>

component Index(items []Entry)

<nav><a href={path("/")}>Home</a></nav>
<ul>
  <li :for={it <- items} :key={it.id}><a href={path("/items/" + it.id)}>{it.name}</a></li>
</ul>

component Item(id string, next string)

<p>Item {id}</p>
<a class="back" href={path("/")}>Back</a>
<a class="next" href={next}>Next</a>
