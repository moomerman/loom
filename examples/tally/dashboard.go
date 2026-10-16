package tally

import (
	"fmt"
	"strings"

	"github.com/moomerman/loom"
)

// DashboardPage is the home page: what the person owes and is owed
// overall, their groups, the latest activity, and a dialog to start a
// new group. It watches the tables it reads (app.go), so a friend's new
// expense shows up here as it is added.
type DashboardPage struct {
	app   *App
	me    Person
	toast Toast

	// The new-group dialog.
	creating bool
	form     NewGroupForm
}

// NewGroupForm is the new-group dialog as it renders.
type NewGroupForm struct {
	Name    string
	Emojis  []Option
	Members []MemberChoice
	Error   string
}

// MemberChoice is a person to add to a new group.
type MemberChoice struct {
	Person  Person
	Checked bool
}

// groupEmojis are the icons a new group can have, each with its color.
var groupEmojis = []struct{ emoji, color string }{
	{"🏠", "teal"}, {"✈️", "sky"}, {"🌊", "indigo"}, {"🍕", "orange"}, {"🎉", "pink"},
	{"⛰️", "red"}, {"⛺", "green"}, {"🎿", "violet"}, {"💼", "slate"},
}

func (p *DashboardPage) Mount(c *loom.Ctx) {
	me, err := p.app.me(c)
	must(err)
	p.me = me
	c.Set("section", "dashboard")
	c.Title("Dashboard")
}

func (p *DashboardPage) Events() loom.Events {
	return loom.Events{
		"new_group":    p.openNewGroup,
		"close_group":  func(c *loom.Ctx, _ loom.Payload) { p.creating = false },
		"group_form":   p.groupForm,
		"create_group": p.createGroup,
		"toast_close":  func(c *loom.Ctx, _ loom.Payload) { p.toast.Text = "" },
	}
}

func (p *DashboardPage) Assigns(c *loom.Ctx) DashboardAssigns {
	ctx := c.Context()
	groups, err := p.app.store.Groups(ctx, p.me.ID)
	must(err)
	var owe, owed int64
	for _, g := range groups {
		if g.Balance.Cents < 0 {
			owe -= int64(g.Balance.Cents)
		} else {
			owed += int64(g.Balance.Cents)
		}
	}
	recs, err := p.app.store.Activity(ctx, p.me.ID, 0, 8)
	must(err)
	people, err := p.app.store.People(ctx)
	must(err)
	return DashboardAssigns{
		Me:       p.me,
		Greeting: greeting(p.app.store.now().Hour()) + ", " + p.me.First,
		Today:    p.app.store.now().Format("Monday, January 2"),
		Total:    balance(owed - owe),
		Owe:      money(owe),
		Owed:     money(owed),
		Groups:   groups,
		Activity: p.app.activityRows(recs, p.me.ID, people),
		Creating: p.creating,
		Form:     p.form,
		Toast:    p.toast,
	}
}

// greeting says good morning, afternoon or evening.
func greeting(hour int) string {
	switch {
	case hour < 12:
		return "Good morning"
	case hour < 18:
		return "Good afternoon"
	}
	return "Good evening"
}

func (p *DashboardPage) openNewGroup(c *loom.Ctx, _ loom.Payload) {
	people, err := p.app.store.People(c.Context())
	must(err)
	p.creating = true
	p.form = NewGroupForm{}
	for i, e := range groupEmojis {
		p.form.Emojis = append(p.form.Emojis, Option{Value: e.emoji, Label: e.emoji, Selected: i == 0})
	}
	for _, person := range people {
		if person.ID != p.me.ID {
			p.form.Members = append(p.form.Members, MemberChoice{Person: person})
		}
	}
}

// groupForm keeps the dialog's state as the person fills it in, so a
// re-render (a friend's change, a remount) keeps what they picked.
func (p *DashboardPage) groupForm(c *loom.Ctx, payload loom.Payload) {
	p.form.Name, p.form.Error = payload["name"], ""
	for i := range p.form.Emojis {
		p.form.Emojis[i].Selected = p.form.Emojis[i].Value == payload["emoji"]
	}
	for i := range p.form.Members {
		p.form.Members[i].Checked = payload["member_"+p.form.Members[i].Person.ID] != ""
	}
}

func (p *DashboardPage) createGroup(c *loom.Ctx, payload loom.Payload) {
	p.groupForm(c, payload)
	name := strings.TrimSpace(p.form.Name)
	var members []string
	for _, m := range p.form.Members {
		if m.Checked {
			members = append(members, m.Person.ID)
		}
	}
	switch {
	case name == "":
		p.form.Error = "Give the group a name."
		return
	case len(members) == 0:
		p.form.Error = "Add at least one other person."
		return
	}
	emoji, color := groupEmojis[0].emoji, groupEmojis[0].color
	for _, e := range groupEmojis {
		if e.emoji == payload["emoji"] {
			emoji, color = e.emoji, e.color
		}
	}
	id, err := p.app.store.CreateGroup(c.Context(), name, emoji, color, members, p.me.ID)
	if err != nil {
		c.Fail(err)
		p.form.Error = "Couldn't create the group; try again."
		return
	}
	p.creating = false
	c.Navigate(fmt.Sprintf("/groups/%d", id))
}

// activityRows turns activity records into rows for a feed.
func (a *App) activityRows(recs []activityRec, me string, people []Person) []ActivityRow {
	byID := map[string]Person{}
	for _, p := range people {
		byID[p.ID] = p
	}
	now := a.store.now()
	rows := make([]ActivityRow, len(recs))
	for i, r := range recs {
		row := ActivityRow{
			Key:    fmt.Sprintf("%s-%d", r.Kind, r.ID),
			Href:   fmt.Sprintf("/groups/%d", r.GroupID),
			Actor:  byID[r.Actor],
			Group:  r.GroupEmoji + " " + r.GroupName,
			Amount: money(r.AmountCents),
			Mine:   balance(r.Mine),
			When:   ago(r.At, now),
		}
		who := row.Actor.First
		if r.Actor == me {
			who = "You"
		}
		if r.Kind == "settlement" {
			row.Settled = true
			row.Text = who + " recorded a payment: " + r.Title
			row.Mine.Label = map[string]string{"owed": "you paid", "owe": "you received", "settled": ""}[row.Mine.Tone]
		} else {
			row.Href = fmt.Sprintf("/groups/%d/expenses/%d", r.GroupID, r.ID)
			row.Text = who + " added “" + r.Title + "”"
			row.Mine.Label = map[string]string{"owed": "you lent", "owe": "you borrowed", "settled": "not involved"}[row.Mine.Tone]
		}
		rows[i] = row
	}
	return rows
}
