package issues

import (
	"fmt"
	"strings"
	"time"
)

// The types templates render. Templates can only read types from their
// own package, with string, int, float64 and bool fields, so the store's
// results are turned into these rows in Go, with times and names already
// formatted. See docs/example-jobsweb.md, "Templates get plain rows".

// User is a person, as an avatar and a name. A zero User (ID "") is
// "nobody", e.g. an unassigned issue.
type User struct {
	ID       string
	Name     string
	Email    string
	Initials string // "AL" for Ada Lovelace, shown in the avatar
	Color    string // a color token name: violet, teal, ...
}

// Team is a team in the sidebar and the team switcher.
type Team struct {
	ID    int
	Key   string // ENG, the prefix of its issues' identifiers
	Name  string
	Color string
	Open  int // issues not done or canceled, shown in the sidebar

	Hotkey string // "g 1" for the first team in the sidebar
}

// Label is a colored tag on an issue.
type Label struct {
	ID    int
	Name  string
	Color string
}

// IssueRow is an issue in a list.
type IssueRow struct {
	ID         int
	Identifier string // ENG-12
	Href       string // its page: /issue/ENG-12
	Title      string
	Status     string // backlog, todo, in_progress, in_review, done, canceled
	Priority   int    // 0 none, 1 urgent, 2 high, 3 medium, 4 low
	Assignee   User
	Labels     []Label
	Created    string // "Oct 3"
	Updated    string // "3d ago"
}

// StatusGroup is the issues of one status, a section of a list.
type StatusGroup struct {
	Status string
	Name   string // "In Progress"
	Issues []IssueRow
	Count  int
}

// Option is an entry of a menu: a status, priority, person, label or
// team to pick. Kind says which icon it shows: status, priority, user,
// label or none.
type Option struct {
	Value    string
	Label    string
	Kind     string
	Status   string // for Kind status
	Priority int    // for Kind priority
	User     User   // for Kind user
	Color    string // for Kind label
	Selected bool
}

// Tab is a view tab: a link to the page in that view.
type Tab struct {
	Label    string
	Href     string
	Selected bool
}

// Statuses lists the statuses in workflow order, with their names.
var Statuses = []struct{ ID, Name string }{
	{"backlog", "Backlog"},
	{"todo", "Todo"},
	{"in_progress", "In Progress"},
	{"in_review", "In Review"},
	{"done", "Done"},
	{"canceled", "Canceled"},
}

// Priorities lists the priorities in menu order, urgent first.
var Priorities = []struct {
	Value int
	Name  string
}{{0, "No priority"}, {1, "Urgent"}, {2, "High"}, {3, "Medium"}, {4, "Low"}}

// StatusName returns a status's display name, e.g. "In Progress".
//
//loom:helper
func StatusName(status string) string {
	for _, s := range Statuses {
		if s.ID == status {
			return s.Name
		}
	}
	return status
}

// PriorityName returns a priority's display name, e.g. "High".
//
//loom:helper
func PriorityName(priority int) string {
	for _, p := range Priorities {
		if p.Value == priority {
			return p.Name
		}
	}
	return "No priority"
}

// validStatus reports whether s is a status.
func validStatus(s string) bool {
	for _, st := range Statuses {
		if st.ID == s {
			return true
		}
	}
	return false
}

// Initial returns the first letter of a name, for an icon.
//
//loom:helper
func Initial(name string) string {
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return ""
}

// eventText describes a change in an issue's history, after the name of
// who made it.
func eventText(kind, from, to string) string {
	switch kind {
	case "created":
		return "created the issue"
	case "status":
		return "changed status from " + from + " to " + to
	case "priority":
		if to == "No priority" {
			return "removed the priority"
		}
		return "set priority to " + to
	case "assignee":
		switch {
		case to == "":
			return "unassigned " + from
		case from == "":
			return "assigned the issue to " + to
		}
		return "reassigned the issue from " + from + " to " + to
	case "title":
		return "changed the title from “" + from + "” to “" + to + "”"
	case "labels":
		if to == "" {
			return "removed the label " + from
		}
		return "added the label " + to
	}
	return kind
}

// initials returns the first letters of the first and last words of a
// name: "Ada Lovelace" is "AL".
func initials(name string) string {
	words := strings.Fields(name)
	switch len(words) {
	case 0:
		return "?"
	case 1:
		return strings.ToUpper(words[0][:1])
	}
	return strings.ToUpper(words[0][:1] + words[len(words)-1][:1])
}

// ago formats how long ago t was, as Linear does: "just now", "5m ago",
// "3h ago", "2d ago", then the date.
func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return date(now, t)
}

// date formats a date as "Oct 3", with the year if it isn't now's.
func date(now, t time.Time) string {
	if t.Year() != now.Year() {
		return t.Format("Jan 2, 2006")
	}
	return t.Format("Jan 2")
}

// groupByStatus groups issues into sections in workflow order, leaving
// out empty statuses.
func groupByStatus(issues []IssueRow) []StatusGroup {
	var groups []StatusGroup
	for _, s := range Statuses {
		g := StatusGroup{Status: s.ID, Name: s.Name}
		for _, i := range issues {
			if i.Status == s.ID {
				g.Issues = append(g.Issues, i)
			}
		}
		if g.Count = len(g.Issues); g.Count > 0 {
			groups = append(groups, g)
		}
	}
	return groups
}
