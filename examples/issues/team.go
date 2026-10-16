package issues

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/moomerman/loom"
)

// TeamPage, at /team/{key}, lists a team's issues grouped by status,
// with views (all, active, backlog), filters by priority, assignee and
// label, and search. The view, filters and search live in the URL,
// /team/ENG?view=active&priority=1&q=notify, so a filtered list can be
// shared, bookmarked and reloaded, and the back button undoes a filter.
// Changing one patches the URL (c.Patch) and HandleParams reads it back:
// no page load, and the rest of the page (an open composer, the menus)
// stays as it is. The view tabs are plain links to the same path, which
// patch the page the same way. The team is in the path, so Mount loads
// it: a link to another team mounts the page afresh.
type TeamPage struct {
	app     *App
	user    User
	team    Team
	members []User
	labels  []Label

	// From the URL, set by HandleParams.
	view     string // all, active or backlog
	search   string
	priority int    // -1 for any
	assignee string // "" for anyone, "none" for unassigned
	label    int    // 0 for any

	menu string // the open filter menu
}

// Mount loads the team, if the user belongs to it.
func (p *TeamPage) Mount(c *loom.Ctx) {
	key := strings.ToUpper(c.Request.PathValue("key"))
	ch := shell(c)
	ch.section, p.user = "team:"+key, ch.user
	team, err := p.app.store.Team(c.Context(), p.user.ID, key)
	must(err)
	if err != nil {
		c.Title("Not found")
		return
	}
	p.team = team
	p.members, err = p.app.store.TeamMembers(c.Context(), team.ID)
	must(err)
	p.labels, err = p.app.store.Labels(c.Context(), team.ID)
	must(err)
	c.Title(team.Name)
}

// HandleParams reads the view, filters and search from the URL: after
// Mount, and whenever the URL changes. Anything it doesn't understand
// means the default, so a hand-edited URL can't break the page.
func (p *TeamPage) HandleParams(c *loom.Ctx, query url.Values) {
	p.view = "all"
	if v := query.Get("view"); v == "active" || v == "backlog" {
		p.view = v
	}
	p.priority = -1
	if n, err := strconv.Atoi(query.Get("priority")); err == nil && n >= 0 && n <= 4 {
		p.priority = n
	}
	p.assignee = query.Get("assignee")
	p.label = atoi(query.Get("label"))
	p.search = query.Get("q")
}

// query is the URL query holding the page's view, filters and search,
// leaving out defaults so URLs stay short.
func (p *TeamPage) query() url.Values {
	q := url.Values{}
	set := func(key, value, def string) {
		if value != def {
			q.Set(key, value)
		}
	}
	set("view", p.view, "all")
	set("priority", strconv.Itoa(p.priority), "-1")
	set("assignee", p.assignee, "")
	set("label", strconv.Itoa(p.label), "0")
	set("q", p.search, "")
	return q
}

// url is the page's URL with query.
func (p *TeamPage) url(c *loom.Ctx, q url.Values) string {
	u := "/team/" + p.team.Key
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return c.Path(u)
}

// patch changes one value in the URL, "" removing it.
func (p *TeamPage) patch(c *loom.Ctx, key, value string, opts ...loom.PatchOption) {
	q := p.query()
	if value == "" {
		q.Del(key)
	} else {
		q.Set(key, value)
	}
	c.Patch(p.url(c, q), opts...)
}

// Events changes the filters and search, by patching the URL. The +
// buttons send compose, which the layout handles.
func (p *TeamPage) Events() loom.Events {
	return loom.Events{
		// Each keystroke replaces the history entry, so the back button
		// skips the search's intermediate states.
		"search": func(c *loom.Ctx, payload loom.Payload) { p.patch(c, "q", payload["value"], loom.Replace()) },
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
		"pick": p.pick,
		"clear_filters": func(c *loom.Ctx, _ loom.Payload) {
			q := url.Values{}
			if p.view != "all" {
				q.Set("view", p.view)
			}
			c.Patch(p.url(c, q))
		},
	}
}

// pick picks a filter's value from its menu.
func (p *TeamPage) pick(c *loom.Ctx, payload loom.Payload) {
	p.menu = ""
	switch menu := payload["menu"]; menu {
	case "priority", "assignee", "label":
		p.patch(c, menu, payload["value"])
	}
}

// views are the statuses each view shows.
var views = map[string][]string{
	"active":  {"todo", "in_progress", "in_review"},
	"backlog": {"backlog"},
}

// Assigns reads the team's issues through the filters, so a change to
// one shows at once: the layout hears of it, and the page renders again.
func (p *TeamPage) Assigns(c *loom.Ctx) TeamIssuesAssigns {
	if p.team.ID == 0 {
		return TeamIssuesAssigns{} // not found
	}
	issues, err := p.app.store.Issues(c.Context(), Filter{
		UserID: p.user.ID, TeamID: p.team.ID, Statuses: views[p.view],
		Priority: p.priority, AssigneeID: p.assignee, LabelID: p.label, Search: p.search,
	})
	must(err)
	if t, ok := shell(c).team(p.team.ID); ok {
		p.team.Open = t.Open
	}
	return TeamIssuesAssigns{Found: true, Team: p.team, Tabs: p.tabs(c), Search: p.search,
		Groups: groupByStatus(issues), Count: len(issues), Filters: p.filters()}
}

// tabs are the view tabs: links to this page in each view, keeping the
// filters.
func (p *TeamPage) tabs(c *loom.Ctx) []Tab {
	var tabs []Tab
	for _, v := range []struct{ view, label string }{{"all", "All issues"}, {"active", "Active"}, {"backlog", "Backlog"}} {
		q := p.query()
		q.Del("view")
		if v.view != "all" {
			q.Set("view", v.view)
		}
		tabs = append(tabs, Tab{Label: v.label, Href: p.url(c, q), Selected: v.view == p.view})
	}
	return tabs
}

// Filters is the filter bar: each menu's current choice and options.
type Filters struct {
	Menu                                           string
	Priority, Assignee, Label                      Option
	PriorityOptions, AssigneeOptions, LabelOptions []Option
	Active                                         bool // any filter is set
}

func (p *TeamPage) filters() Filters {
	f := Filters{Menu: p.menu, Active: p.priority >= 0 || p.assignee != "" || p.label != 0 || p.search != ""}

	f.Priority = Option{Label: "Priority", Kind: "none"}
	f.PriorityOptions = []Option{{Value: "-1", Label: "Any priority", Kind: "none", Selected: p.priority < 0}}
	for _, o := range priorityOptions(p.priority) {
		if o.Selected {
			f.Priority = o
		}
		f.PriorityOptions = append(f.PriorityOptions, o)
	}

	f.Assignee = Option{Label: "Assignee", Kind: "none"}
	f.AssigneeOptions = []Option{{Value: "", Label: "Anyone", Kind: "none", Selected: p.assignee == ""}, {Value: "none", Label: "No assignee", Kind: "user", Selected: p.assignee == "none"}}
	for _, u := range p.members {
		o := Option{Value: u.ID, Label: u.Name, Kind: "user", User: u, Selected: u.ID == p.assignee}
		if o.Selected {
			f.Assignee = o
		}
		f.AssigneeOptions = append(f.AssigneeOptions, o)
	}
	if p.assignee == "none" {
		f.Assignee = Option{Value: "none", Label: "No assignee", Kind: "user"}
	}

	f.Label = Option{Label: "Label", Kind: "none"}
	f.LabelOptions = []Option{{Value: "0", Label: "Any label", Kind: "none", Selected: p.label == 0}}
	for _, o := range labelOptions(p.labels, []int{p.label}) {
		if o.Selected {
			f.Label = o
		}
		f.LabelOptions = append(f.LabelOptions, o)
	}
	return f
}
