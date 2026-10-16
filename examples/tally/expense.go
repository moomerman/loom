package tally

import (
	"errors"
	"fmt"

	"github.com/moomerman/loom"
)

// ExpensePage is one expense: who paid, how it was split, and a way to
// delete it. If someone else deletes it while it's open, the page says
// so (it watches expenses).
type ExpensePage struct {
	app        *App
	me         Person
	groupID    int64
	expenseID  int64
	confirming bool
}

// ExpenseDetail is the expense as its page shows it.
type ExpenseDetail struct {
	Title    string
	Amount   string
	Category Category
	Date     string
	PaidBy   Person
	Payer    string // "You paid" or "Priya paid"
	AddedBy  string // "Added by Priya · 3 days ago"
	Note     string
	Mine     Amount
	MineNote string
	Recurs   bool
}

func (p *ExpensePage) Mount(c *loom.Ctx) {
	me, err := p.app.me(c)
	must(err)
	p.me = me
	p.groupID = id(c.Request.PathValue("id"))
	p.expenseID = id(c.Request.PathValue("eid"))
	c.Set("section", fmt.Sprintf("group:%d", p.groupID))
}

func (p *ExpensePage) Events() loom.Events {
	return loom.Events{
		"delete":         func(c *loom.Ctx, _ loom.Payload) { p.confirming = true },
		"cancel_delete":  func(c *loom.Ctx, _ loom.Payload) { p.confirming = false },
		"confirm_delete": p.delete,
	}
}

func (p *ExpensePage) Assigns(c *loom.Ctx) ExpenseViewAssigns {
	ctx, store := c.Context(), p.app.store
	g, err := store.Group(ctx, p.groupID, p.me.ID)
	if errors.Is(err, ErrNotFound) {
		c.Title("Not found")
		return ExpenseViewAssigns{Missing: true, Back: "/"}
	}
	must(err)
	back := fmt.Sprintf("/groups/%d", g.ID)
	head := GroupHead{ID: int(g.ID), Name: g.Name, Emoji: g.Emoji, Color: g.Color}
	e, shares, err := store.Expense(ctx, p.expenseID, p.me.ID)
	if errors.Is(err, ErrNotFound) || (err == nil && e.GroupID != p.groupID) {
		c.Title("Expense deleted")
		return ExpenseViewAssigns{Missing: true, Group: head, Back: back}
	}
	must(err)
	c.Title(e.Description)
	people, err := store.People(ctx)
	must(err)
	byID := map[string]Person{}
	for _, person := range people {
		byID[person.ID] = person
	}
	d := ExpenseDetail{
		Title: e.Description, Amount: money(e.AmountCents), Category: category(e.Category),
		Date: e.SpentOn.Format("Monday, January 2, 2006"), PaidBy: byID[e.PaidBy],
		Payer:   byID[e.PaidBy].First + " paid",
		AddedBy: "Added by " + byID[e.CreatedBy].First + " · " + ago(e.CreatedAt, store.now()),
		Note:    e.Note, Recurs: e.RecurringID != nil,
	}
	if e.PaidBy == p.me.ID {
		d.Payer = "You paid"
	}
	if e.RecurringID != nil {
		d.AddedBy = "Posted automatically · monthly bill"
	}
	row := (&GroupPage{me: p.me}).expenseRow(e, byID)
	d.Mine, d.MineNote = row.Mine, row.MineNote
	rows := make([]ShareRow, len(shares))
	for i, s := range shares {
		rows[i] = ShareRow{
			Person: byID[s.PersonID], Amount: money(s.AmountCents), Paid: s.PersonID == e.PaidBy,
			Percent: int((s.AmountCents*100 + e.AmountCents/2) / e.AmountCents),
		}
	}
	return ExpenseViewAssigns{Group: head, Back: back, Expense: d, Shares: rows, Confirming: p.confirming}
}

func (p *ExpensePage) delete(c *loom.Ctx, _ loom.Payload) {
	if err := p.app.store.DeleteExpense(c.Context(), p.expenseID, p.me.ID); err != nil && !errors.Is(err, ErrNotFound) {
		c.Fail(err)
		return
	}
	c.Navigate(fmt.Sprintf("/groups/%d", p.groupID))
}
