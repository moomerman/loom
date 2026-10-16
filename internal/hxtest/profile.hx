component Profile(user User, admin bool, scores []int, ratio float)

<!-- profile card -->
<section id={"user-" + str(user.user_id)} hidden={!admin} data-x='say "hi"'>
  <h2>{user.name} &amp; friends</h2>
  <p>{user.address.city}, joined {user.created_at}</p>
  <div class="bio">{raw(user.html_bio)}</div>
  <p>{scores[0] + scores[1] * 2} {(scores[0] + scores[1]) * 2} {-scores[0] % 3}</p>
  <p>{ratio * 2.0} {ratio > 0.5} {admin && len(scores) > 1}</p>
  <p>{if admin, if ratio >= 1.0, "full", "partial", "guest"}</p>
  <p>{"<escaped> & \"quoted\""}</p>
  <script type="application/json">if (a < b) { go() }</script>
</section>
