package tally

import (
	"context"
	"fmt"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
)

// The background jobs. Each runs at least once, so each is safe to run
// twice: notifications carry the job's id, unique per person, and a
// recurring bill records the day it was last posted in the same
// transaction as the expense.

// Notify tells a group's other members that someone did something in it.
type Notify struct {
	GroupID   int64  `json:"group_id"`
	Actor     string `json:"actor"`
	Body      string `json:"body"`       // "added “Pizza night”, $42.60"
	ExpenseID int64  `json:"expense_id"` // 0 when it isn't about one expense

	Store *Store `json:"-"`
}

func (Notify) Options() jobs.Options {
	return jobs.Options{Queue: "notifications", MaxAttempts: 5, Timeout: 10 * time.Second}
}

func (j Notify) Perform(ctx context.Context, c *jobs.Ctx) error {
	actor, err := j.Store.Person(ctx, j.Actor)
	if err != nil {
		return jobs.Cancel(err)
	}
	_, err = c.DB().Exec(ctx, `
		insert into notifications (person_id, group_id, body, job_id)
		select m.person_id, $1, $2, $3 from members m
		where m.group_id = $1 and m.person_id <> $4
		on conflict (job_id, person_id) do nothing`,
		j.GroupID, actor.First+" "+j.Body, c.ID(), j.Actor)
	return err
}

// Remind nudges a member who owes another.
type Remind struct {
	GroupID  int64  `json:"group_id"`
	Creditor string `json:"creditor"`
	Debtor   string `json:"debtor"`
	Cents    int64  `json:"cents"`

	Store *Store `json:"-"`
}

func (Remind) Options() jobs.Options {
	return jobs.Options{Queue: "notifications", MaxAttempts: 5, Timeout: 10 * time.Second}
}

func (j Remind) Perform(ctx context.Context, c *jobs.Ctx) error {
	creditor, err := j.Store.Person(ctx, j.Creditor)
	if err != nil {
		return jobs.Cancel(err)
	}
	_, err = c.DB().Exec(ctx, `
		insert into notifications (person_id, group_id, body, job_id) values ($1, $2, $3, $4)
		on conflict (job_id, person_id) do nothing`,
		j.Debtor, j.GroupID, fmt.Sprintf("%s sent a reminder: you owe them %s", creditor.First, money(j.Cents)), c.ID())
	return err
}

// PostRecurring posts each monthly bill that is due: its day of the
// month has come and it hasn't been posted this month. It runs daily on
// cron; running it twice posts nothing new, because the bill's
// last_posted moves in the same transaction as the expense.
type PostRecurring struct {
	Store *Store `json:"-"`
}

func (j PostRecurring) Perform(ctx context.Context, c *jobs.Ctx) error {
	today := c.Now().UTC()
	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	due, err := loom.Select[recurringRec](ctx, c.DB(), `
		select * from recurring
		where day_of_month <= $1 and (last_posted is null or last_posted < $2)`, today.Day(), month)
	if err != nil {
		return err
	}
	for _, r := range due {
		if err := j.post(ctx, c, r, month); err != nil {
			return err
		}
	}
	return nil
}

func (j PostRecurring) post(ctx context.Context, c *jobs.Ctx, r recurringRec, month time.Time) error {
	return c.DB().Tx(ctx, func(tx loom.Tx) error {
		// Claim the bill for this month first: if another run got here
		// first, nothing is updated and nothing is posted.
		res, err := tx.Exec(ctx, `update recurring set last_posted = $2 where id = $1 and (last_posted is null or last_posted < $2)`, r.ID, month)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		members, err := loom.Select[string](ctx, tx, `select person_id from members where group_id = $1 order by person_id`, r.GroupID)
		if err != nil {
			return err
		}
		id := r.ID
		e := NewExpense{
			GroupID: r.GroupID, Description: r.Description, Category: r.Category, Cents: r.AmountCents,
			PaidBy: r.PaidBy, SpentOn: month.AddDate(0, 0, r.DayOfMonth-1), By: r.PaidBy,
			Shares: splitEqually(r.AmountCents, members), RecurringID: &id,
		}
		eid, err := addExpense(ctx, tx, e)
		if err != nil {
			return err
		}
		_, err = jobs.Enqueue(tx, Notify{GroupID: r.GroupID, Actor: r.PaidBy, ExpenseID: eid,
			Body: fmt.Sprintf("posted the monthly “%s”, %s", r.Description, money(r.AmountCents))})
		return err
	})
}
