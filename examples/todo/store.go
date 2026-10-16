package todo

import (
	"context"
	"errors"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/examples/todo/templates"
	"github.com/moomerman/loom/jobs"
)

// Todos stores the todo lists in Postgres (migrations/), one per user.
// The todos table is tracked (-- loom:track), so every committed change
// re-renders the pages that watch it; nothing here needs to tell them.
type Todos struct {
	DB    *loom.DB
	Clock jobs.Clock // when todos are done, for PruneCompleted
}

// columns are a Todo's columns; inserted_at as RFC 3339 text in UTC.
const columns = `id::text, title, done, to_char(inserted_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS inserted_at`

// List returns a user's todos in the order they were added.
func (s *Todos) List(ctx context.Context, user string) ([]templates.Todo, error) {
	return loom.Select[templates.Todo](ctx, s.DB, `SELECT `+columns+` FROM todos WHERE user_id = $1 ORDER BY inserted_at, id`, user)
}

// Get returns one of a user's todos, or the zero Todo if they have none
// with that id.
func (s *Todos) Get(ctx context.Context, user, id string) (templates.Todo, error) {
	t, err := loom.Get[templates.Todo](ctx, s.DB, `SELECT `+columns+` FROM todos WHERE user_id = $1 AND id::text = $2`, user, id)
	if errors.Is(err, loom.ErrNotFound) {
		return templates.Todo{}, nil
	}
	return t, err
}

// Add appends a todo to a user's list.
func (s *Todos) Add(ctx context.Context, user, title string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO todos (id, user_id, title) VALUES (gen_random_uuid(), $1, $2)`, user, title)
	return err
}

// Toggle flips one of a user's todos between done and not done, and
// records when it was done, for PruneCompleted.
func (s *Todos) Toggle(ctx context.Context, user, id string) error {
	_, err := s.DB.Exec(ctx, `UPDATE todos SET done = NOT done, done_at = CASE WHEN done THEN NULL ELSE $3::timestamptz END WHERE user_id = $1 AND id::text = $2`, user, id, s.Clock.Now())
	return err
}

// Delete removes one of a user's todos.
func (s *Todos) Delete(ctx context.Context, user, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM todos WHERE user_id = $1 AND id::text = $2`, user, id)
	return err
}

// ClearCompleted removes a user's done todos.
func (s *Todos) ClearCompleted(ctx context.Context, user string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM todos WHERE user_id = $1 AND done`, user)
	return err
}

// PruneDone deletes the todos done before cutoff and returns how many it
// deleted for each user.
func (s *Todos) PruneDone(ctx context.Context, cutoff time.Time) (map[string]int, error) {
	users, err := loom.Select[string](ctx, s.DB, `DELETE FROM todos WHERE done AND done_at < $1 RETURNING user_id`, cutoff)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, u := range users {
		counts[u]++
	}
	return counts, nil
}
