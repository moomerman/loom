package issues

import (
	"context"
	"strconv"
	"strings"

	"github.com/moomerman/loom"
)

// The app layout, Shell, shows the sidebar, the new-issue composer, the
// toast and the shortcuts dialog on every signed-in page. It is a live
// layout (loom.LiveLayout): chrome holds its state, mounted once per page
// load before the page, and handles its events (compose, help,
// toast_close, ...) whichever markup sends them, so a page handles only
// its own. A page reaches the chrome with shell(c), to read the user and
// teams or to say which sidebar item is current.

// Composer is the new-issue dialog, as the layout renders it.
type Composer struct {
	Open        bool
	Menu        string // the open menu: team, status, priority, assignee or labels
	Title       string
	Description string
	Error       string

	Team, Status, Priority, Assignee            Option // the current choices
	TeamOptions, StatusOptions, PriorityOptions []Option
	AssigneeOptions, LabelOptions               []Option
	Labels                                      []Label
	LabelsText                                  string // "Bug, Docs"
}

// Toast is a short message in the corner, such as "Created ENG-23".
// The toast behavior dismisses it after a few seconds.
type Toast struct {
	ID   int // a new ID for each toast, so a new one replays its animation
	Text string
	Link string
	Href string
}

// chrome is the state of the layout around a signed-in page.
type chrome struct {
	app     *App
	user    User
	section string // the sidebar's current item: mine, inbox or team:ENG; set by the page
	teams   []Team
	unread  int
	toast   Toast
	help    bool

	compose composeState
}

// composeState is the composer's state while it is open.
type composeState struct {
	open        bool
	menu        string
	team        Team
	title       string
	description string
	status      string
	priority    int
	assignee    string
	labels      []int
	err         string

	members   []User  // the team's, to assign
	teamLabel []Label // the team's labels
}

// shell returns the page's chrome.
func shell(c *loom.Ctx) *chrome { return loom.LayoutOf[*chrome](c) }

// Mount loads the chrome for the signed-in user and subscribes to the
// user's inbox and teams, so the sidebar's counts stay current on every
// page.
func (ch *chrome) Mount(c *loom.Ctx) {
	ch.user = loom.User[User](c)
	c.Subscribe(InboxTopic(ch.user.ID))
	must(ch.refresh(c.Context()))
	for _, t := range ch.teams {
		c.Subscribe(TeamTopic(t.ID))
	}
}

// refresh re-reads the teams' open counts and the unread count.
func (ch *chrome) refresh(ctx context.Context) error {
	teams, err := ch.app.store.Teams(ctx, ch.user.ID)
	if err != nil {
		return err
	}
	for i := range teams {
		if i < 9 {
			teams[i].Hotkey = "g " + strconv.Itoa(i+1)
		}
	}
	unread, err := ch.app.store.Unread(ctx, ch.user.ID)
	ch.teams, ch.unread = teams, unread
	return err
}

// HandleInfo re-reads the counts after a message on the inbox or a
// team's topic. The page then renders again, re-reading what it shows.
func (ch *chrome) HandleInfo(c *loom.Ctx, msg loom.Message) {
	must(ch.refresh(c.Context()))
}

// team returns the user's team with the id.
func (ch *chrome) team(id int) (Team, bool) {
	for _, t := range ch.teams {
		if t.ID == id {
			return t, true
		}
	}
	return Team{}, false
}

// Events handles the layout's events: the composer, the toast and the
// shortcuts dialog. A page's markup may send them too, as the team
// page's + buttons send compose with a suggested team and status.
func (ch *chrome) Events() loom.Events {
	cs := &ch.compose
	return loom.Events{
		"help":          func(c *loom.Ctx, _ loom.Payload) { ch.help = true },
		"help_close":    func(c *loom.Ctx, _ loom.Payload) { ch.help = false },
		"toast_close":   func(c *loom.Ctx, _ loom.Payload) { ch.toast.Text = "" },
		"compose":       ch.openComposer,
		"compose_close": func(c *loom.Ctx, _ loom.Payload) { cs.open = false },
		"compose_menu": func(c *loom.Ctx, payload loom.Payload) {
			if cs.menu == payload["menu"] {
				cs.menu = ""
			} else {
				cs.menu = payload["menu"]
			}
		},
		"compose_menu_close": func(c *loom.Ctx, payload loom.Payload) {
			// A click on another menu's button closes this one after
			// opening that one, so only the menu that is open may close.
			if cs.menu == payload["menu"] {
				cs.menu = ""
			}
		},
		"compose_pick": ch.composePick,
		"compose_field": func(c *loom.Ctx, payload loom.Payload) {
			switch payload["field"] {
			case "title":
				cs.title = payload["value"]
			case "description":
				cs.description = payload["value"]
			}
		},
		"compose_submit": ch.composeSubmit,
	}
}

// openComposer opens the composer. The page may suggest a team (its own) and
// a status (the group whose + was clicked).
func (ch *chrome) openComposer(c *loom.Ctx, payload loom.Payload) {
	team, ok := ch.team(atoi(payload["team"]))
	if !ok {
		team, ok = ch.team(ch.defaultTeam(c))
	}
	if !ok && len(ch.teams) > 0 {
		team = ch.teams[0]
	}
	status := payload["status"]
	if !validStatus(status) {
		status = "todo"
	}
	ch.compose = composeState{open: true, status: status}
	must(ch.setTeam(c.Context(), team))
}

// composePick picks a value from one of the composer's menus.
func (ch *chrome) composePick(c *loom.Ctx, payload loom.Payload) {
	cs := &ch.compose
	v := payload["value"]
	switch payload["menu"] {
	case "team":
		if t, ok := ch.team(atoi(v)); ok && t.ID != cs.team.ID {
			must(ch.setTeam(c.Context(), t))
		}
	case "status":
		if validStatus(v) {
			cs.status = v
		}
	case "priority":
		cs.priority = atoi(v)
	case "assignee":
		cs.assignee = v
	case "labels":
		// Labels toggle, and the menu stays open to pick several.
		cs.labels = toggle(cs.labels, atoi(v))
		return
	}
	cs.menu = ""
}

// composeSubmit creates the issue.
func (ch *chrome) composeSubmit(c *loom.Ctx, payload loom.Payload) {
	cs := &ch.compose
	cs.title, cs.description = payload["title"], payload["description"]
	if strings.TrimSpace(cs.title) == "" {
		cs.err = "Give the issue a title."
		return
	}
	id, err := ch.app.store.Create(c.Context(), ch.user.ID, NewIssue{
		TeamID: cs.team.ID, Title: strings.TrimSpace(cs.title), Description: strings.TrimSpace(cs.description),
		Status: cs.status, Priority: cs.priority, AssigneeID: cs.assignee, LabelIDs: cs.labels,
	})
	must(err)
	cs.open = false
	ch.toast = Toast{ID: ch.toast.ID + 1, Text: "Created " + id, Link: "View issue", Href: "/issue/" + id}
}

// defaultTeam is the team a new issue goes to when the page suggests
// none: the page's own, if it is a team page.
func (ch *chrome) defaultTeam(c *loom.Ctx) int {
	for _, t := range ch.teams {
		if ch.section == "team:"+t.Key {
			return t.ID
		}
	}
	return 0
}

// setTeam points the composer at a team, loading its members and labels.
// Choices that belong to the old team are cleared.
func (ch *chrome) setTeam(ctx context.Context, t Team) error {
	cs := &ch.compose
	members, err := ch.app.store.TeamMembers(ctx, t.ID)
	if err != nil {
		return err
	}
	labels, err := ch.app.store.Labels(ctx, t.ID)
	if err != nil {
		return err
	}
	cs.team, cs.members, cs.teamLabel, cs.labels = t, members, labels, nil
	if !containsUser(members, cs.assignee) {
		cs.assignee = ""
	}
	return nil
}

// Assigns are the layout's assigns; the page fills its inner_block.
func (ch *chrome) Assigns(c *loom.Ctx) ShellAssigns {
	return ShellAssigns{User: ch.user, Teams: ch.teams, Section: ch.section, Unread: ch.unread,
		Composer: ch.composer(), Toast: ch.toast, Help: ch.help}
}

// composer is the composer's view.
func (ch *chrome) composer() Composer {
	cs := ch.compose
	if !cs.open {
		return Composer{}
	}
	var teams []Option
	for _, t := range ch.teams {
		teams = append(teams, Option{Value: strconv.Itoa(t.ID), Label: t.Name, Kind: "label", Color: t.Color, Selected: t.ID == cs.team.ID})
	}
	view := Composer{
		Open: true, Menu: cs.menu, Title: cs.title, Description: cs.description, Error: cs.err,
		Team:            Option{Value: strconv.Itoa(cs.team.ID), Label: cs.team.Name, Kind: "label", Color: cs.team.Color},
		Status:          statusOption(cs.status, false),
		Priority:        priorityOption(cs.priority, false),
		Assignee:        assigneeOption(cs.members, cs.assignee),
		TeamOptions:     teams,
		StatusOptions:   statusOptions(cs.status),
		PriorityOptions: priorityOptions(cs.priority),
		AssigneeOptions: assigneeOptions(cs.members, cs.assignee),
		LabelOptions:    labelOptions(cs.teamLabel, cs.labels),
	}
	var names []string
	for _, l := range cs.teamLabel {
		if contains(cs.labels, l.ID) {
			view.Labels = append(view.Labels, l)
			names = append(names, l.Name)
		}
	}
	view.LabelsText = strings.Join(names, ", ")
	return view
}

// --- Menu options ---

func statusOption(s string, selected bool) Option {
	return Option{Value: s, Label: StatusName(s), Kind: "status", Status: s, Selected: selected}
}

func statusOptions(current string) []Option {
	opts := make([]Option, len(Statuses))
	for i, s := range Statuses {
		opts[i] = statusOption(s.ID, s.ID == current)
	}
	return opts
}

func priorityOption(p int, selected bool) Option {
	return Option{Value: strconv.Itoa(p), Label: PriorityName(p), Kind: "priority", Priority: p, Selected: selected}
}

func priorityOptions(current int) []Option {
	opts := make([]Option, len(Priorities))
	for i, p := range Priorities {
		opts[i] = priorityOption(p.Value, p.Value == current)
	}
	return opts
}

func assigneeOption(members []User, id string) Option {
	for _, u := range members {
		if u.ID == id {
			return Option{Value: u.ID, Label: u.Name, Kind: "user", User: u}
		}
	}
	return Option{Value: "", Label: "Assignee", Kind: "user"}
}

func assigneeOptions(members []User, current string) []Option {
	opts := []Option{{Value: "", Label: "No assignee", Kind: "user", Selected: current == ""}}
	for _, u := range members {
		opts = append(opts, Option{Value: u.ID, Label: u.Name, Kind: "user", User: u, Selected: u.ID == current})
	}
	return opts
}

func labelOptions(labels []Label, selected []int) []Option {
	opts := make([]Option, len(labels))
	for i, l := range labels {
		opts[i] = Option{Value: strconv.Itoa(l.ID), Label: l.Name, Kind: "label", Color: l.Color, Selected: contains(selected, l.ID)}
	}
	return opts
}

func contains(xs []int, x int) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// toggle adds x to xs, or removes it if it is there.
func toggle(xs []int, x int) []int {
	for i, y := range xs {
		if y == x {
			return append(xs[:i:i], xs[i+1:]...)
		}
	}
	return append(xs, x)
}

func containsUser(us []User, id string) bool {
	for _, u := range us {
		if u.ID == id {
			return true
		}
	}
	return false
}
