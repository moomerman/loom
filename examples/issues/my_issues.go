package issues

import (
	"net/url"

	"github.com/moomerman/loom"
)

// MyIssuesPage, at /, shows the issues assigned to the signed-in user
// across their teams, or the ones they created.
type MyIssuesPage struct {
	app  *App
	user User
	view string // assigned or created
}

// Mount marks My issues current in the sidebar.
func (p *MyIssuesPage) Mount(c *loom.Ctx) {
	ch := shell(c)
	ch.section, p.user = "mine", ch.user
	c.Title("My issues")
}

// HandleParams reads the view from the URL, /?view=created: after
// Mount, and when a view tab, a link to this page, patches the URL.
func (p *MyIssuesPage) HandleParams(c *loom.Ctx, query url.Values) {
	p.view = "assigned"
	if query.Get("view") == "created" {
		p.view = "created"
	}
}

// Assigns reads the list, so a new issue from the composer shows at
// once, and so does a change in one of the user's teams: the layout
// hears of it, and the page renders again.
func (p *MyIssuesPage) Assigns(c *loom.Ctx) MyIssuesAssigns {
	f := Filter{UserID: p.user.ID, Priority: -1}
	if p.view == "created" {
		f.CreatorID = p.user.ID
	} else {
		f.AssigneeID = p.user.ID
	}
	issues, err := p.app.store.Issues(c.Context(), f)
	must(err)
	tabs := []Tab{
		{Label: "Assigned", Href: c.Path("/"), Selected: p.view == "assigned"},
		{Label: "Created", Href: c.Path("/?view=created"), Selected: p.view == "created"},
	}
	return MyIssuesAssigns{View: p.view, Tabs: tabs, Groups: groupByStatus(issues), Count: len(issues)}
}
