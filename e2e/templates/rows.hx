component Rows(rows []Row)

<ul>
  <li :for={r <- rows} :key={r.id}>
    <form on:change="save" val-id={r.id}><input name="title" value={r.title} /></form>
  </li>
</ul>
