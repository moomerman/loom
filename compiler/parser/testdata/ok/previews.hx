component Button(label string @preview("Save", "Delete forever"), count int = 1 @preview(3, 20), ratio float @preview(0.5), variant: :primary | :danger = :primary, inner_block slot @preview("<p>Some content</p>"), on:click event)

<button class={variant}>{label}</button>
