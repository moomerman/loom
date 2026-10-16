package issues

import (
	"strconv"

	"github.com/moomerman/loom"
)

// IssuePage, at /issue/{id} (e.g. /issue/ENG-12), shows one issue: its
// title and description, edited in place; its properties, changed from
// menus; and its history and comments. Changes others make appear live.
type IssuePage struct {
	app     *App
	user    User
	ident   string
	issue   *FullIssue
	members []User
	labels  []Label
	menu    string // the open property menu
}

// Mount loads the issue, subscribes to its team's changes, and marks
// the user's notifications about it read: they have seen it.
func (p *IssuePage) Mount(c *loom.Ctx) {
	ch := shell(c)
	p.user = ch.user
	p.ident = c.Request.PathValue("id")
	if !p.load(c) {
		c.Title("Not found")
		return
	}
	c.Subscribe(TeamTopic(p.issue.TeamID))
	must(p.app.store.MarkIssueRead(c.Context(), p.user.ID, p.issue.ID))
	must(ch.refresh(c.Context())) // the unread count
	var err error
	p.members, err = p.app.store.TeamMembers(c.Context(), p.issue.TeamID)
	must(err)
	p.labels, err = p.app.store.Labels(c.Context(), p.issue.TeamID)
	must(err)
}

// load reads the issue and reports whether the user can see it.
func (p *IssuePage) load(c *loom.Ctx) bool {
	issue, err := p.app.store.Issue(c.Context(), p.user.ID, p.ident)
	must(err)
	p.issue = issue
	if issue != nil {
		c.Title(issue.Identifier + " " + issue.Title)
	}
	return issue != nil
}

// Events edits the issue. Each change is one transaction; when it
// commits, every page showing the issue hears of it and re-reads, this
// one included.
func (p *IssuePage) Events() loom.Events {
	return loom.Events{
		"menu": func(c *loom.Ctx, payload loom.Payload) {
			if p.menu == payload["menu"] {
				p.menu = ""
			} else {
				p.menu = payload["menu"]
			}
		},
		"menu_close": func(c *loom.Ctx, payload loom.Payload) {
			// A click on another menu's button closes this one after
			// opening that one, so only the menu that is open may close.
			if p.menu == payload["menu"] {
				p.menu = ""
			}
		},
		"pick": func(c *loom.Ctx, payload loom.Payload) {
			field := payload["menu"]
			if field == "labels" {
				field = "label" // toggles one label; the menu stays open
			} else {
				p.menu = ""
			}
			p.change(c, Change{Field: field, Value: payload["value"]})
		},
		"set": func(c *loom.Ctx, payload loom.Payload) {
			// The title and description save when they lose focus.
			p.change(c, Change{Field: payload["field"], Value: payload["value"]})
		},
		"comment": func(c *loom.Ctx, payload loom.Payload) {
			if p.issue != nil {
				must(p.app.store.Comment(c.Context(), p.user.ID, p.issue.ID, payload["body"]))
				p.load(c)
			}
		},
	}
}

// change saves a change to the issue and re-reads it.
func (p *IssuePage) change(c *loom.Ctx, ch Change) {
	if p.issue != nil {
		must(p.app.store.Update(c.Context(), p.user.ID, p.issue.ID, ch))
		p.load(c)
	}
}

// HandleInfo re-reads the issue when it changes. The layout gets the
// same message, and re-reads the sidebar's counts.
func (p *IssuePage) HandleInfo(c *loom.Ctx, msg loom.Message) {
	if p.issue == nil || msg.Payload["issue"] != strconv.Itoa(p.issue.ID) {
		return // this issue didn't change
	}
	p.load(c)
}

// IssueView is an issue as its page shows it.
type IssueView struct {
	Identifier  string
	Title       string
	Description string
	Team        Team
	Status      Option
	Priority    Option
	Assignee    Option
	Labels      []Label
	Creator     User
	Created     string
	Updated     string
	Timeline    []TimelineItem

	StatusOptions, PriorityOptions, AssigneeOptions, LabelOptions []Option
}

// Assigns shows the loaded issue, or not found if the user can't see it
// or it was deleted meanwhile.
func (p *IssuePage) Assigns(c *loom.Ctx) IssueDetailAssigns {
	i := p.issue
	if i == nil {
		return IssueDetailAssigns{}
	}
	team, _ := shell(c).team(i.TeamID)
	var labelIDs []int
	for id := range i.LabelIDs {
		labelIDs = append(labelIDs, id)
	}
	assignee := assigneeOption(p.members, i.Assignee.ID)
	if i.Assignee.ID == "" {
		assignee.Label = "Unassigned"
	}
	return IssueDetailAssigns{Found: true, Menu: p.menu, Issue: IssueView{
		Identifier: i.Identifier, Title: i.Title, Description: i.Description, Team: team,
		Status: statusOption(i.Status, false), Priority: priorityOption(i.Priority, false), Assignee: assignee,
		Labels: i.Labels, Creator: i.Creator, Created: i.Created, Updated: i.Updated, Timeline: i.Timeline,
		StatusOptions:   statusOptions(i.Status),
		PriorityOptions: priorityOptions(i.Priority),
		AssigneeOptions: assigneeOptions(p.members, i.Assignee.ID),
		LabelOptions:    labelOptions(p.labels, labelIDs),
	}}
}
