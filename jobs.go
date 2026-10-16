package loom

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/pgjobs"
	"github.com/moomerman/loom/trace"
)

// Background jobs: the app owns a jobs.Runner, started by Run (or the
// first request) and stopped gracefully on shutdown. See docs/jobs.md.
//
//	app.UseDB(db)                                  // default: a local DB
//	app.JobStore(pgjobs.New(db, pgjobs.Options{})) // default: in memory
//	app.Jobs(SendWelcome{}, PruneSessions{DB: db})
//	app.Queues(jobs.Queue{Name: "mail", Concurrency: 10})
//	app.Cron("0 3 * * *", PruneSessions{})

// JobStore sets where jobs are stored. It must be called before any
// other jobs method. The store's DB becomes the app's DB (app.DB()); if
// the app already has one, it must be the same. The default is a
// jobs.MemoryStore on app.DB().
func (a *App) JobStore(s jobs.Store) {
	if a.jobs != nil {
		panic(&jobs.Error{Code: jobs.JB512, Msg: "JobStore was called after the job runner was created", Fix: "call app.JobStore before app.Jobs, app.Queues, app.Cron and app.JobRunner"})
	}
	if a.db != nil && a.db != s.DB() {
		panic(&jobs.Error{Code: jobs.JB512, Msg: "the job store uses a different database from the app's", Fix: "create the store on app.DB(), e.g. pgjobs.New(app.DB(), ...), so jobs commit with your data"})
	}
	a.db = s.DB()
	a.observe(a.db)
	a.jobStore = s
}

// UseJobRunner makes r the app's job runner, so app.Jobs, app.Queues
// and app.Cron (a sub-app's included) register on it, its store's DB
// becomes the app's DB as with JobStore, and its PubSub, if set, the
// app's. It is for tests that run
// jobs themselves (loomtest's JobQueue.Attach); Run starts r as it
// would its own. It must be called before any other jobs method.
func (a *App) UseJobRunner(r *jobs.Runner) {
	if a.jobs != nil {
		panic(&jobs.Error{Code: jobs.JB512, Msg: "UseJobRunner was called after the job runner was created", Fix: "call app.UseJobRunner before app.Jobs, app.Queues, app.Cron, app.JobRunner and mounting sub-apps that register jobs"})
	}
	a.JobStore(r.Store())
	if r.Events == nil {
		r.Events = a.stream
	}
	a.jobs = r
	if r.PubSub != nil {
		a.cfg.PubSub = r.PubSub // jobs' broadcasts reach the app's pages
	}
}

// JobRunner returns the app's job runner, creating it if needed; set its
// fields to tune it before Run.
func (a *App) JobRunner() *jobs.Runner {
	if a.jobs == nil {
		if a.jobStore == nil {
			if db := a.DB(); db.SQL() != nil {
				a.jobStore = pgjobs.New(db, pgjobs.Options{})
			} else {
				a.jobStore = jobs.NewMemoryStore(jobs.MemoryOptions{DB: db})
			}
		}
		s := a.jobStore
		r := jobs.NewRunner(s)
		r.Logger = a.cfg.Logger
		r.Events = a.stream // its errors reach `loom errors` from the stream
		r.PubSub = a.cfg.PubSub
		r.ShutdownGrace = a.cfg.ShutdownGrace
		r.PruneHooks = append(r.PruneHooks, func(ctx context.Context, now time.Time) error {
			if !a.storeClients(ctx) {
				return nil
			}
			return a.pruneClients(ctx, now.Add(-clientRetention)) // events handled per tab (outbox.go)
		})
		a.jobs = r
	}
	return a.jobs
}

// Jobs registers job types; see jobs.Runner.Register. It panics on an
// invalid type (JB502, JB503), at startup.
func (a *App) Jobs(js ...jobs.Job) {
	if err := a.JobRunner().Register(js...); err != nil {
		panic(err)
	}
}

// Queues configures job queues; see jobs.Runner.Queues.
func (a *App) Queues(qs ...jobs.Queue) {
	if err := a.JobRunner().Queues(qs...); err != nil {
		panic(err)
	}
}

// Cron enqueues job on a cron schedule, once per cluster; see
// jobs.Runner.Cron. It panics on an invalid expression (JB504).
func (a *App) Cron(expr string, job jobs.Job) {
	if err := a.JobRunner().Cron(expr, job); err != nil {
		panic(err)
	}
}

// startJobs starts the runner once, if the app uses jobs.
func (a *App) startJobs() error {
	if a.jobs == nil {
		return nil
	}
	a.jobsOnce.Do(func() {
		a.jobs.PubSub = a.cfg.PubSub
		if o, ok := a.jobStore.(interface{ Observe(*trace.Stream) }); ok {
			o.Observe(a.stream) // pgjobs reports its LISTEN connection
		}
		if a.cfg.Jobs == JobsEnqueueOnly {
			a.jobsErr = a.jobs.Validate() // other nodes run them
		} else {
			a.jobsErr = a.jobs.Start()
		}
		if a.jobsErr != nil {
			a.logError("", a.jobsErr)
		}
	})
	return a.jobsErr
}

func (a *App) shutdownJobs() {
	if a.jobs == nil {
		return
	}
	if err := a.jobs.Shutdown(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
		a.logError("", fmt.Errorf("shutting down jobs: %w", err))
	}
}
