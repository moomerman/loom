package issues

import (
	"context"
	"time"

	"github.com/moomerman/loom/jobs"
)

// Notify puts an entry in a user's inbox: someone assigned them an issue
// or commented on one of theirs. It is enqueued in the transaction that
// makes the change, so the notification exists if and only if the change
// does, and it runs in the background, so the change doesn't wait for it.
type Notify struct {
	UserID  string `json:"user_id"`
	IssueID int    `json:"issue_id"`
	ActorID string `json:"actor_id"`
	Kind    string `json:"kind"` // assigned or commented

	Store *Store `json:"-"` // set on the prototype given to app.Jobs
}

// Options puts notifications on their own queue, so a burst of them
// never delays other work.
func (Notify) Options() jobs.Options {
	return jobs.Options{Queue: "notifications", MaxAttempts: 5, Timeout: 10 * time.Second}
}

// Perform inserts the notification and tells the user's open pages.
// Jobs run at least once: the notification is keyed by the job's id, so
// a second run inserts nothing, and the broadcast only makes pages
// re-read their unread count.
func (j Notify) Perform(ctx context.Context, c *jobs.Ctx) error {
	if _, err := j.Store.DB.Exec(ctx, `
		INSERT INTO notifications (job_id, user_id, issue_id, actor_id, kind, created_at)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (job_id) DO NOTHING`,
		c.ID(), j.UserID, j.IssueID, j.ActorID, j.Kind, c.Now()); err != nil {
		return err
	}
	return c.Broadcast(InboxTopic(j.UserID), "notified", nil)
}
