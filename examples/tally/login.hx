component Login(people []Person)

<style>
  .page { min-height: 100%; display: grid; grid-template-columns: 1.1fr 1fr; }
  .hero { display: flex; flex-direction: column; justify-content: space-between; padding: token(space.12); background: token(color.accent); color: token(color.on-accent); }
  .brand { display: flex; align-items: center; gap: token(space.2); font-family: token(font.display); font-size: token(text.xl); font-weight: 600; }
  .logo { display: grid; place-items: center; width: 2rem; height: 2rem; border-radius: token(radius.md); background: token(color.on-accent); color: token(color.accent); font-weight: 700; }
  h1 { margin: 0 0 token(space.4); font-family: token(font.display); font-size: token(text.4xl); font-weight: 500; line-height: 1.1; letter-spacing: -0.02em; max-width: 14ch; }
  .lede { margin: 0; max-width: 34ch; opacity: 0.85; font-size: token(text.lg); }
  .fine { font-size: token(text.sm); opacity: 0.7; }
  .pick { display: grid; align-content: center; justify-items: center; padding: token(space.12) token(space.8); }
  .box { width: 100%; max-width: 24rem; }
  h2 { margin: 0 0 token(space.1); font-size: token(text.xl); font-weight: 600; }
  .hint { margin: 0 0 token(space.6); color: token(color.muted); }
  ul { list-style: none; margin: 0; padding: 0; display: grid; gap: token(space.2); }
  form { margin: 0; }
  .person { display: flex; align-items: center; gap: token(space.3); width: 100%; padding: token(space.3) token(space.4); border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); cursor: pointer; text-align: left; box-shadow: token(shadow.sm); transition: border-color token(duration.fast), transform token(duration.fast); }
  .person:hover { border-color: token(color.accent); transform: translateY(-1px); }
  .person:focus-visible { outline: 2px solid token(color.focus); outline-offset: 2px; }
  .name { flex: 1; display: grid; line-height: 1.3; }
  .name strong { font-weight: 600; }
  .name span { font-size: token(text.sm); color: token(color.muted); }
  .arrow { color: token(color.faint); }
  @media (max-width: 52rem) { .page { grid-template-columns: 1fr; } .hero { padding: token(space.8); } }
</style>
<div class="page">
  <section class="hero">
    <div class="brand"><span class="logo" aria-hidden="true">T</span>Tally</div>
    <div>
      <h1>Split bills. Stay friends.</h1>
      <p class="lede">Track shared expenses with flatmates, trips and clubs. Tally works out who owes whom, live, for everyone at once.</p>
    </div>
    <p class="fine">A Loom example app.</p>
  </section>
  <section class="pick">
    <div class="box">
      <h2>Sign in</h2>
      <p class="hint">Pick someone to be. There are no passwords here.</p>
      <ul>
        <li :for={p <- people} :key={p.id}>
          <form method="post" action="/login">
            <input type="hidden" name="person" value={p.id} />
            <button class="person" type="submit">
              <.avatar person={p} size="lg" />
              <span class="name"><strong>{p.name}</strong><span>{p.email}</span></span>
              <span class="arrow" aria-hidden="true">→</span>
            </button>
          </form>
        </li>
      </ul>
    </div>
  </section>
</div>
