package tally

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/form"
)

// GroupPage is one group: its expenses by month, everyone's balance, the
// payments that would settle it up, spending by category and monthly
// bills, with dialogs to add an expense and record a payment. Everything
// it shows is read in Assigns, so a change from another member, a job
// or another server re-renders it (loom.Watch in app.go).
type GroupPage struct {
	app     *App
	me      Person
	groupID int64
	missing bool // not a group the person belongs to

	search   string
	category string
	toast    Toast

	composing   bool
	expense     form.Form[ExpenseInput]
	expenseFail string // why saving failed, when it wasn't the input's fault

	settling   bool
	settle     form.Form[SettleInput]
	settleFail string
}

// ExpenseInput is the add-expense form. Its fields are the form's
// fields: the template binds each input with field={expense.amount}, and
// the form decodes what is typed into them, so a name can't drift
// between the markup, this struct and the insert.
type ExpenseInput struct {
	Description string
	Amount      string // dollars as typed, "12.50"; parsed to cents by parseCents
	Date        string // from a date input, 2006-01-02
	Category    string
	PaidBy      string
	Split       string            // "equal" or "exact"
	Among       map[string]bool   // split equally: who shares it, by person ID
	Exact       map[string]string // exact amounts: each person's, as typed
}

// Validate checks the expense; the form shows each error by its field
// once the person has changed it, or every error after a submit.
func (in *ExpenseInput) Validate(e *form.Errors) {
	if strings.TrimSpace(in.Description) == "" {
		e.Add(&in.Description, "What was it for? Add a description.")
	}
	cents, ok := parseCents(in.Amount)
	if !ok || cents == 0 {
		e.Add(&in.Amount, "Enter an amount, like 12.50.")
	}
	if _, err := time.Parse("2006-01-02", in.Date); err != nil {
		e.Add(&in.Date, "Pick the date it was spent.")
	}
	if in.Split == "exact" {
		var sum int64
		for who, v := range in.Exact {
			c, ok := parseCents(v)
			if strings.TrimSpace(v) != "" && !ok {
				e.AddKey(&in.Exact, who, "Enter an amount, like 4.50.")
			}
			sum += c
		}
		switch {
		case sum == 0:
			e.Add(&in.Split, "Give at least one person an amount.")
		case ok && sum != cents:
			e.Add(&in.Split, fmt.Sprintf("The amounts add up to %s, not %s.", money(sum), money(cents)))
		}
		return
	}
	for _, included := range in.Among {
		if included {
			return
		}
	}
	e.Add(&in.Split, "Split it with at least one person.")
}

// SettleInput is the record-a-payment form.
type SettleInput struct {
	From   string
	To     string
	Amount string
}

func (in *SettleInput) Validate(e *form.Errors) {
	if in.From == "" {
		e.Add(&in.From, "Pick who paid.")
	}
	switch {
	case in.To == "":
		e.Add(&in.To, "Pick who they paid.")
	case in.To == in.From:
		e.Add(&in.To, "Pick two different people.")
	}
	if cents, ok := parseCents(in.Amount); !ok || cents == 0 {
		e.Add(&in.Amount, "Enter an amount, like 40.00.")
	}
}

// GroupHead is the group as the page's header shows it.
type GroupHead struct {
	ID      int
	Name    string
	Emoji   string
	Color   string
	Since   string // "Since July 2026"
	Members int
}

// ExpenseDialog is what the add-expense dialog shows beyond its fields:
// the choices, and the split worked out from what has been typed.
type ExpenseDialog struct {
	Categories []Option
	PaidBy     []Option
	Exact      bool
	Split      []SplitRow
	Remaining  string // exact split: what's left to assign, "$0.00" when done
	Balanced   bool
	Fail       string
}

// SettleDialog is what the record-a-payment dialog shows beyond its fields.
type SettleDialog struct {
	From []Option
	To   []Option
	Fail string
}

func (p *GroupPage) Mount(c *loom.Ctx) {
	me, err := p.app.me(c)
	must(err)
	p.me = me
	p.groupID = id(c.Request.PathValue("id"))
	g, err := p.app.store.Group(c.Context(), p.groupID, me.ID)
	if errors.Is(err, ErrNotFound) {
		p.missing = true
		c.Title("Group not found")
		return
	}
	must(err)
	c.Set("section", "group:"+strconv.FormatInt(p.groupID, 10))
	c.Title(g.Name)
}

func (p *GroupPage) Events() loom.Events {
	return loom.Events{
		"search":          p.setSearch,
		"filter_category": p.filterCategory,
		"add_expense":     p.openComposer,
		"close_expense":   func(c *loom.Ctx, _ loom.Payload) { p.composing = false },
		"expense_form":    p.expenseForm,
		"save_expense":    p.saveExpense,
		"settle_up":       p.openSettle,
		"close_settle":    func(c *loom.Ctx, _ loom.Payload) { p.settling = false },
		"settle_form":     p.settleForm,
		"save_settle":     p.saveSettle,
		"remind":          p.remind,
		"toast_close":     func(c *loom.Ctx, _ loom.Payload) { p.toast.Text = "" },
	}
}

func (p *GroupPage) Assigns(c *loom.Ctx) GroupViewAssigns {
	if p.missing {
		return GroupViewAssigns{Missing: true}
	}
	ctx, store := c.Context(), p.app.store
	g, err := store.Group(ctx, p.groupID, p.me.ID)
	if errors.Is(err, ErrNotFound) {
		// Removed from the group, or the group deleted, while looking at it.
		return GroupViewAssigns{Missing: true}
	}
	must(err)
	members, err := store.Members(ctx, p.groupID)
	must(err)
	byID := map[string]Person{}
	for _, m := range members {
		byID[m.ID] = m
	}
	expenses, err := store.Expenses(ctx, p.groupID, p.me.ID, ExpenseFilter{Search: p.search, Category: p.category})
	must(err)
	nets, err := store.Nets(ctx, p.groupID)
	must(err)
	cats, spent, err := store.CategoryTotals(ctx, p.groupID)
	must(err)
	recurring, err := store.Recurring(ctx, p.groupID)
	must(err)

	return GroupViewAssigns{
		Group: GroupHead{
			ID: int(g.ID), Name: g.Name, Emoji: g.Emoji, Color: g.Color,
			Since: "Since " + g.CreatedAt.Format("January 2006"), Members: len(members),
		},
		Members:       members,
		Mine:          balance(nets[p.me.ID]),
		Spent:         money(spent),
		Months:        p.months(expenses, byID),
		Balances:      balances(members, nets, p.me.ID),
		Payments:      p.payments(nets, byID),
		Categories:    cats,
		Recurring:     p.recurring(recurring, byID),
		Search:        p.search,
		Category:      p.category,
		Filters:       p.filters(cats),
		Composing:     p.composing,
		Expense:       p.expense,
		ExpenseDialog: p.expenseDialog(members),
		Settling:      p.settling,
		Settle:        p.settle,
		SettleDialog:  p.settleDialog(members),
		Toast:         p.toast,
	}
}

// months groups expenses by the month they were spent in.
func (p *GroupPage) months(recs []expenseRec, byID map[string]Person) []MonthGroup {
	var out []MonthGroup
	var totals []int64
	for _, e := range recs {
		label := e.SpentOn.Format("January 2006")
		if len(out) == 0 || out[len(out)-1].Label != label {
			out = append(out, MonthGroup{Label: label})
			totals = append(totals, 0)
		}
		m := &out[len(out)-1]
		totals[len(totals)-1] += e.AmountCents
		m.Expenses = append(m.Expenses, p.expenseRow(e, byID))
	}
	for i := range out {
		out[i].Total = money(totals[i])
	}
	return out
}

func (p *GroupPage) expenseRow(e expenseRec, byID map[string]Person) ExpenseRow {
	payer := byID[e.PaidBy].First
	if payer == "" {
		payer = "A former member"
	}
	if e.PaidBy == p.me.ID {
		payer = "You"
	}
	row := ExpenseRow{
		ID: int(e.ID), Href: fmt.Sprintf("/groups/%d/expenses/%d", e.GroupID, e.ID),
		Day: e.SpentOn.Format("02"), MonthAbr: e.SpentOn.Format("Jan"),
		Title: e.Description, Category: category(e.Category),
		Amount: money(e.AmountCents), PaidBy: payer + " paid " + money(e.AmountCents),
		Recurs: e.RecurringID != nil,
	}
	switch {
	case e.PaidBy == p.me.ID && e.AmountCents-e.MyShare > 0:
		row.Mine, row.MineNote = balance(e.AmountCents-e.MyShare), "you lent"
	case e.PaidBy != p.me.ID && e.MyShare > 0:
		row.Mine, row.MineNote = balance(-e.MyShare), "you borrowed"
	default:
		row.Mine, row.MineNote = balance(0), "not involved"
	}
	return row
}

// balances lists every member's standing, the largest first, with bars
// scaled to the largest.
func balances(members []Person, nets map[string]int64, me string) []MemberBalance {
	var top int64
	for _, n := range nets {
		top = max(top, n, -n)
	}
	out := make([]MemberBalance, len(members))
	for i, m := range members {
		n := nets[m.ID]
		a := balance(n)
		switch a.Tone {
		case "owed":
			a.Label = "gets back"
		case "owe":
			a.Label = "owes"
		}
		out[i] = MemberBalance{Person: m, Amount: a, Me: m.ID == me, Bar: int(max(n, -n) * 100 / max(top, 1))}
	}
	return out
}

// payments are the fewest payments that settle the group up.
func (p *GroupPage) payments(nets map[string]int64, byID map[string]Person) []Payment {
	var out []Payment
	for _, pay := range payments(nets) {
		out = append(out, Payment{
			From: byID[pay.From], To: byID[pay.To], Cents: int(pay.Cents), Amount: money(pay.Cents),
			FromMe: pay.From == p.me.ID, ToMe: pay.To == p.me.ID, Key: pay.From + ">" + pay.To,
		})
	}
	return out
}

// recurring lists the monthly bills with when each is next posted.
func (p *GroupPage) recurring(recs []recurringRec, byID map[string]Person) []RecurringRow {
	now := p.app.store.now()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	out := make([]RecurringRow, len(recs))
	for i, r := range recs {
		next := month.AddDate(0, 0, r.DayOfMonth-1)
		if r.LastPosted != nil && !r.LastPosted.Before(month) {
			next = month.AddDate(0, 1, r.DayOfMonth-1)
		}
		out[i] = RecurringRow{
			ID: int(r.ID), Title: r.Description, Category: category(r.Category), Amount: money(r.AmountCents),
			PaidBy: byID[r.PaidBy].First + " pays", Next: next.Format("Jan 2"),
		}
	}
	return out
}

// filters are the category chips above the list: All, then each
// category the group has spent on, largest first.
func (p *GroupPage) filters(cats []CategoryTotal) []Option {
	out := []Option{{Value: "", Label: "All", Selected: p.category == ""}}
	for _, c := range cats {
		cat := c.Category
		out = append(out, Option{Value: cat.Key, Label: cat.Icon + " " + cat.Label, Selected: p.category == cat.Key})
	}
	return out
}

// setSearch filters the list as the person types. The search box is a
// named field in a form with on:change, so after a server restart the
// browser restores it and sends it again.
func (p *GroupPage) setSearch(c *loom.Ctx, payload loom.Payload) {
	if q, ok := payload["q"]; ok {
		p.search = q // the form's change or submit
	} else {
		p.search = payload["value"] // the input's own input event
	}
}

func (p *GroupPage) filterCategory(c *loom.Ctx, payload loom.Payload) {
	p.category = payload["category"]
}

// The add-expense dialog.

func (p *GroupPage) openComposer(c *loom.Ctx, _ loom.Payload) {
	members, err := p.app.store.Members(c.Context(), p.groupID)
	must(err)
	in := ExpenseInput{
		Date: p.app.store.now().Format("2006-01-02"), Category: "general", PaidBy: p.me.ID, Split: "equal",
		Among: map[string]bool{}, Exact: map[string]string{},
	}
	for _, m := range members {
		in.Among[m.ID] = true
		in.Exact[m.ID] = ""
	}
	p.composing, p.expense, p.expenseFail = true, form.New(in), ""
}

// expenseForm keeps the dialog's state as the person fills it in, so the
// split's shares and what's left to assign update as they type.
func (p *GroupPage) expenseForm(c *loom.Ctx, payload loom.Payload) {
	p.expense.Change(payload)
}

func (p *GroupPage) expenseDialog(members []Person) ExpenseDialog {
	in := p.expense.Value()
	v := ExpenseDialog{Exact: in.Split == "exact", Fail: p.expenseFail}
	for _, cat := range categories {
		v.Categories = append(v.Categories, Option{Value: cat.Key, Label: cat.Icon + "  " + cat.Label, Selected: cat.Key == in.Category})
	}
	for _, m := range members {
		label := m.Name
		if m.ID == p.me.ID {
			label = "You"
		}
		v.PaidBy = append(v.PaidBy, Option{Value: m.ID, Label: label, Selected: m.ID == in.PaidBy})
	}
	cents, _ := parseCents(in.Amount)
	shares := shares(in, members, cents)
	var assigned int64
	for _, m := range members {
		row := SplitRow{Person: m, Included: in.Among[m.ID]}
		if s, ok := shares[m.ID]; ok && !v.Exact {
			row.Share = money(s)
		}
		if c, ok := parseCents(in.Exact[m.ID]); ok {
			assigned += c
		}
		v.Split = append(v.Split, row)
	}
	v.Remaining = money(cents - assigned)
	v.Balanced = cents == assigned
	if cents-assigned < 0 {
		v.Remaining = "-" + v.Remaining
	}
	return v
}

// shares works out each person's share of cents under the input's split,
// in the members' order so leftover cents go to the same people each time.
func shares(in ExpenseInput, members []Person, cents int64) map[string]int64 {
	if in.Split == "exact" {
		out := map[string]int64{}
		for _, m := range members {
			if v, ok := parseCents(in.Exact[m.ID]); ok && v > 0 {
				out[m.ID] = v
			}
		}
		return out
	}
	var among []string
	for _, m := range members {
		if in.Among[m.ID] {
			among = append(among, m.ID)
		}
	}
	return splitEqually(cents, among)
}

func (p *GroupPage) saveExpense(c *loom.Ctx, payload loom.Payload) {
	in, ok := p.expense.Submit(payload)
	if !ok {
		return
	}
	members, err := p.app.store.Members(c.Context(), p.groupID)
	must(err)
	cents, _ := parseCents(in.Amount)
	spent, _ := time.Parse("2006-01-02", in.Date)
	description := strings.TrimSpace(in.Description)
	_, err = p.app.store.AddExpense(c.Context(), NewExpense{
		GroupID: p.groupID, Description: description, Category: category(in.Category).Key,
		Cents: cents, PaidBy: in.PaidBy, SpentOn: spent, Shares: shares(*in, members, cents), By: p.me.ID,
	})
	if err != nil {
		c.Fail(err)
		p.expenseFail = "Couldn't save the expense; try again."
		return
	}
	p.composing = false
	p.showToast("Added “"+description+"”", "success")
}

// The record-a-payment dialog.

// openSettle opens the dialog, filled in from a suggested payment when
// one was clicked, or as the person paying the first one they owe.
func (p *GroupPage) openSettle(c *loom.Ctx, payload loom.Payload) {
	in := SettleInput{From: payload["from"], To: payload["to"], Amount: payload["amount"]}
	if in.From == "" {
		in.From = p.me.ID
		nets, err := p.app.store.Nets(c.Context(), p.groupID)
		must(err)
		for _, pay := range payments(nets) {
			if pay.From == p.me.ID {
				in.To, in.Amount = pay.To, strings.TrimPrefix(money(pay.Cents), "$")
				break
			}
		}
	}
	p.settling, p.settle, p.settleFail = true, form.New(in), ""
}

func (p *GroupPage) settleForm(c *loom.Ctx, payload loom.Payload) {
	p.settle.Change(payload)
}

func (p *GroupPage) settleDialog(members []Person) SettleDialog {
	in := p.settle.Value()
	v := SettleDialog{Fail: p.settleFail}
	for _, m := range members {
		label := m.Name
		if m.ID == p.me.ID {
			label = "You"
		}
		v.From = append(v.From, Option{Value: m.ID, Label: label, Selected: m.ID == in.From})
		v.To = append(v.To, Option{Value: m.ID, Label: label, Selected: m.ID == in.To})
	}
	return v
}

func (p *GroupPage) saveSettle(c *loom.Ctx, payload loom.Payload) {
	in, ok := p.settle.Submit(payload)
	if !ok {
		return
	}
	cents, _ := parseCents(in.Amount)
	if err := p.app.store.Settle(c.Context(), p.groupID, in.From, in.To, cents, p.me.ID); err != nil {
		c.Fail(err)
		p.settleFail = "Couldn't record the payment; try again."
		return
	}
	p.settling = false
	p.showToast("Payment of "+money(cents)+" recorded", "success")
}

// remind sends a reminder to someone who owes the person.
func (p *GroupPage) remind(c *loom.Ctx, payload loom.Payload) {
	cents := id(payload["cents"])
	debtor, err := p.app.store.Person(c.Context(), payload["debtor"])
	must(err)
	dup, err := p.app.store.Remind(c.Context(), p.groupID, p.me.ID, debtor.ID, cents)
	if err != nil {
		c.Fail(err)
		p.showToast("Couldn't send the reminder; try again.", "info")
		return
	}
	if dup {
		p.showToast("You already reminded "+debtor.First+" in the last hour", "info")
		return
	}
	p.showToast("Reminder sent to "+debtor.First, "success")
}

func (p *GroupPage) showToast(text, tone string) {
	p.toast = Toast{ID: p.toast.ID + 1, Text: text, Tone: tone}
}
