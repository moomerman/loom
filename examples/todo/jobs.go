package todo

import (
	"context"
	"time"

	"github.com/moomerman/loom/jobs"
)

// PruneCompleted deletes todos completed more than a week ago. The
// pages watching the todos table show the deletes. The app runs it daily at
// 03:00 UTC.
type PruneCompleted struct {
	Todos *Todos `json:"-"` // set on the prototype given to app.Jobs
}

// Perform prunes. Running it twice is harmless: the second run finds
// nothing to remove.
func (j PruneCompleted) Perform(ctx context.Context, c *jobs.Ctx) error {
	_, err := j.Todos.PruneDone(ctx, c.Now().Add(-7*24*time.Hour))
	return err
}
