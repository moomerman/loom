component Expressions(a int, b int, items []string, user User, ok bool)

<p>{a + b * 2 - 1}</p>
<p>{(a + b) * 2 % 3}</p>
<p>{!ok && a < b || a >= b}</p>
<p>{a == 1 != false}</p>
<p>{-a / 2}</p>
<p>{items[0]}</p>
<p>{user.address.city}</p>
<p>{users[a].name}</p>
<p>{upper(user.name)}</p>
<p>{join(items, ", ")}</p>
<p>{now()}</p>
<p>{if ok, if a > b, "a", "b", "none"}</p>
<p>{3.25}</p>
<p>{"tab\tquote\"backslash\\newline\n"}</p>
<p>{true} {false}</p>
<p>{raw(user.bio)}</p>
<p>{"braces { } in strings"}</p>
