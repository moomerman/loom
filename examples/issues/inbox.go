package issues

import "github.com/moomerman/loom"

// InboxPage, at /inbox, lists the user's notifications: issues assigned
// to them and comments on theirs, written by the Notify job. Opening an
// issue marks its notifications read. The layout hears of new ones (the
// Notify job broadcasts on the user's inbox topic) and re-reads its
// counts; Assigns then re-reads the list.
type InboxPage struct {
	app  *App
	user User
}

// Mount marks the inbox current in the sidebar.
func (p *InboxPage) Mount(c *loom.Ctx) {
	ch := shell(c)
	ch.section, p.user = "inbox", ch.user
	c.Title("Inbox")
}

// Events marks everything read.
func (p *InboxPage) Events() loom.Events {
	return loom.Events{"read_all": p.readAll}
}

func (p *InboxPage) readAll(c *loom.Ctx, _ loom.Payload) {
	must(p.app.store.MarkRead(c.Context(), p.user.ID, 0))
	must(shell(c).refresh(c.Context()))
}

// Assigns reads the notifications.
func (p *InboxPage) Assigns(c *loom.Ctx) InboxAssigns {
	items, err := p.app.store.Notifications(c.Context(), p.user.ID)
	must(err)
	return InboxAssigns{Items: items, Unread: shell(c).unread}
}
