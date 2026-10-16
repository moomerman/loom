package tally

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
)

// Store holds every query and write. Each write is one transaction that
// changes the rows and enqueues the job that tells the other members;
// pages watch the tables (loom.Watch), so they re-render on commit.
type Store struct {
	DB  *loom.DB
	Now func() time.Time // the clock, for "2 days ago" and recurring bills
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// The rows as they are in the database. The rows templates render are
// in rows.go.

type personRec struct {
	ID    string
	Name  string
	Email string
	Color string
}

type groupRec struct {
	ID        int64
	Name      string
	Emoji     string
	Color     string
	CreatedBy string
	CreatedAt time.Time
}

type expenseRec struct {
	ID          int64
	GroupID     int64
	Description string
	Category    string
	AmountCents int64
	PaidBy      string
	SpentOn     time.Time
	Note        string
	CreatedBy   string
	CreatedAt   time.Time
	RecurringID *int64
	MyShare     int64 // the signed-in person's share, from the query
}

type shareRec struct {
	PersonID    string
	AmountCents int64
}

type netRec struct {
	PersonID string
	Net      int64
}

// ErrNotFound is the data layer's not-found error.
var ErrNotFound = loom.ErrNotFound

func (r personRec) person() Person { return person(r.ID, r.Name, r.Email, r.Color) }

// People returns everyone, by name.
func (s *Store) People(ctx context.Context) ([]Person, error) {
	recs, err := loom.Select[personRec](ctx, s.DB, `select id, name, email, color from people order by name`)
	return people(recs), err
}

// Person returns the person with the id.
func (s *Store) Person(ctx context.Context, id string) (Person, error) {
	r, err := loom.Get[personRec](ctx, s.DB, `select id, name, email, color from people where id = $1`, id)
	return r.person(), err
}

// Members returns a group's members, by name.
func (s *Store) Members(ctx context.Context, groupID int64) ([]Person, error) {
	recs, err := loom.Select[personRec](ctx, s.DB, `
		select p.id, p.name, p.email, p.color from people p
		join members m on m.person_id = p.id
		where m.group_id = $1 order by p.name`, groupID)
	return people(recs), err
}

func people(recs []personRec) []Person {
	out := make([]Person, len(recs))
	for i, r := range recs {
		out[i] = r.person()
	}
	return out
}

// Group returns a group the person belongs to: ErrNotFound for anyone else.
func (s *Store) Group(ctx context.Context, id int64, me string) (groupRec, error) {
	return loom.Get[groupRec](ctx, s.DB, `
		select g.id, g.name, g.emoji, g.color, g.created_by, g.created_at from groups g
		join members m on m.group_id = g.id and m.person_id = $2
		where g.id = $1`, id, me)
}

// Groups returns the person's groups, with their balance in each,
// most recently active first.
func (s *Store) Groups(ctx context.Context, me string) ([]GroupCard, error) {
	type row struct {
		groupRec
		Expenses int
		Net      int64
		Active   time.Time
	}
	rows, err := loom.Select[row](ctx, s.DB, `
		select g.id, g.name, g.emoji, g.color, g.created_by, g.created_at,
			(select count(*) from expenses e where e.group_id = g.id)::int as expenses,
			coalesce((select sum(amt) from (`+netTerms+`) t where person_id = $1), 0)::bigint as net,
			greatest(g.created_at, (select max(created_at) from expenses e where e.group_id = g.id)) as active
		from groups g join members m on m.group_id = g.id and m.person_id = $1
		order by active desc, g.id`, me)
	if err != nil {
		return nil, err
	}
	cards := make([]GroupCard, len(rows))
	for i, r := range rows {
		members, err := s.Members(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		cards[i] = GroupCard{
			ID: int(r.ID), Name: r.Name, Emoji: r.Emoji, Color: r.Color,
			Href: fmt.Sprintf("/groups/%d", r.ID), Members: members,
			Expenses: r.Expenses, Balance: balance(r.Net),
		}
	}
	return cards, nil
}

// netTerms lists, for group g.id, every amount that moves a member's
// balance: what they paid (+), their shares (-), settlements they sent
// (+) and received (-). Summed per person it is their net balance,
// positive when the group owes them.
const netTerms = `
	select paid_by as person_id, amount_cents as amt from expenses where group_id = g.id
	union all
	select s.person_id, -s.amount_cents from shares s join expenses e on e.id = s.expense_id where e.group_id = g.id
	union all
	select from_person, amount_cents from settlements where group_id = g.id
	union all
	select to_person, -amount_cents from settlements where group_id = g.id`

// Nets returns each member's net balance in a group.
func (s *Store) Nets(ctx context.Context, groupID int64) (map[string]int64, error) {
	recs, err := loom.Select[netRec](ctx, s.DB, `
		select m.person_id, coalesce(sum(t.amt), 0)::bigint as net
		from groups g
		join members m on m.group_id = g.id
		left join lateral (`+netTerms+`) t on t.person_id = m.person_id
		where g.id = $1
		group by m.person_id`, groupID)
	nets := map[string]int64{}
	for _, r := range recs {
		nets[r.PersonID] = r.Net
	}
	return nets, err
}

// ExpenseFilter narrows a group's expense list.
type ExpenseFilter struct {
	Search   string
	Category string
}

// Expenses returns a group's expenses, newest first, with the person's
// share of each.
func (s *Store) Expenses(ctx context.Context, groupID int64, me string, f ExpenseFilter) ([]expenseRec, error) {
	return loom.Select[expenseRec](ctx, s.DB, `
		select e.*, coalesce(s.amount_cents, 0) as my_share
		from expenses e left join shares s on s.expense_id = e.id and s.person_id = $2
		where e.group_id = $1
			and ($3 = '' or e.description ilike '%' || $3 || '%')
			and ($4 = '' or e.category = $4)
		order by e.spent_on desc, e.id desc
		limit 300`, groupID, me, strings.TrimSpace(f.Search), f.Category)
}

// Expense returns one expense of a group the person belongs to, and its shares.
func (s *Store) Expense(ctx context.Context, id int64, me string) (expenseRec, []shareRec, error) {
	e, err := loom.Get[expenseRec](ctx, s.DB, `
		select e.*, coalesce(s.amount_cents, 0) as my_share
		from expenses e
		join members m on m.group_id = e.group_id and m.person_id = $2
		left join shares s on s.expense_id = e.id and s.person_id = $2
		where e.id = $1`, id, me)
	if err != nil {
		return e, nil, err
	}
	shares, err := loom.Select[shareRec](ctx, s.DB, `
		select person_id, amount_cents from shares where expense_id = $1 order by amount_cents desc, person_id`, id)
	return e, shares, err
}

// CategoryTotals returns a group's spending per category, largest first.
func (s *Store) CategoryTotals(ctx context.Context, groupID int64) ([]CategoryTotal, int64, error) {
	type row struct {
		Category string
		Total    int64
	}
	rows, err := loom.Select[row](ctx, s.DB, `
		select category, sum(amount_cents)::bigint as total from expenses
		where group_id = $1 group by category order by total desc, category`, groupID)
	if err != nil {
		return nil, 0, err
	}
	var all, top int64
	for _, r := range rows {
		all += r.Total
		top = max(top, r.Total)
	}
	out := make([]CategoryTotal, len(rows))
	for i, r := range rows {
		out[i] = CategoryTotal{
			Category: category(r.Category), Amount: money(r.Total),
			Percent: int((r.Total*100 + all/2) / max(all, 1)),
			Bar:     int(r.Total * 100 / max(top, 1)),
		}
	}
	return out, all, nil
}

type recurringRec struct {
	ID          int64
	GroupID     int64
	Description string
	Category    string
	AmountCents int64
	PaidBy      string
	DayOfMonth  int
	LastPosted  *time.Time
}

// Recurring returns a group's monthly bills.
func (s *Store) Recurring(ctx context.Context, groupID int64) ([]recurringRec, error) {
	return loom.Select[recurringRec](ctx, s.DB, `
		select * from recurring where group_id = $1 order by day_of_month, id`, groupID)
}

// NewExpense is an expense to add, with how it's split.
type NewExpense struct {
	GroupID     int64
	Description string
	Category    string
	Cents       int64
	PaidBy      string
	SpentOn     time.Time
	Note        string
	Shares      map[string]int64 // person -> cents; adds up to Cents
	By          string
	RecurringID *int64
}

// AddExpense adds an expense and its shares, and enqueues a
// notification for the other members, in one transaction.
func (s *Store) AddExpense(ctx context.Context, e NewExpense) (int64, error) {
	var sum int64
	for _, c := range e.Shares {
		sum += c
	}
	if sum != e.Cents {
		return 0, fmt.Errorf("the shares add up to %s, not %s", money(sum), money(e.Cents))
	}
	var id int64
	err := s.DB.Tx(ctx, func(tx loom.Tx) error {
		var err error
		id, err = addExpense(ctx, tx, e)
		if err != nil {
			return err
		}
		verb := "added"
		if e.RecurringID != nil {
			verb = "posted the monthly"
		}
		_, err = jobs.Enqueue(tx, Notify{
			GroupID: e.GroupID, Actor: e.By, ExpenseID: id,
			Body: fmt.Sprintf("%s “%s”, %s", verb, e.Description, money(e.Cents)),
		})
		return err
	})
	return id, err
}

func addExpense(ctx context.Context, tx loom.Tx, e NewExpense) (int64, error) {
	id, err := loom.Get[int64](ctx, tx, `
		insert into expenses (group_id, description, category, amount_cents, paid_by, spent_on, note, created_by, recurring_id)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9) returning id`,
		e.GroupID, e.Description, e.Category, e.Cents, e.PaidBy, e.SpentOn, e.Note, e.By, e.RecurringID)
	if err != nil {
		return 0, err
	}
	for who, cents := range e.Shares {
		if cents == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `insert into shares (expense_id, person_id, amount_cents) values ($1, $2, $3)`, id, who, cents); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// DeleteExpense deletes an expense of a group the person belongs to.
func (s *Store) DeleteExpense(ctx context.Context, id int64, me string) error {
	return s.DB.Tx(ctx, func(tx loom.Tx) error {
		e, err := loom.Get[expenseRec](ctx, tx, `
			select e.*, 0::bigint as my_share from expenses e
			join members m on m.group_id = e.group_id and m.person_id = $2
			where e.id = $1`, id, me)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from expenses where id = $1`, id); err != nil {
			return err
		}
		_, err = jobs.Enqueue(tx, Notify{GroupID: e.GroupID, Actor: me, Body: fmt.Sprintf("deleted “%s”, %s", e.Description, money(e.AmountCents))})
		return err
	})
}

// Settle records a payment from one member to another.
func (s *Store) Settle(ctx context.Context, groupID int64, from, to string, cents int64, by string) error {
	if from == to {
		return errors.New("pick two different people")
	}
	return s.DB.Tx(ctx, func(tx loom.Tx) error {
		if _, err := tx.Exec(ctx, `
			insert into settlements (group_id, from_person, to_person, amount_cents, created_by)
			values ($1, $2, $3, $4, $5)`, groupID, from, to, cents, by); err != nil {
			return err
		}
		names, err := loom.Select[personRec](ctx, tx, `select id, name, email, color from people where id = any($1)`, []string{from, to})
		if err != nil {
			return err
		}
		first := map[string]string{}
		for _, n := range names {
			first[n.ID] = n.person().First
		}
		_, err = jobs.Enqueue(tx, Notify{GroupID: groupID, Actor: by,
			Body: fmt.Sprintf("recorded %s paying %s %s", first[from], first[to], money(cents))})
		return err
	})
}

// Remind enqueues a reminder to a member who owes another, at most once
// an hour per pair and amount. duplicate is true when one was already sent.
func (s *Store) Remind(ctx context.Context, groupID int64, from, to string, cents int64) (duplicate bool, err error) {
	err = s.DB.Tx(ctx, func(tx loom.Tx) error {
		res, err := jobs.Enqueue(tx, Remind{GroupID: groupID, Creditor: from, Debtor: to, Cents: cents}, jobs.Unique(time.Hour))
		duplicate = res.Duplicate
		return err
	})
	return duplicate, err
}

// CreateGroup creates a group with its members, the creator among them.
func (s *Store) CreateGroup(ctx context.Context, name, emoji, color string, members []string, by string) (int64, error) {
	var id int64
	err := s.DB.Tx(ctx, func(tx loom.Tx) error {
		var err error
		id, err = loom.Get[int64](ctx, tx, `insert into groups (name, emoji, color, created_by) values ($1, $2, $3, $4) returning id`, name, emoji, color, by)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, m := range append([]string{by}, members...) {
			if seen[m] {
				continue
			}
			seen[m] = true
			if _, err := tx.Exec(ctx, `insert into members (group_id, person_id) values ($1, $2)`, id, m); err != nil {
				return err
			}
		}
		_, err = jobs.Enqueue(tx, Notify{GroupID: id, Actor: by, Body: "added you to " + emoji + " " + name})
		return err
	})
	return id, err
}

type activityRec struct {
	Kind        string // expense or settlement
	ID          int64
	GroupID     int64
	GroupName   string
	GroupEmoji  string
	Actor       string
	Title       string
	AmountCents int64
	Mine        int64 // the change to the person's balance
	At          time.Time
}

// Activity returns the latest expenses and settlements in the person's
// groups, newest first.
func (s *Store) Activity(ctx context.Context, me string, groupID int64, limit int) ([]activityRec, error) {
	return loom.Select[activityRec](ctx, s.DB, `
		select * from (
			select 'expense' as kind, e.id, e.group_id, g.name as group_name, g.emoji as group_emoji,
				e.created_by as actor, e.description as title, e.amount_cents,
				(case when e.paid_by = $1 then e.amount_cents else 0 end) - coalesce(sh.amount_cents, 0) as mine,
				e.created_at as at
			from expenses e
			join groups g on g.id = e.group_id
			join members m on m.group_id = g.id and m.person_id = $1
			left join shares sh on sh.expense_id = e.id and sh.person_id = $1
			where $2 = 0 or e.group_id = $2
			union all
			select 'settlement', st.id, st.group_id, g.name, g.emoji, st.created_by,
				split_part(fp.name, ' ', 1) || ' paid ' || split_part(tp.name, ' ', 1), st.amount_cents,
				(case when st.from_person = $1 then st.amount_cents when st.to_person = $1 then -st.amount_cents else 0 end),
				st.created_at
			from settlements st
			join groups g on g.id = st.group_id
			join members m on m.group_id = g.id and m.person_id = $1
			join people fp on fp.id = st.from_person
			join people tp on tp.id = st.to_person
			where $2 = 0 or st.group_id = $2
		) a order by at desc, id desc limit $3`, me, groupID, limit)
}

type notificationRec struct {
	ID         int64
	Body       string
	GroupID    *int64
	GroupName  *string
	GroupEmoji *string
	Read       bool
	CreatedAt  time.Time
}

// Notifications returns the person's notifications, newest first.
func (s *Store) Notifications(ctx context.Context, me string) ([]notificationRec, error) {
	return loom.Select[notificationRec](ctx, s.DB, `
		select n.id, n.body, n.group_id, g.name as group_name, g.emoji as group_emoji, n.read, n.created_at
		from notifications n left join groups g on g.id = n.group_id
		where n.person_id = $1 order by n.created_at desc, n.id desc limit 100`, me)
}

// Unread counts the person's unread notifications.
func (s *Store) Unread(ctx context.Context, me string) (int, error) {
	return loom.Get[int](ctx, s.DB, `select count(*)::int from notifications where person_id = $1 and not read`, me)
}

// MarkRead marks all the person's notifications read.
func (s *Store) MarkRead(ctx context.Context, me string) error {
	_, err := s.DB.Exec(ctx, `update notifications set read = true where person_id = $1 and not read`, me)
	return err
}

// payments works out the fewest payments that settle a group up: the
// largest debtor pays the largest creditor, repeatedly.
func payments(nets map[string]int64) []struct {
	From, To string
	Cents    int64
} {
	type bal struct {
		id    string
		cents int64
	}
	var debtors, creditors []bal
	for id, n := range nets {
		switch {
		case n < 0:
			debtors = append(debtors, bal{id, -n})
		case n > 0:
			creditors = append(creditors, bal{id, n})
		}
	}
	byAmount := func(b []bal) {
		sort.Slice(b, func(i, j int) bool {
			if b[i].cents != b[j].cents {
				return b[i].cents > b[j].cents
			}
			return b[i].id < b[j].id
		})
	}
	var out []struct {
		From, To string
		Cents    int64
	}
	for len(debtors) > 0 && len(creditors) > 0 {
		byAmount(debtors)
		byAmount(creditors)
		d, c := &debtors[0], &creditors[0]
		pay := min(d.cents, c.cents)
		out = append(out, struct {
			From, To string
			Cents    int64
		}{d.id, c.id, pay})
		d.cents -= pay
		c.cents -= pay
		if d.cents == 0 {
			debtors = debtors[1:]
		}
		if c.cents == 0 {
			creditors = creditors[1:]
		}
	}
	return out
}

// splitEqually splits cents among people, giving leftover cents to the
// first ones so the shares add up exactly.
func splitEqually(cents int64, among []string) map[string]int64 {
	out := map[string]int64{}
	if len(among) == 0 {
		return out
	}
	each, left := cents/int64(len(among)), cents%int64(len(among))
	for i, id := range among {
		out[id] = each
		if int64(i) < left {
			out[id]++
		}
	}
	return out
}
