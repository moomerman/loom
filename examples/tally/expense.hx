import "github.com/moomerman/loom/examples/tally/components"

component ExpenseView(missing bool = false, group GroupHead, back string, expense ExpenseDetail, shares []ShareRow, confirming bool = false)

<style>
  .page { max-width: 46rem; margin: 0 auto; padding: token(space.8) token(space.8) token(space.16); }
  .crumbs { display: flex; align-items: center; gap: token(space.2); margin-bottom: token(space.6); color: token(color.muted); font-size: token(text.sm); }
  .crumbs a:hover { color: token(color.on-page); }
  .card { background: token(color.surface); border: 1px solid token(color.line); border-radius: token(radius.xl); box-shadow: token(shadow.card); overflow: hidden; }
  .top { display: flex; gap: token(space.5); align-items: flex-start; padding: token(space.8); border-bottom: 1px solid token(color.line); }
  .heading { flex: 1; min-width: 0; }
  .kind { font-size: token(text.sm); color: token(color.muted); font-weight: 500; }
  .repeat { color: token(color.accent); font-weight: 600; }
  h1 { margin: token(space.1) 0 token(space.2); font-family: token(font.display); font-size: token(text.2xl); font-weight: 500; line-height: 1.2; letter-spacing: -0.01em; }
  .amount { font-family: token(font.display); font-size: token(text.3xl); font-variant-numeric: tabular-nums; line-height: 1; }
  .meta { display: grid; grid-template-columns: repeat(3, 1fr); border-bottom: 1px solid token(color.line); }
  .meta div { padding: token(space.4) token(space.8); display: grid; gap: token(space.1); }
  .meta div + div { border-left: 1px solid token(color.line); }
  .meta span { font-size: token(text.xs); color: token(color.muted); text-transform: uppercase; letter-spacing: 0.05em; font-weight: 600; }
  .meta strong { font-weight: 500; display: flex; align-items: center; gap: token(space.2); }
  .split { padding: token(space.6) token(space.8); }
  h2 { margin: 0 0 token(space.4); font-size: token(text.sm); font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase; color: token(color.muted); }
  ul { list-style: none; margin: 0; padding: 0; display: grid; gap: token(space.3); }
  li { display: flex; align-items: center; gap: token(space.3); }
  .name { flex: 1; }
  .paid { margin-left: token(space.2); padding: token(space.0-5) token(space.2); border-radius: token(radius.full); background: token(color.accent-soft); color: token(color.on-accent-soft); font-size: token(text.xs); font-weight: 600; }
  .pct { color: token(color.muted); font-size: token(text.sm); font-variant-numeric: tabular-nums; width: 3rem; text-align: right; }
  .share { font-variant-numeric: tabular-nums; font-weight: 600; width: 6rem; text-align: right; }
  .foot { display: flex; align-items: center; justify-content: space-between; gap: token(space.4); padding: token(space.4) token(space.8); background: token(color.subtle); font-size: token(text.sm); color: token(color.muted); }
  .note { margin: 0; padding: 0 token(space.8) token(space.6); color: token(color.muted); }
  .gone { padding: token(space.12) token(space.8); text-align: center; }
  .gone a { color: token(color.accent); font-weight: 500; }
  p.confirm { margin: 0; color: token(color.muted); }
</style>
<div class="page">
  <nav class="crumbs" aria-label="Breadcrumb">
    <a href={back}>{group.emoji} {group.name}</a><span aria-hidden="true">/</span><span>{if missing, "Not found", expense.title}</span>
  </nav>
  <div class="card gone" :if={missing}>
    <.empty_note title="This expense was deleted">Someone in the group removed it. <a href={back}>Back to the group</a></.empty_note>
  </div>
  <article class="card" :if={!missing}>
    <div class="top">
      <.category_icon category={expense.category} size="lg" />
      <div class="heading">
        <span class="kind">{expense.category.label}<span class="repeat" :if={expense.recurs}> · ↻ monthly</span></span>
        <h1>{expense.title}</h1>
        <div class="amount">{expense.amount}</div>
      </div>
    </div>
    <div class="meta">
      <div><span>Paid by</span><strong><.avatar person={expense.paid_by} size="sm" />{expense.payer}</strong></div>
      <div><span>Date</span><strong>{expense.date}</strong></div>
      <div><span>{expense.mine_note}</span><strong><.money amount={expense.mine} /></strong></div>
    </div>
    <section class="split" aria-labelledby="split-heading">
      <h2 id="split-heading">Split between {len(shares)}</h2>
      <ul>
        <li :for={s <- shares} :key={s.person.id}>
          <.avatar person={s.person} />
          <span class="name">{s.person.name}<span class="paid" :if={s.paid}>paid</span></span>
          <span class="pct">{s.percent}%</span>
          <span class="share">{s.amount}</span>
        </li>
      </ul>
    </section>
    <p class="note" :if={expense.note != ""}>{expense.note}</p>
    <div class="foot">
      <span>{expense.added_by}</span>
      <.button label="Delete expense" variant="ghost" size="sm" on:click="delete" />
    </div>
  </article>
  <.dialog open={confirming} title="Delete this expense?" on:close="cancel_delete">
    <p class="confirm">“{expense.title}” will be removed for everyone in {group.name}, and balances will update.</p>
    <:actions>
      <.button label="Cancel" variant="ghost" on:click="cancel_delete" />
      <.button label="Delete" variant="danger" on:click="confirm_delete" optimistic:disabled />
    </:actions>
  </.dialog>
</div>
