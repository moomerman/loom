package issues

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/pubsub"
)

// Store reads and writes the tracker's data in Postgres (migrations/).
// Every write runs in one transaction that also records the change in
// the issue's history and enqueues any notification, so the three exist
// together or not at all. Once it commits, an AfterCommit hook tells the
// team's open pages, which re-read what they show.
type Store struct {
	DB     *loom.DB
	PubSub pubsub.PubSub // where changes are announced
	Clock  jobs.Clock    // "now", for relative times; tests fix it
}

// TeamTopic is the PubSub topic on which a team's open pages hear that
// one of its issues changed. The payload's "issue" is the issue's id.
func TeamTopic(teamID int) string { return "team:" + strconv.Itoa(teamID) }

// InboxTopic is the topic on which a user's open pages hear of a new
// notification.
func InboxTopic(userID string) string { return "inbox:" + userID }

// ErrNotFound is returned for an issue or team that doesn't exist, or
// that the user may not see.
var ErrNotFound = errors.New("not found")

// --- Users and teams ---

type userRecord struct {
	ID    string
	Name  string
	Email string
	Color string
}

func (u userRecord) row() User {
	return User{ID: u.ID, Name: u.Name, Email: u.Email, Initials: initials(u.Name), Color: u.Color}
}

// User returns the user with the id, or loom.ErrNotFound. It is the
// app's user loader (loom.Users), so loom.User[User](c) is the signed-in
// user, and a deleted user counts as signed out.
func (s *Store) User(ctx context.Context, id string) (User, error) {
	u, err := loom.Get[userRecord](ctx, s.DB, `SELECT id, name, email, color FROM users WHERE id = $1`, id)
	return u.row(), err
}

// Users returns everyone, by name.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	us, err := loom.Select[userRecord](ctx, s.DB, `SELECT id, name, email, color FROM users ORDER BY name`)
	rows := make([]User, len(us))
	for i, u := range us {
		rows[i] = u.row()
	}
	return rows, err
}

// TeamMembers returns the members of a team, by name.
func (s *Store) TeamMembers(ctx context.Context, teamID int) ([]User, error) {
	us, err := loom.Select[userRecord](ctx, s.DB, `SELECT u.id, u.name, u.email, u.color FROM users u JOIN team_members m ON m.user_id = u.id WHERE m.team_id = $1 ORDER BY u.name`, teamID)
	rows := make([]User, len(us))
	for i, u := range us {
		rows[i] = u.row()
	}
	return rows, err
}

// Teams returns the teams a user belongs to, with their open issue
// counts, by name.
func (s *Store) Teams(ctx context.Context, userID string) ([]Team, error) {
	return loom.Select[Team](ctx, s.DB, `
		SELECT t.id, t.key, t.name, t.color,
			(SELECT count(*) FROM issues i WHERE i.team_id = t.id AND i.status NOT IN ('done', 'canceled')) AS open
		FROM teams t JOIN team_members m ON m.team_id = t.id
		WHERE m.user_id = $1 ORDER BY t.name`, userID)
}

// Team returns a team the user belongs to, by key, or ErrNotFound.
func (s *Store) Team(ctx context.Context, userID, key string) (Team, error) {
	t, err := loom.Get[Team](ctx, s.DB, `
		SELECT t.id, t.key, t.name, t.color, 0 AS open
		FROM teams t JOIN team_members m ON m.team_id = t.id
		WHERE m.user_id = $1 AND t.key = $2`, userID, strings.ToUpper(key))
	if errors.Is(err, loom.ErrNotFound) {
		return Team{}, ErrNotFound
	}
	return t, err
}

// Labels returns a team's labels, by name.
func (s *Store) Labels(ctx context.Context, teamID int) ([]Label, error) {
	return loom.Select[Label](ctx, s.DB, `SELECT id, name, color FROM labels WHERE team_id = $1 ORDER BY name`, teamID)
}

// --- Reading issues ---

// Filter selects issues. Zero fields don't filter.
type Filter struct {
	UserID     string   // only teams this user belongs to (required)
	TeamID     int      // one team
	AssigneeID string   // "none" for unassigned
	CreatorID  string   //
	Statuses   []string // any of these
	Priority   int      // -1 for any; 0 is "no priority"
	LabelID    int      //
	Search     string   // in the title, or an identifier such as ENG-12
}

type issueRecord struct {
	ID         int
	Identifier string
	TeamID     int
	Title      string
	Status     string
	Priority   int
	AssigneeID string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// issueColumns are the columns of an issueRecord, from issues i joined
// with teams t.
const issueColumns = `i.id, t.key || '-' || i.number AS identifier, i.team_id, i.title, i.status, i.priority,
	coalesce(i.assignee_id, '') AS assignee_id, i.created_at, i.updated_at`

// Issues returns the issues matching f, most urgent first, then most
// recently updated. Callers group them by status.
func (s *Store) Issues(ctx context.Context, f Filter) ([]IssueRow, error) {
	if f.Statuses == nil {
		f.Statuses = []string{}
	}
	recs, err := loom.Select[issueRecord](ctx, s.DB, `
		SELECT `+issueColumns+`
		FROM issues i JOIN teams t ON t.id = i.team_id
		WHERE i.team_id IN (SELECT team_id FROM team_members WHERE user_id = $1)
			AND ($2 = 0 OR i.team_id = $2)
			AND ($3 = '' OR ($3 = 'none' AND i.assignee_id IS NULL) OR i.assignee_id = $3)
			AND ($4 = '' OR i.creator_id = $4)
			AND (cardinality($5::text[]) = 0 OR i.status = ANY($5))
			AND ($6 < 0 OR i.priority = $6)
			AND ($7 = 0 OR EXISTS (SELECT 1 FROM issue_labels il WHERE il.issue_id = i.id AND il.label_id = $7))
			AND ($8 = '' OR i.title ILIKE '%' || $8 || '%' OR t.key || '-' || i.number ILIKE $8)
		ORDER BY CASE i.priority WHEN 0 THEN 5 ELSE i.priority END, i.updated_at DESC, i.id DESC`,
		f.UserID, f.TeamID, f.AssigneeID, f.CreatorID, f.Statuses, f.Priority, f.LabelID, strings.TrimSpace(f.Search))
	if err != nil {
		return nil, err
	}
	return s.rows(ctx, recs)
}

// rows turns records into rows, with their assignees and labels.
func (s *Store) rows(ctx context.Context, recs []issueRecord) ([]IssueRow, error) {
	users, err := s.userMap(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int, len(recs))
	for i, r := range recs {
		ids[i] = r.ID
	}
	type issueLabel struct {
		IssueID int
		Label
	}
	labels, err := loom.Select[issueLabel](ctx, s.DB, `
		SELECT il.issue_id, l.id, l.name, l.color
		FROM issue_labels il JOIN labels l ON l.id = il.label_id
		WHERE il.issue_id = ANY($1) ORDER BY l.name`, ids)
	if err != nil {
		return nil, err
	}
	byIssue := map[int][]Label{}
	for _, l := range labels {
		byIssue[l.IssueID] = append(byIssue[l.IssueID], l.Label)
	}
	now := s.Clock.Now()
	rows := make([]IssueRow, len(recs))
	for i, r := range recs {
		rows[i] = IssueRow{
			ID: r.ID, Identifier: r.Identifier, Href: "/issue/" + r.Identifier, Title: r.Title, Status: r.Status, Priority: r.Priority,
			Assignee: users[r.AssigneeID], Labels: byIssue[r.ID],
			Created: date(now, r.CreatedAt), Updated: ago(now, r.UpdatedAt),
		}
	}
	return rows, nil
}

// userMap returns every user by id. There are few, and every list shows
// some of them.
func (s *Store) userMap(ctx context.Context) (map[string]User, error) {
	us, err := s.Users(ctx)
	m := make(map[string]User, len(us))
	for _, u := range us {
		m[u.ID] = u
	}
	return m, err
}

// FullIssue is one issue with everything its page shows.
type FullIssue struct {
	IssueRow
	TeamID      int
	TeamKey     string
	TeamName    string
	Description string
	Creator     User
	LabelIDs    map[int]bool
	Timeline    []TimelineItem
}

// TimelineItem is a comment or a change in an issue's history.
type TimelineItem struct {
	Key   string // c12 or e40: unique across both kinds, for :key
	Kind  string // comment, created, status, priority, assignee, title or labels
	Actor User
	Body  string // a comment's text
	Text  string // a change, after the actor's name: "changed status from Todo to Done"
	When  string // "3h ago"
}

// Issue returns the issue with the identifier (ENG-12), if the user
// belongs to its team, or ErrNotFound.
func (s *Store) Issue(ctx context.Context, userID, identifier string) (*FullIssue, error) {
	key, num, ok := strings.Cut(strings.ToUpper(identifier), "-")
	n, err := strconv.Atoi(num)
	if !ok || err != nil {
		return nil, ErrNotFound
	}
	type record struct {
		issueRecord
		TeamName    string
		Description string
		CreatorID   string
	}
	rec, err := loom.Get[record](ctx, s.DB, `
		SELECT `+issueColumns+`, t.name AS team_name, i.description, i.creator_id
		FROM issues i JOIN teams t ON t.id = i.team_id
		WHERE t.key = $1 AND i.number = $2
			AND i.team_id IN (SELECT team_id FROM team_members WHERE user_id = $3)`, key, n, userID)
	if errors.Is(err, loom.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.rows(ctx, []issueRecord{rec.issueRecord})
	if err != nil {
		return nil, err
	}
	d := &FullIssue{IssueRow: rows[0], TeamID: rec.TeamID, TeamKey: key, TeamName: rec.TeamName,
		Description: rec.Description, LabelIDs: map[int]bool{}}
	for _, l := range d.Labels {
		d.LabelIDs[l.ID] = true
	}
	users, err := s.userMap(ctx)
	if err != nil {
		return nil, err
	}
	d.Creator = users[rec.CreatorID]
	type entry struct {
		Key       string
		Kind      string
		ActorID   string
		Body      string
		FromValue string
		ToValue   string
		CreatedAt time.Time
	}
	entries, err := loom.Select[entry](ctx, s.DB, `
		SELECT 'c' || id AS key, 'comment' AS kind, author_id AS actor_id, body, '' AS from_value, '' AS to_value, created_at
		FROM comments WHERE issue_id = $1
		UNION ALL
		SELECT 'e' || id, kind, actor_id, '', from_value, to_value, created_at
		FROM issue_events WHERE issue_id = $1
		ORDER BY created_at, key`, rec.ID)
	if err != nil {
		return nil, err
	}
	now := s.Clock.Now()
	for _, e := range entries {
		d.Timeline = append(d.Timeline, TimelineItem{Key: e.Key, Kind: e.Kind, Actor: users[e.ActorID],
			Body: e.Body, Text: eventText(e.Kind, e.FromValue, e.ToValue), When: ago(now, e.CreatedAt)})
	}
	return d, nil
}

// --- Writing issues ---

// NewIssue is what the composer creates.
type NewIssue struct {
	TeamID      int
	Title       string
	Description string
	Status      string
	Priority    int
	AssigneeID  string
	LabelIDs    []int
}

// Create adds an issue, numbered next in its team, and returns its
// identifier. The actor must belong to the team.
func (s *Store) Create(ctx context.Context, actor string, n NewIssue) (string, error) {
	var identifier string
	err := s.DB.Tx(ctx, func(tx loom.Tx) error {
		if _, err := loom.Get[int](ctx, tx, `SELECT 1 FROM team_members WHERE team_id = $1 AND user_id = $2`, n.TeamID, actor); err != nil {
			return err
		}
		var key string
		var number, id int
		// Taking the next number locks the team's row, so concurrent
		// creates in one team take turns and never share a number.
		if err := tx.QueryRow(ctx, `UPDATE teams SET issue_count = issue_count + 1 WHERE id = $1 RETURNING key, issue_count`, n.TeamID).Scan(&key, &number); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO issues (team_id, number, title, description, status, priority, assignee_id, creator_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, nullif($7, ''), $8, $9, $9) RETURNING id`,
			n.TeamID, number, n.Title, n.Description, n.Status, n.Priority, n.AssigneeID, actor, s.Clock.Now()).Scan(&id); err != nil {
			return err
		}
		for _, l := range n.LabelIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO issue_labels (issue_id, label_id, team_id) SELECT $1, id, team_id FROM labels WHERE id = $2 AND team_id = $3`, id, l, n.TeamID); err != nil {
				return err
			}
		}
		if err := s.record(ctx, tx, id, n.TeamID, actor, "created", "", ""); err != nil {
			return err
		}
		if n.AssigneeID != "" && n.AssigneeID != actor {
			if _, err := jobs.Enqueue(tx, Notify{UserID: n.AssigneeID, IssueID: id, ActorID: actor, Kind: "assigned"}); err != nil {
				return err
			}
		}
		identifier = key + "-" + strconv.Itoa(number)
		s.announce(tx, n.TeamID, id)
		return nil
	})
	return identifier, err
}

// Change is a change to one property of an issue.
type Change struct {
	Field string // status, priority, assignee, title, description or label
	Value string // the new value; for label, the id of a label to toggle
}

// Update applies a change to an issue the actor can see, and records it
// in the issue's history. A change to the current value does nothing.
func (s *Store) Update(ctx context.Context, actor string, issueID int, ch Change) error {
	return s.DB.Tx(ctx, func(tx loom.Tx) error {
		cur, err := loom.Get[current](ctx, tx, `
			SELECT id, team_id, status, priority, coalesce(assignee_id, '') AS assignee_id, title FROM issues
			WHERE id = $1 AND team_id IN (SELECT team_id FROM team_members WHERE user_id = $2)
			FOR UPDATE`, issueID, actor)
		if errors.Is(err, loom.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		changed, err := s.apply(ctx, tx, actor, cur, ch)
		if changed && err == nil {
			s.announce(tx, cur.TeamID, issueID)
		}
		return err
	})
}

// current is an issue's state before a change, locked for the update.
type current struct {
	ID         int
	TeamID     int
	Status     string
	Priority   int
	AssigneeID string
	Title      string
}

// apply makes a change in tx and records it. It reports whether
// anything changed.
func (s *Store) apply(ctx context.Context, tx loom.Tx, actor string, cur current, ch Change) (bool, error) {
	set := func(query string, args ...any) error {
		_, err := tx.Exec(ctx, `UPDATE issues SET `+query+`, updated_at = $1 WHERE id = $2`, append([]any{s.Clock.Now(), cur.ID}, args...)...)
		return err
	}
	record := func(kind, from, to string) error { return s.record(ctx, tx, cur.ID, cur.TeamID, actor, kind, from, to) }
	switch ch.Field {
	case "status":
		if ch.Value == cur.Status || !validStatus(ch.Value) {
			return false, nil
		}
		// completed_at is set when an issue is done, and cleared when it
		// is reopened.
		if err := set(`status = $3, completed_at = CASE WHEN $3 = 'done' THEN $1::timestamptz END`, ch.Value); err != nil {
			return false, err
		}
		return true, record("status", StatusName(cur.Status), StatusName(ch.Value))
	case "priority":
		p, err := strconv.Atoi(ch.Value)
		if err != nil || p < 0 || p > 4 || p == cur.Priority {
			return false, nil
		}
		if err := set(`priority = $3`, p); err != nil {
			return false, err
		}
		return true, record("priority", PriorityName(cur.Priority), PriorityName(p))
	case "assignee":
		if ch.Value == cur.AssigneeID {
			return false, nil
		}
		if ch.Value != "" {
			// Only the team's members can be assigned.
			if _, err := loom.Get[int](ctx, tx, `SELECT 1 FROM team_members WHERE team_id = $1 AND user_id = $2`, cur.TeamID, ch.Value); err != nil {
				return false, err
			}
		}
		if err := set(`assignee_id = nullif($3, '')`, ch.Value); err != nil {
			return false, err
		}
		from, to, err := s.names(ctx, tx, cur.AssigneeID, ch.Value)
		if err != nil {
			return false, err
		}
		if err := record("assignee", from, to); err != nil {
			return false, err
		}
		if ch.Value != "" && ch.Value != actor {
			_, err = jobs.Enqueue(tx, Notify{UserID: ch.Value, IssueID: cur.ID, ActorID: actor, Kind: "assigned"})
		}
		return true, err
	case "title":
		title := strings.TrimSpace(ch.Value)
		if title == "" || title == cur.Title {
			return false, nil
		}
		if err := set(`title = $3`, title); err != nil {
			return false, err
		}
		return true, record("title", cur.Title, title)
	case "description":
		// Descriptions are saved as people edit them; recording each save
		// in the history would be noise.
		return true, set(`description = $3`, ch.Value)
	case "label":
		label, err := strconv.Atoi(ch.Value)
		if err != nil {
			return false, nil
		}
		name, err := loom.Get[string](ctx, tx, `SELECT name FROM labels WHERE id = $1 AND team_id = $2`, label, cur.TeamID)
		if err != nil {
			return false, err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM issue_labels WHERE issue_id = $1 AND label_id = $2`, cur.ID, label)
		if err != nil {
			return false, err
		}
		removed, err := tag.RowsAffected()
		if err != nil {
			return false, err
		}
		from, to := name, "" // removed
		if removed == 0 {
			from, to = "", name // added
			if _, err := tx.Exec(ctx, `INSERT INTO issue_labels (issue_id, label_id, team_id) VALUES ($1, $2, $3)`, cur.ID, label, cur.TeamID); err != nil {
				return false, err
			}
		}
		if err := set(`team_id = team_id`); err != nil { // only updated_at
			return false, err
		}
		return true, record("labels", from, to)
	}
	return false, errors.New("issues: unknown field " + ch.Field)
}

// Comment adds a comment to an issue, and notifies its assignee.
func (s *Store) Comment(ctx context.Context, actor string, issueID int, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	return s.DB.Tx(ctx, func(tx loom.Tx) error {
		type issue struct {
			TeamID     int
			AssigneeID string
		}
		is, err := loom.Get[issue](ctx, tx, `
			SELECT team_id, coalesce(assignee_id, '') AS assignee_id FROM issues
			WHERE id = $1 AND team_id IN (SELECT team_id FROM team_members WHERE user_id = $2)`, issueID, actor)
		if errors.Is(err, loom.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO comments (issue_id, team_id, author_id, body, created_at) VALUES ($1, $2, $3, $4, $5)`,
			issueID, is.TeamID, actor, body, s.Clock.Now()); err != nil {
			return err
		}
		if is.AssigneeID != "" && is.AssigneeID != actor {
			if _, err := jobs.Enqueue(tx, Notify{UserID: is.AssigneeID, IssueID: issueID, ActorID: actor, Kind: "commented"}); err != nil {
				return err
			}
		}
		s.announce(tx, is.TeamID, issueID)
		return nil
	})
}

// record adds an entry to an issue's history.
func (s *Store) record(ctx context.Context, tx loom.Tx, issueID, teamID int, actor, kind, from, to string) error {
	_, err := tx.Exec(ctx, `INSERT INTO issue_events (issue_id, team_id, actor_id, kind, from_value, to_value, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		issueID, teamID, actor, kind, from, to, s.Clock.Now())
	return err
}

// names returns the names of two users, "" for none.
func (s *Store) names(ctx context.Context, q loom.Querier, a, b string) (string, string, error) {
	name := func(id string) (string, error) {
		if id == "" {
			return "", nil
		}
		return loom.Get[string](ctx, q, `SELECT name FROM users WHERE id = $1`, id)
	}
	x, err := name(a)
	if err != nil {
		return "", "", err
	}
	y, err := name(b)
	return x, y, err
}

// announce tells the team's open pages about the change once tx commits.
func (s *Store) announce(tx loom.Tx, teamID, issueID int) {
	tx.AfterCommit(func() {
		s.PubSub.Publish(pubsub.Message{Topic: TeamTopic(teamID), Event: "changed", Payload: map[string]string{"issue": strconv.Itoa(issueID)}})
	})
}

// --- The inbox ---

// NotificationRow is an entry in a user's inbox.
type NotificationRow struct {
	ID         int
	Kind       string // assigned or commented
	Actor      User
	Identifier string
	Title      string
	Status     string
	When       string
	Unread     bool
}

// Notifications returns a user's newest 100 notifications.
func (s *Store) Notifications(ctx context.Context, userID string) ([]NotificationRow, error) {
	type record struct {
		ID         int
		Kind       string
		ActorID    string
		Identifier string
		Title      string
		Status     string
		CreatedAt  time.Time
		Unread     bool
	}
	recs, err := loom.Select[record](ctx, s.DB, `
		SELECT n.id, n.kind, n.actor_id, t.key || '-' || i.number AS identifier, i.title, i.status,
			n.created_at, n.read_at IS NULL AS unread
		FROM notifications n JOIN issues i ON i.id = n.issue_id JOIN teams t ON t.id = i.team_id
		WHERE n.user_id = $1 ORDER BY n.id DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	users, err := s.userMap(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Clock.Now()
	rows := make([]NotificationRow, len(recs))
	for i, r := range recs {
		rows[i] = NotificationRow{ID: r.ID, Kind: r.Kind, Actor: users[r.ActorID], Identifier: r.Identifier,
			Title: r.Title, Status: r.Status, When: ago(now, r.CreatedAt), Unread: r.Unread}
	}
	return rows, nil
}

// Unread counts a user's unread notifications.
func (s *Store) Unread(ctx context.Context, userID string) (int, error) {
	return loom.Get[int](ctx, s.DB, `SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, userID)
}

// MarkIssueRead marks a user's notifications about an issue read: they
// have seen it.
func (s *Store) MarkIssueRead(ctx context.Context, userID string, issueID int) error {
	tag, err := s.DB.Exec(ctx, `UPDATE notifications SET read_at = $3 WHERE user_id = $1 AND issue_id = $2 AND read_at IS NULL`, userID, issueID, s.Clock.Now())
	if err != nil {
		return err
	}
	if n, _ := tag.RowsAffected(); n > 0 {
		s.PubSub.Publish(pubsub.Message{Topic: InboxTopic(userID), Event: "read"})
	}
	return nil
}

// MarkRead marks a user's notification read, or all of them for id 0.
func (s *Store) MarkRead(ctx context.Context, userID string, id int) error {
	return s.DB.Tx(ctx, func(tx loom.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE notifications SET read_at = $3 WHERE user_id = $1 AND ($2 = 0 OR id = $2) AND read_at IS NULL`, userID, id, s.Clock.Now()); err != nil {
			return err
		}
		tx.AfterCommit(func() { s.PubSub.Publish(pubsub.Message{Topic: InboxTopic(userID), Event: "read"}) })
		return nil
	})
}
