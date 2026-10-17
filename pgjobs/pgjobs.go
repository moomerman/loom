// Package pgjobs is the Postgres store for Loom jobs. It fetches jobs with
// FOR UPDATE SKIP LOCKED, wakes workers with LISTEN/NOTIFY, and elects the
// cron leader with an advisory lock.
//
//	db, _ := loom.OpenDB(ctx, os.Getenv("DATABASE_URL"))
//	store := pgjobs.New(db, pgjobs.Options{})
//	app.JobStore(store)
//
// Its tables, loom_jobs and loom_nodes (the node registry), are among
// Loom's own migrations (`loom db install github.com/moomerman/loom`,
// then `loom migrate`); the app checks they are applied at startup
// (RT433).
//
// Jobs are enqueued in the same transaction as the data change:
//
//	db.Tx(ctx, func(tx loom.Tx) error {
//		tx.Exec(ctx, "INSERT INTO users ...")
//		_, err := jobs.Enqueue(tx, SendWelcome{UserID: id})
//		return err
//	})
package pgjobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/trace"
)

// Options configure a Store.
type Options struct {
	// Clock is the store's clock; tests use a jobs.FakeClock. Default
	// jobs.RealClock.
	Clock jobs.Clock
}

// Store is a jobs.Store backed by Postgres. Each process should have one.
type Store struct {
	ldb   *data.DB
	db    *sql.DB
	clock jobs.Clock

	events atomic.Pointer[trace.Stream] // where it reports (Observe)

	mu         sync.Mutex
	leaderConn *sql.Conn // holds the advisory lock while this node leads
	subs       map[int]func(string)
	nextSub    int
	listening  bool
	stopListen context.CancelFunc
	listenDone chan struct{}
}

const (
	channel   = "loom_jobs"
	leaderKey = 0x6c6f6f6d6a6f6273 // "loomjobs": the advisory lock for cron leadership
)

// New returns a store on db, bound to it so jobs.Enqueue in db's
// transactions inserts here. db must be a Postgres database (from
// loom.OpenDB), whose loom_jobs table exists: see the package doc.
func New(db *data.DB, opts Options) *Store {
	if db.SQL() == nil {
		panic(&jobs.Error{Code: jobs.JB512, Msg: "pgjobs.New needs a Postgres database, but this DB has none", Fix: "open one with loom.OpenDB(ctx, url), or use jobs.NewMemoryStore"})
	}
	c := opts.Clock
	if c == nil {
		c = jobs.RealClock{}
	}
	s := &Store{ldb: db, db: db.SQL(), clock: c, subs: map[int]func(string){}}
	jobs.Bind(db, s)
	return s
}

// Observe makes the store report to stream: jobs.listen_lost when its
// LISTEN connection drops. The app calls it.
func (s *Store) Observe(stream *trace.Stream) { s.events.Store(stream) }

// DB returns the store's database.
func (s *Store) DB() *data.DB { return s.ldb }

// Clock returns the store's clock.
func (s *Store) Clock() jobs.Clock { return s.clock }

// uniqueSQL finds an existing unique job. Its non-empty unique_key clause
// repeats the loom_jobs_unique index's predicate: a prepared statement's
// generic plan can't infer it from unique_key = $1, and without it Postgres
// scans the table on every unique insert, cron's included.
const uniqueSQL = `
	SELECT id FROM loom_jobs
	WHERE unique_key = $1 AND unique_key <> '' AND inserted_at >= $2 AND state NOT IN ('discarded', 'cancelled')
	ORDER BY inserted_at, id LIMIT 1`

// fetchSQL claims due jobs. Its WHERE clause states the loom_jobs_fetch
// index's predicate as the index is written, so the planner walks that
// index in (queue, scheduled_at, id) order and stops at the limit,
// instead of combining indexes or scanning the table.
const fetchSQL = `
	UPDATE loom_jobs SET state = 'executing', attempt = attempt + 1, attempted_at = $3, attempted_by = $4, lease_until = $5
	WHERE id IN (
		SELECT id FROM loom_jobs
		WHERE queue = $1 AND state IN ('available', 'scheduled', 'retryable') AND (state = 'available' OR scheduled_at <= $3)
		ORDER BY scheduled_at, id
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	)
	RETURNING ` + columns

// Insert inserts a job in tx. For a unique job it first takes a
// transaction-scoped advisory lock on the key, so concurrent inserts of
// the same unique job serialize, and the second sees the first once it
// commits.
func (s *Store) Insert(t data.Tx, j jobs.NewJob) (jobs.Result, error) {
	ctx := t.Context()
	if j.UniqueKey != "" {
		if _, err := t.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, j.UniqueKey); err != nil {
			return jobs.Result{}, err
		}
		var id int64
		err := t.QueryRow(ctx, uniqueSQL, j.UniqueKey, j.UniqueSince).Scan(&id)
		switch {
		case err == nil:
			return jobs.Result{ID: id, Duplicate: true}, nil
		case !errors.Is(err, sql.ErrNoRows):
			return jobs.Result{}, err
		}
	}
	state := jobs.Available
	if j.ScheduledAt.After(j.InsertedAt) {
		state = jobs.Scheduled
	}
	var id int64
	err := t.QueryRow(ctx, `
		INSERT INTO loom_jobs (type, queue, args, state, max_attempts, inserted_at, scheduled_at, unique_key, trace)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		j.Type, j.Queue, string(j.Args), state, j.MaxAttempts, j.InsertedAt, j.ScheduledAt, j.UniqueKey, j.Trace).Scan(&id)
	if err != nil {
		return jobs.Result{}, err
	}
	if state == jobs.Available {
		// Delivered to listeners only if the transaction commits.
		if _, err := t.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, j.Queue); err != nil {
			return jobs.Result{}, err
		}
	}
	return jobs.Result{ID: id}, nil
}

const columns = `id, type, queue, args, state, attempt, max_attempts, inserted_at, scheduled_at,
	attempted_at, finished_at, attempted_by, lease_until, errors, unique_key, trace`

type scanner interface{ Scan(dest ...any) error }

func scan(row scanner) (*jobs.Record, error) {
	var r jobs.Record
	var args string
	var errs []byte
	var attempted, finished, lease sql.NullTime
	if err := row.Scan(&r.ID, &r.Type, &r.Queue, &args, &r.State, &r.Attempt, &r.MaxAttempts, &r.InsertedAt, &r.ScheduledAt,
		&attempted, &finished, &r.AttemptedBy, &lease, &errs, &r.UniqueKey, &r.Trace); err != nil {
		return nil, err
	}
	r.Args = json.RawMessage(args)
	r.InsertedAt, r.ScheduledAt = r.InsertedAt.UTC(), r.ScheduledAt.UTC()
	r.AttemptedAt, r.FinishedAt, r.LeaseUntil = nullTime(attempted), nullTime(finished), nullTime(lease)
	r.Errors = []jobs.AttemptError{}
	if err := json.Unmarshal(errs, &r.Errors); err != nil {
		return nil, err
	}
	for i := range r.Errors {
		r.Errors[i].At = r.Errors[i].At.UTC()
	}
	return &r, nil
}

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

func scanAll(rows *sql.Rows) ([]*jobs.Record, error) {
	defer rows.Close()
	out := []*jobs.Record{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Fetch claims due jobs with FOR UPDATE SKIP LOCKED, so concurrent
// fetches never block on or return the same job.
func (s *Store) Fetch(ctx context.Context, r jobs.FetchRequest) ([]*jobs.Record, error) {
	rows, err := s.db.QueryContext(ctx, fetchSQL, r.Queue, r.Limit, r.Now, r.Node, r.LeaseUntil)
	if err != nil {
		return nil, err
	}
	recs, err := scanAll(rows)
	if err != nil {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool {
		if !recs[i].ScheduledAt.Equal(recs[j].ScheduledAt) {
			return recs[i].ScheduledAt.Before(recs[j].ScheduledAt)
		}
		return recs[i].ID < recs[j].ID
	})
	return recs, nil
}

func errorJSON(e *jobs.AttemptError) (string, error) {
	if e == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]jobs.AttemptError{*e})
	return string(b), err
}

// Finish ends an attempt, if the job is still held by f.Node at f.Attempt.
func (s *Store) Finish(ctx context.Context, id int64, f jobs.Finish) error {
	errs, err := errorJSON(f.Error)
	if err != nil {
		return err
	}
	uncount := 0
	if f.Uncount {
		uncount = 1
	}
	var finishedAt, scheduledAt any
	if f.State.Finished() {
		finishedAt = f.At
	} else {
		scheduledAt = f.At
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE loom_jobs SET state = $4, lease_until = NULL, errors = errors || $5::jsonb, attempt = attempt - $6,
			finished_at = COALESCE($7, finished_at), scheduled_at = COALESCE($8, scheduled_at)
		WHERE id = $1 AND state = 'executing' AND attempted_by = $2 AND attempt = $3`,
		id, f.Node, f.Attempt, f.State, errs, uncount, finishedAt, scheduledAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return jobs.ErrLeaseLost
	}
	if f.State == jobs.Available {
		if err := notifyQueueOf(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func notifyQueueOf(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_notify($1, queue) FROM loom_jobs WHERE id = $2`, channel, id)
	return err
}

// ExtendLeases renews the leases of jobs executing on node.
func (s *Store) ExtendLeases(ctx context.Context, node string, ids []int64, until time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE loom_jobs SET lease_until = $3
		WHERE id = ANY($2) AND state = 'executing' AND attempted_by = $1`, node, ids, until)
	return err
}

// Rescue frees executing jobs whose lease ended before now, to run
// again after jobs.RescueDelay; a job rescued jobs.MaxRescues times is
// discarded (JB517).
func (s *Store) Rescue(ctx context.Context, now time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		WITH due AS (
			SELECT id, attempt >= max_attempts AS spent,
				(SELECT count(*) FROM jsonb_array_elements(errors) e WHERE e->>'code' = $3) AS rescues
			FROM loom_jobs WHERE state = 'executing' AND lease_until < $1
			FOR UPDATE
		)
		UPDATE loom_jobs j SET
			state = CASE WHEN d.rescues + 1 >= $7 OR d.spent THEN 'discarded' ELSE 'retryable' END,
			finished_at = CASE WHEN d.rescues + 1 >= $7 OR d.spent THEN $1 ELSE j.finished_at END,
			scheduled_at = CASE WHEN d.rescues + 1 >= $7 OR d.spent THEN j.scheduled_at
				ELSE $1 + make_interval(secs => $8 * power(2, d.rescues)) END,
			lease_until = NULL,
			errors = j.errors || jsonb_build_array(jsonb_build_object('attempt', j.attempt, 'at', $2::text,
				'code', CASE WHEN d.rescues + 1 >= $7 THEN $5 ELSE $3 END, 'node', j.attempted_by,
				'error', replace(CASE WHEN d.rescues + 1 >= $7 THEN $6 ELSE $4 END, $9, j.attempted_by)))
		FROM due d WHERE j.id = d.id
		RETURNING j.queue, j.state`, now, now.Format(time.RFC3339Nano),
		jobs.JB511, jobs.RescuedError(0, now, nodePlaceholder).Error, jobs.JB517, jobs.CrashedError(0, now, nodePlaceholder).Error,
		jobs.MaxRescues, jobs.RescueDelay(0).Seconds(), nodePlaceholder)
	if err != nil {
		return 0, err
	}
	n := 0
	queues := map[string]bool{}
	for rows.Next() {
		var q, st string
		if err := rows.Scan(&q, &st); err != nil {
			rows.Close()
			return 0, err
		}
		n++
		if st == string(jobs.Retryable) {
			queues[q] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for q := range queues {
		if _, err := tx.ExecContext(ctx, `SELECT pg_notify($1, $2)`, channel, q); err != nil {
			return 0, err
		}
	}
	return n, tx.Commit()
}

// nodePlaceholder stands for the node's name in the rescue errors'
// text, replaced by each job's attempted_by.
const nodePlaceholder = "{node}"

// pruneBatch is how many jobs one Prune statement deletes, so a large
// backlog is deleted in short transactions rather than one long one.
const pruneBatch = 5000

// Prune deletes finished jobs older than before, pruneBatch at a time.
func (s *Store) Prune(ctx context.Context, before time.Time) (int, error) {
	total := 0
	for {
		res, err := s.db.ExecContext(ctx, `
			DELETE FROM loom_jobs WHERE id IN (
				SELECT id FROM loom_jobs
				WHERE state IN ('completed', 'discarded', 'cancelled') AND finished_at < $1
				LIMIT $2
			)`, before, pruneBatch)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		total += int(n)
		if err != nil || n < pruneBatch {
			return total, err
		}
	}
}

// Get returns a job.
func (s *Store) Get(ctx context.Context, id int64) (*jobs.Record, error) {
	r, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM loom_jobs WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, jobs.ErrNotFound
	}
	return r, err
}

// List returns matching jobs, newest first.
func (s *Store) List(ctx context.Context, f jobs.Filter) ([]*jobs.Record, error) {
	var where []string
	var args []any
	add := func(col, v string) {
		if v != "" {
			args = append(args, v)
			where = append(where, fmt.Sprintf("%s = $%d", col, len(args)))
		}
	}
	add("queue", f.Queue)
	add("state", string(f.State))
	add("type", f.Type)
	if f.Before != 0 {
		args = append(args, f.Before)
		where = append(where, fmt.Sprintf("id < $%d", len(args)))
	}
	if f.Search != "" {
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(f.Search)+"%")
		where = append(where, fmt.Sprintf("args::text ILIKE $%d", len(args)))
	}
	q := `SELECT ` + columns + ` FROM loom_jobs`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanAll(rows)
}

// change runs an UPDATE on one job guarded by a state condition, and
// distinguishes a missing job (ErrNotFound) from a state that doesn't
// allow it (ErrState).
func (s *Store) change(ctx context.Context, id int64, notify bool, query string, args ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, query, append([]any{id}, args...)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM loom_jobs WHERE id = $1)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return jobs.ErrNotFound
		}
		return jobs.ErrState
	}
	if notify {
		if err := notifyQueueOf(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Retry makes a job run now.
func (s *Store) Retry(ctx context.Context, id int64, now time.Time) error {
	return s.change(ctx, id, true, `
		UPDATE loom_jobs SET state = 'available', scheduled_at = $2, finished_at = NULL, max_attempts = GREATEST(max_attempts, attempt + 1)
		WHERE id = $1 AND state NOT IN ('executing', 'available')`, now)
}

// Cancel cancels an unfinished job.
func (s *Store) Cancel(ctx context.Context, id int64, now time.Time) error {
	return s.change(ctx, id, false, `
		UPDATE loom_jobs SET state = 'cancelled', finished_at = $2, lease_until = NULL
		WHERE id = $1 AND state NOT IN ('completed', 'discarded', 'cancelled')`, now)
}

// Delete deletes a job that isn't executing.
func (s *Store) Delete(ctx context.Context, id int64) error {
	return s.change(ctx, id, false, `DELETE FROM loom_jobs WHERE id = $1 AND state <> 'executing'`)
}

// Stats counts jobs by queue and state.
func (s *Store) Stats(ctx context.Context) ([]jobs.QueueStats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT queue, state, count(*) FROM loom_jobs GROUP BY queue, state ORDER BY queue`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []jobs.QueueStats{}
	for rows.Next() {
		var q string
		var st jobs.State
		var n int
		if err := rows.Scan(&q, &st, &n); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Queue != q {
			out = append(out, jobs.QueueStats{Queue: q, States: map[jobs.State]int{}})
		}
		out[len(out)-1].States[st] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The fetch index covers the waiting jobs, so this reads one row per queue.
	for i := range out {
		var at sql.NullTime
		err := s.db.QueryRowContext(ctx, `SELECT min(scheduled_at) FROM loom_jobs WHERE queue = $1 AND state IN ('available', 'scheduled', 'retryable')`, out[i].Queue).Scan(&at)
		if err != nil {
			return nil, err
		}
		if at.Valid {
			out[i].Waiting = at.Time
		}
	}
	return out, nil
}

// TryLead takes or keeps a session-level advisory lock on a dedicated
// connection. If this process dies, Postgres drops the connection and
// the lock with it, and another node takes over.
func (s *Store) TryLead(ctx context.Context, node string, now, until time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leaderConn != nil {
		if err := s.leaderConn.PingContext(ctx); err == nil {
			return true, nil
		}
		s.leaderConn.Close() // the connection died, and the lock with it
		s.leaderConn = nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	var ok bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, int64(leaderKey)).Scan(&ok); err != nil {
		conn.Close()
		return false, err
	}
	if !ok {
		conn.Close()
		return false, nil
	}
	s.leaderConn = conn
	return true, nil
}

// Resign releases the leader lock if this store holds it.
func (s *Store) Resign(ctx context.Context, node string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leaderConn == nil {
		return nil
	}
	_, err := s.leaderConn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, int64(leaderKey))
	s.leaderConn.Close()
	s.leaderConn = nil
	return err
}

// Heartbeat records a node in loom_nodes, replacing its entry.
func (s *Store) Heartbeat(ctx context.Context, n jobs.Node) error {
	queues := n.Queues
	if queues == nil {
		queues = []jobs.NodeQueue{}
	}
	q, err := json.Marshal(queues)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO loom_nodes (name, queues, leader, started_at, seen_at, lease_until)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (name) DO UPDATE SET queues = $2, leader = $3, started_at = $4, seen_at = $5, lease_until = $6`,
		n.Name, string(q), n.Leader, n.StartedAt, n.SeenAt, n.LeaseUntil)
	return err
}

// Nodes returns loom_nodes, by name.
func (s *Store) Nodes(ctx context.Context) ([]jobs.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, queues, leader, started_at, seen_at, lease_until FROM loom_nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []jobs.Node{}
	for rows.Next() {
		var n jobs.Node
		var q []byte
		if err := rows.Scan(&n.Name, &q, &n.Leader, &n.StartedAt, &n.SeenAt, &n.LeaseUntil); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(q, &n.Queues); err != nil {
			return nil, err
		}
		n.StartedAt, n.SeenAt, n.LeaseUntil = n.StartedAt.UTC(), n.SeenAt.UTC(), n.LeaseUntil.UTC()
		out = append(out, n)
	}
	return out, rows.Err()
}

// RemoveNode deletes a node's entry.
func (s *Store) RemoveNode(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM loom_nodes WHERE name = $1`, name)
	return err
}

// PruneNodes deletes entries whose lease ended before before.
func (s *Store) PruneNodes(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM loom_nodes WHERE lease_until < $1`, before)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// Subscribe calls fn with a queue's name whenever a NOTIFY says jobs may be
// available in it. The first subscriber starts a LISTEN connection.
func (s *Store) Subscribe(fn func(string)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSub++
	id := s.nextSub
	s.subs[id] = fn
	if !s.listening {
		s.listening = true
		ctx, cancel := context.WithCancel(context.Background())
		s.stopListen, s.listenDone = cancel, make(chan struct{})
		ready := make(chan struct{})
		go s.listen(ctx, ready)
		// Wait briefly for LISTEN, so jobs enqueued right after Subscribe
		// wake workers. If the database is slow, polling covers the gap.
		s.mu.Unlock()
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
		}
		s.mu.Lock()
	}
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subs, id)
	}
}

// listen holds a LISTEN connection, reconnecting after errors. Workers
// also poll, so a missed notification only delays a job.
func (s *Store) listen(ctx context.Context, ready chan struct{}) {
	defer close(s.listenDone)
	var once sync.Once
	signal := func() { once.Do(func() { close(ready) }) }
	for ctx.Err() == nil {
		err := s.listenOnce(ctx, signal)
		if ctx.Err() != nil {
			return
		}
		// Workers poll meanwhile, so jobs are only slower to start.
		s.events.Load().Emit(ctx, trace.JobsListenLost, "msg", "loom jobs: LISTEN connection lost; reconnecting", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (s *Store) listenOnce(ctx context.Context, listening func()) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		sc, ok := dc.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("pgjobs: the database must use the pgx driver (sql.Open(\"pgx\", url)), got %T", dc)
		}
		pc := sc.Conn()
		if _, err := pc.Exec(ctx, "LISTEN "+channel); err != nil {
			return err
		}
		listening()
		for {
			n, err := pc.WaitForNotification(ctx)
			if err != nil {
				return err
			}
			s.mu.Lock()
			fns := make([]func(string), 0, len(s.subs))
			for _, fn := range s.subs {
				fns = append(fns, fn)
			}
			s.mu.Unlock()
			for _, fn := range fns {
				fn(n.Payload)
			}
		}
	})
}

// Close stops listening and gives up leadership. It doesn't close the
// *sql.DB, which belongs to the application.
func (s *Store) Close() error {
	s.mu.Lock()
	stop, done := s.stopListen, s.listenDone
	s.listening, s.stopListen = false, nil
	s.mu.Unlock()
	if stop != nil {
		stop()
		<-done
	}
	return s.Resign(context.Background(), "")
}
