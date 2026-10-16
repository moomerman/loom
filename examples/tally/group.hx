import "github.com/moomerman/loom/examples/tally/components"

component GroupView(missing bool = false, group GroupHead, members []Person, mine Amount, spent string, months []MonthGroup, balances []MemberBalance, payments []Payment, categories []CategoryTotal, recurring []RecurringRow, search string, category string, filters []Option, composing bool, expense form[ExpenseInput], expense_dialog ExpenseDialog, settling bool, settle form[SettleInput], settle_dialog SettleDialog, toast Toast)

<style>
  .page { max-width: 78rem; margin: 0 auto; padding: token(space.8) token(space.10) token(space.16); }
  .missing { max-width: 30rem; margin: token(space.16) auto; text-align: center; }
  .head { display: flex; align-items: center; gap: token(space.4); margin-bottom: token(space.6); }
  .title { flex: 1; min-width: 0; }
  h1 { margin: 0; font-family: token(font.display); font-size: token(text.2xl); font-weight: 500; letter-spacing: -0.01em; line-height: 1.2; }
  .sub { display: flex; align-items: center; gap: token(space.2); color: token(color.muted); font-size: token(text.sm); margin-top: token(space.1); }
  .actions { display: flex; gap: token(space.2); }
  .strip { display: grid; grid-template-columns: repeat(3, 1fr); gap: token(space.4); margin-bottom: token(space.8); }
  .tile { padding: token(space.4) token(space.5); border-radius: token(radius.lg); background: token(color.surface); border: 1px solid token(color.line); display: grid; gap: token(space.0-5); }
  .tile.you { background: token(color.accent-soft); border-color: transparent; }
  .tile-label { font-size: token(text.sm); color: token(color.muted); font-weight: 500; }
  .you .tile-label { color: token(color.on-accent-soft); }
  .tile-value { font-family: token(font.display); font-size: token(text.xl); font-variant-numeric: tabular-nums; }
  .layout { display: grid; grid-template-columns: minmax(0, 1fr) 22rem; gap: token(space.8); align-items: start; }
  .side { display: grid; gap: token(space.4); position: sticky; top: token(space.6); }
  .toolbar { display: flex; flex-direction: column; gap: token(space.3); margin-bottom: token(space.4); }
  .search { width: 100%; font: inherit; padding: token(space.2) token(space.3) token(space.2) token(space.8); border: 1px solid token(color.line); border-radius: token(radius.md); background: token(color.surface); color: token(color.on-surface); }
  .search:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .search-wrap { position: relative; margin: 0; }
  .search-wrap svg { position: absolute; left: token(space.3); top: 50%; transform: translateY(-50%); width: 1rem; height: 1rem; color: token(color.faint); pointer-events: none; }
  .chips { display: flex; flex-wrap: wrap; gap: token(space.1-5); }
  .chip { padding: token(space.1) token(space.3); border: 1px solid token(color.line); border-radius: token(radius.full); background: token(color.surface); font-size: token(text.sm); cursor: pointer; white-space: nowrap; }
  .chip:hover { border-color: token(color.line-strong); }
  .chip:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .chip.on { background: token(color.on-page); color: token(color.page); border-color: transparent; }
  .month { margin-bottom: token(space.6); }
  .month-head { display: flex; justify-content: space-between; align-items: baseline; padding: 0 token(space.1) token(space.2); }
  h2 { margin: 0; font-size: token(text.sm); font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase; color: token(color.muted); }
  .month-total { font-size: token(text.sm); color: token(color.muted); font-variant-numeric: tabular-nums; }
  .list { list-style: none; margin: 0; padding: 0; background: token(color.surface); border: 1px solid token(color.line); border-radius: token(radius.lg); box-shadow: token(shadow.card); overflow: hidden; }
  .list li + li { border-top: 1px solid token(color.line); }
  .row { display: flex; align-items: center; gap: token(space.4); padding: token(space.3) token(space.4); }
  .row:hover { background: token(color.hover); }
  .row:focus-visible { outline: 2px solid token(color.focus); outline-offset: -2px; }
  .date { display: grid; justify-items: center; width: 2.25rem; line-height: 1.1; color: token(color.muted); }
  .date small { font-size: token(text.xs); text-transform: uppercase; letter-spacing: 0.04em; }
  .date strong { font-size: token(text.lg); font-weight: 500; color: token(color.on-surface); }
  .desc { flex: 1; min-width: 0; display: grid; line-height: 1.35; }
  .desc strong { font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .desc span { font-size: token(text.sm); color: token(color.muted); }
  .repeat { font-size: token(text.xs); color: token(color.accent); font-weight: 600; margin-left: token(space.1); }
  .share { display: grid; justify-items: end; line-height: 1.3; min-width: 6.5rem; }
  .share small { font-size: token(text.xs); color: token(color.muted); }
  .people { list-style: none; margin: 0; padding: 0; display: grid; gap: token(space.3); }
  .person { display: grid; grid-template-columns: auto 1fr auto; gap: token(space.1) token(space.3); align-items: center; }
  .person-name { font-weight: 500; font-size: token(text.sm); }
  .person-name em { font-style: normal; color: token(color.muted); font-weight: 400; }
  .person-amount { display: grid; justify-items: end; line-height: 1.2; }
  .person-amount small { font-size: token(text.xs); color: token(color.muted); }
  .bar { grid-column: 2 / 4; height: 0.3rem; border-radius: token(radius.full); background: token(color.subtle); overflow: hidden; }
  .fill { display: block; height: 100%; border-radius: token(radius.full); }
  .fill.owed { background: token(color.owed); }
  .fill.owe { background: token(color.owe); }
  .fill.settled { background: transparent; }
  .pays { list-style: none; margin: 0; padding: 0; display: grid; gap: token(space.2); }
  .pay { display: flex; align-items: center; gap: token(space.2); padding: token(space.2) token(space.3); border-radius: token(radius.md); background: token(color.subtle); }
  .pay-text { flex: 1; min-width: 0; font-size: token(text.sm); line-height: 1.35; }
  .pay-text strong { font-weight: 600; font-variant-numeric: tabular-nums; }
  .arrow { color: token(color.faint); }
  .done { margin: 0; color: token(color.muted); font-size: token(text.sm); }
  .cats { list-style: none; margin: 0; padding: 0; display: grid; gap: token(space.3); }
  .cat { display: grid; grid-template-columns: 1fr auto; gap: token(space.1); font-size: token(text.sm); }
  .cat-amount { font-variant-numeric: tabular-nums; color: token(color.muted); }
  .cat .bar { grid-column: 1 / 3; }
  .cat-fill { display: block; height: 100%; border-radius: token(radius.full); background: token(color.accent); opacity: 0.75; }
  .bills { list-style: none; margin: 0; padding: 0; display: grid; gap: token(space.3); }
  .bill { display: flex; align-items: center; gap: token(space.3); }
  .bill-text { flex: 1; display: grid; line-height: 1.3; font-size: token(text.sm); }
  .bill-text strong { font-weight: 500; }
  .bill-text span { color: token(color.muted); font-size: token(text.xs); }
  .bill-amount { display: grid; justify-items: end; line-height: 1.3; font-size: token(text.sm); font-variant-numeric: tabular-nums; font-weight: 600; }
  .bill-amount span { font-weight: 400; color: token(color.muted); font-size: token(text.xs); }
  .form { display: grid; gap: token(space.4); }
  .grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: token(space.3); }
  .amount-field { font-size: token(text.xl); }
  .split-head { display: flex; align-items: center; justify-content: space-between; gap: token(space.2); }
  .split-label { font-size: token(text.sm); font-weight: 500; }
  .segmented { display: inline-flex; padding: token(space.0-5); border-radius: token(radius.md); background: token(color.subtle); border: 0; margin: 0; }
  .segmented label { position: relative; }
  .segmented input { position: absolute; opacity: 0; pointer-events: none; }
  .segmented span { display: block; padding: token(space.1) token(space.3); border-radius: token(radius.sm); font-size: token(text.sm); cursor: pointer; color: token(color.muted); }
  .segmented input:checked + span { background: token(color.surface); color: token(color.on-surface); box-shadow: token(shadow.sm); font-weight: 500; }
  .segmented input:focus-visible + span { outline: 2px solid token(color.focus); }
  .split { list-style: none; margin: 0; padding: 0; border: 1px solid token(color.line); border-radius: token(radius.md); }
  .split li + li { border-top: 1px solid token(color.line); }
  .split-row { display: flex; align-items: center; gap: token(space.3); padding: token(space.2) token(space.3); font-size: token(text.sm); cursor: pointer; }
  .split-row input[type=checkbox] { width: 1rem; height: 1rem; accent-color: token(color.accent); margin: 0; }
  .split-row .exact[aria-invalid=true] { border-color: token(color.danger); }
  .split-name { flex: 1; }
  .split-share { font-variant-numeric: tabular-nums; color: token(color.muted); }
  .out .split-name, .out .split-share { color: token(color.faint); }
  .exact { width: 7rem; font: inherit; text-align: right; padding: token(space.1) token(space.2); border: 1px solid token(color.line); border-radius: token(radius.sm); background: token(color.surface); color: token(color.on-surface); font-variant-numeric: tabular-nums; }
  .remaining { font-size: token(text.sm); text-align: right; color: token(color.owe); font-variant-numeric: tabular-nums; }
  .remaining.ok { color: token(color.owed); }
  .split-error { margin: 0; font-size: token(text.sm); color: token(color.danger); }
  .error { margin: 0; padding: token(space.2) token(space.3); border-radius: token(radius.md); background: color-mix(in srgb, token(color.danger) 12%, transparent); color: token(color.danger); font-size: token(text.sm); }
  .buttons { display: flex; justify-content: flex-end; gap: token(space.2); margin-top: token(space.2); }
  .form.loom-pending .buttons { opacity: 0.6; pointer-events: none; }
  @media (max-width: 40rem) { .head { flex-wrap: wrap; } .actions { width: 100%; } .title { min-width: 60%; } }
  @media (max-width: 64rem) { .layout { grid-template-columns: 1fr; } .side { position: static; } .page { padding: token(space.6) token(space.4); } .strip { grid-template-columns: 1fr; } }
</style>

<div class="page missing" :if={missing}>
  <.empty_note title="This group isn't here">It may have been deleted, or you aren't in it. <a href="/">Back to your dashboard</a>.</.empty_note>
</div>

<div class="page" :if={!missing}>
  <header class="head">
    <.group_tile emoji={group.emoji} color={group.color} size="lg" />
    <div class="title">
      <h1>{group.name}</h1>
      <div class="sub"><.avatar_stack people={members} /><span>{group.members} members · {group.since}</span></div>
    </div>
    <div class="actions">
      <.button label="Settle up" variant="secondary" on:click="settle_up" />
      <.button label="Add expense" on:click="add_expense" />
    </div>
  </header>

  <section class="strip" aria-label="Summary">
    <div class="tile you">
      <span class="tile-label">{if mine.tone == "owe", "You owe", if mine.tone == "owed", "You are owed", "You are"}</span>
      <span class="tile-value" :if={mine.tone != "settled"}><.money amount={mine} size="lg" /></span>
      <span class="tile-value" :if={mine.tone == "settled"}>all settled up</span>
    </div>
    <div class="tile">
      <span class="tile-label">Total spent</span>
      <span class="tile-value">{spent}</span>
    </div>
    <div class="tile">
      <span class="tile-label">Payments to settle up</span>
      <span class="tile-value">{len(payments)}</span>
    </div>
  </section>

  <div class="layout">
    <section aria-label="Expenses">
      <div class="toolbar">
        <form class="search-wrap" role="search" on:change="search" on:submit="search">
          <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="7" cy="7" r="4.5" fill="none" stroke="currentColor" stroke-width="1.5" /><path d="M10.5 10.5 14 14" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" /></svg>
          <input class="search" type="search" name="q" placeholder="Search expenses" aria-label="Search expenses" value={search} on:input="search" />
        </form>
        <div class="chips" role="group" aria-label="Category" :if={len(filters) > 1}>
          <button :for={f <- filters} :key={f.value} type="button" class={"chip" if f.selected, "on"} aria-pressed={if f.selected, "true", "false"} on:click="filter_category" val-category={f.value} optimistic:class="on">{f.label}</button>
        </div>
      </div>

      <div class="month" :for={m <- months} :key={m.label}>
        <div class="month-head"><h2>{m.label}</h2><span class="month-total">{m.total}</span></div>
        <ul class="list">
          <li :for={e <- m.expenses} :key={e.id}>
            <a class="row" href={e.href}>
              <span class="date"><small>{e.month_abr}</small><strong>{e.day}</strong></span>
              <.category_icon category={e.category} />
              <span class="desc"><strong>{e.title}<span class="repeat" :if={e.recurs}>↻ monthly</span></strong><span>{e.paid_by}</span></span>
              <span class="share"><small>{e.mine_note}</small><.money amount={e.mine} /></span>
            </a>
          </li>
        </ul>
      </div>
      <.empty_note :if={len(months) == 0 && (search != "" || category != "")} title="No matches">Nothing matches that search or category.</.empty_note>
      <.empty_note :if={len(months) == 0 && search == "" && category == ""} title="No expenses yet">Add the first one and Tally will keep score.</.empty_note>
    </section>

    <aside class="side">
      <.panel title="Balances">
        <ul class="people">
          <li class="person" :for={b <- balances} :key={b.person.id}>
            <.avatar person={b.person} size="sm" />
            <span class="person-name">{b.person.first}<em :if={b.me}> (you)</em></span>
            <span class="person-amount"><.money amount={b.amount} size="sm" /><small :if={b.amount.tone != "settled"}>{b.amount.label}</small></span>
            <span class="bar"><span class={"fill" b.amount.tone} style={width(b.bar)}></span></span>
          </li>
        </ul>
      </.panel>

      <.panel title="Settle up">
        <p class="done" :if={len(payments) == 0}>Everyone's square. 🎉</p>
        <ul class="pays" :if={len(payments) > 0}>
          <li class="pay" :for={p <- payments} :key={p.key}>
            <.avatar person={p.from} size="sm" />
            <span class="arrow" aria-hidden="true">→</span>
            <.avatar person={p.to} size="sm" />
            <span class="pay-text">{if p.from_me, "You pay", p.from.first + " pays"} {if p.to_me, "you", p.to.first} <strong>{p.amount}</strong></span>
            <.button :if={p.from_me} label="Record" size="sm" variant="secondary" on:click="settle_up" val-from={p.from.id} val-to={p.to.id} val-amount={format_money(p.cents)} />
            <.button :if={p.to_me} label="Remind" size="sm" variant="ghost" on:click="remind" val-debtor={p.from.id} val-cents={p.cents} optimistic:disabled />
          </li>
        </ul>
      </.panel>

      <.panel title="Spending" :if={len(categories) > 0}>
        <ul class="cats">
          <li class="cat" :for={c <- categories} :key={c.category.key}>
            <span>{c.category.icon} {c.category.label}</span>
            <span class="cat-amount">{c.amount} · {c.percent}%</span>
            <span class="bar"><span class="cat-fill" style={width(c.bar)}></span></span>
          </li>
        </ul>
      </.panel>

      <.panel title="Monthly bills" :if={len(recurring) > 0}>
        <ul class="bills">
          <li class="bill" :for={r <- recurring} :key={r.id}>
            <.category_icon category={r.category} />
            <span class="bill-text"><strong>{r.title}</strong><span>{r.paid_by} · next {r.next}</span></span>
            <span class="bill-amount">{r.amount}<span>a month</span></span>
          </li>
        </ul>
      </.panel>
    </aside>
  </div>

  <.dialog open={composing} title="Add an expense" on:close="close_expense">
    <form class="form" on:change="expense_form" on:submit="save_expense">
      <.input field={expense.description} label="Description" placeholder="Groceries, dinner, taxi…" autofocus />
      <div class="grid2">
        <.input field={expense.amount} label="Amount ($)" placeholder="0.00" />
        <.input field={expense.date} label="Date" type="date" />
      </div>
      <div class="grid2">
        <.select field={expense.paid_by} label="Paid by">
          <option :for={o <- expense_dialog.paid_by} :key={o.value} value={o.value} selected={o.selected}>{o.label}</option>
        </.select>
        <.select field={expense.category} label="Category">
          <option :for={o <- expense_dialog.categories} :key={o.value} value={o.value} selected={o.selected}>{o.label}</option>
        </.select>
      </div>
      <div class="split-head">
        <span class="split-label">Split</span>
        <fieldset class="segmented" aria-label="How to split">
          <label><input type="radio" name={expense.split.name} value="equal" checked={!expense_dialog.exact} /><span>Equally</span></label>
          <label><input type="radio" name={expense.split.name} value="exact" checked={expense_dialog.exact} /><span>Exact amounts</span></label>
        </fieldset>
      </div>
      <!-- Both lists stay in the form, the other one hidden, so switching keeps what each holds: an unchecked box isn't sent, so a list left out would read as nobody. -->
      <ul class="split" hidden={expense_dialog.exact}>
        <li :for={s <- expense_dialog.split} :key={s.person.id}>
          <label class={"split-row" if !s.included, "out"}>
            <input type="checkbox" name={expense.among[s.person.id].name} checked={s.included} />
            <.avatar person={s.person} size="sm" />
            <span class="split-name">{s.person.name}</span>
            <span class="split-share">{if s.included, s.share, "—"}</span>
          </label>
        </li>
      </ul>
      <ul class="split" hidden={!expense_dialog.exact}>
        <li :for={s <- expense_dialog.split} :key={s.person.id}>
          <label class="split-row">
            <.avatar person={s.person} size="sm" />
            <span class="split-name">{s.person.name}</span>
            <input class="exact" name={expense.exact[s.person.id].name} value={expense.exact[s.person.id].value} placeholder="0.00" inputmode="decimal" aria-label={"Amount for " + s.person.name} aria-invalid={if expense.exact[s.person.id].error != "", "true", "false"} title={expense.exact[s.person.id].error} />
          </label>
        </li>
      </ul>
      <p :if={expense_dialog.exact} class={"remaining" if expense_dialog.balanced, "ok"}>{if expense_dialog.balanced, "All assigned ✓", expense_dialog.remaining + " left to assign"}</p>
      <p class="split-error" role="alert" :if={expense.split.error != ""}>{expense.split.error}</p>
      <p class="error" role="alert" :if={expense_dialog.fail != ""}>{expense_dialog.fail}</p>
      <div class="buttons">
        <.button label="Cancel" variant="ghost" on:click="close_expense" />
        <.button label="Add expense" type="submit" />
      </div>
    </form>
  </.dialog>

  <.dialog open={settling} title="Record a payment" on:close="close_settle">
    <form class="form" on:change="settle_form" on:submit="save_settle">
      <div class="grid2">
        <.select field={settle.from} label="Who paid">
          <option :for={o <- settle_dialog.from} :key={o.value} value={o.value} selected={o.selected}>{o.label}</option>
        </.select>
        <.select field={settle.to} label="Who they paid">
          <option :for={o <- settle_dialog.to} :key={o.value} value={o.value} selected={o.selected}>{o.label}</option>
        </.select>
      </div>
      <.input field={settle.amount} label="Amount ($)" placeholder="0.00" autofocus />
      <p class="error" role="alert" :if={settle_dialog.fail != ""}>{settle_dialog.fail}</p>
      <div class="buttons">
        <.button label="Cancel" variant="ghost" on:click="close_settle" />
        <.button label="Record payment" type="submit" />
      </div>
    </form>
  </.dialog>
  <.toast_bar toast={toast} />
</div>
