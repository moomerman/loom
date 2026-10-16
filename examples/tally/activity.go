package tally

import (
	"fmt"

	"github.com/moomerman/loom"
)

// ActivityPage is the person's notifications, written by the Notify and
// Remind jobs, with the latest activity in their groups beside them.
// Unread ones stand out until the person marks them read.
type ActivityPage struct {
	app *App
	me  Person
}

func (p *ActivityPage) Mount(c *loom.Ctx) {
	me, err := p.app.me(c)
	must(err)
	p.me = me
	c.Set("section", "activity")
	c.Title("Activity")
}

func (p *ActivityPage) Events() loom.Events {
	return loom.Events{"mark_read": p.markRead}
}

func (p *ActivityPage) markRead(c *loom.Ctx, _ loom.Payload) {
	if err := p.app.store.MarkRead(c.Context(), p.me.ID); err != nil {
		c.Fail(err)
	}
}

func (p *ActivityPage) Assigns(c *loom.Ctx) ActivityAssigns {
	ctx, store := c.Context(), p.app.store
	recs, err := store.Notifications(ctx, p.me.ID)
	must(err)
	now := store.now()
	notes := make([]Notification, len(recs))
	unread := 0
	for i, r := range recs {
		n := Notification{ID: int(r.ID), Body: r.Body, When: ago(r.CreatedAt, now), Read: r.Read, Href: "/activity"}
		if r.GroupID != nil && r.GroupName != nil {
			n.Group = *r.GroupEmoji + " " + *r.GroupName
			n.Href = fmt.Sprintf("/groups/%d", *r.GroupID)
		}
		if !r.Read {
			unread++
		}
		notes[i] = n
	}
	acts, err := store.Activity(ctx, p.me.ID, 0, 30)
	must(err)
	people, err := store.People(ctx)
	must(err)
	return ActivityAssigns{Notifications: notes, Unread: unread, Activity: p.app.activityRows(acts, p.me.ID, people)}
}
