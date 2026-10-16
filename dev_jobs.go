package loom

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/moomerman/loom/jobs"
)

// Dev endpoints for background jobs, behind `loom jobs`, `loom queues`,
// `loom nodes` and `loom cron`. The JSON shapes are documented in docs/cli.md.
func (a *App) devJobs(m *http.ServeMux) {
	m.HandleFunc("GET /_loom/dev/jobs", func(w http.ResponseWriter, r *http.Request) {
		if a.jobs == nil {
			writeJSON(w, []*jobs.Record{})
			return
		}
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
		list, err := a.jobs.Jobs(r.Context(), jobs.Filter{Queue: q.Get("queue"), State: jobs.State(q.Get("state")), Type: q.Get("type"), Search: q.Get("search"), Before: before, Limit: limit})
		if err != nil {
			writeJobsError(w, err)
			return
		}
		writeJSON(w, list)
	})
	m.HandleFunc("GET /_loom/dev/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.withJob(w, r, func(id int64) error { return nil })
	})
	m.HandleFunc("POST /_loom/dev/jobs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		a.withJob(w, r, func(id int64) error { return a.jobs.Retry(r.Context(), id) })
	})
	m.HandleFunc("POST /_loom/dev/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		a.withJob(w, r, func(id int64) error { return a.jobs.Cancel(r.Context(), id) })
	})
	m.HandleFunc("POST /_loom/dev/jobs/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		id, ok := a.jobID(w, r)
		if !ok {
			return
		}
		if err := a.jobs.Delete(r.Context(), id); err != nil {
			writeJobsError(w, err)
			return
		}
		writeJSON(w, map[string]any{"id": id, "deleted": true})
	})
	m.HandleFunc("POST /_loom/dev/jobs/retry-discarded", func(w http.ResponseWriter, r *http.Request) {
		if a.jobs == nil {
			writeJSON(w, map[string]int{"retried": 0})
			return
		}
		n, err := a.jobs.RetryDiscarded(r.Context(), r.URL.Query().Get("queue"))
		if err != nil {
			writeJobsError(w, err)
			return
		}
		writeJSON(w, map[string]int{"retried": n})
	})
	m.HandleFunc("POST /_loom/dev/cron/{index}/run", func(w http.ResponseWriter, r *http.Request) {
		i, err := strconv.Atoi(r.PathValue("index"))
		if err != nil || a.jobs == nil {
			writeJobsError(w, &jobs.Error{Code: jobs.JB515, Msg: fmt.Sprintf("no cron entry %q", r.PathValue("index")), Fix: "use an index from `loom cron --json`"})
			return
		}
		res, err := a.jobs.RunCron(r.Context(), i)
		if err != nil {
			writeJobsError(w, err)
			return
		}
		writeJSON(w, res)
	})
	m.HandleFunc("POST /_loom/dev/jobs", func(w http.ResponseWriter, r *http.Request) {
		typ := r.URL.Query().Get("type")
		if a.jobs == nil {
			writeJobsError(w, &jobs.Error{Code: jobs.JB501, Msg: fmt.Sprintf("job type %s is not registered: this app has no jobs", typ), Fix: "register job types at startup with app.Jobs(...)"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeJobsError(w, &jobs.Error{Code: jobs.JB509, Msg: err.Error()})
			return
		}
		var opts []jobs.Option
		if in := r.URL.Query().Get("in"); in != "" {
			d, err := time.ParseDuration(in)
			if err != nil {
				writeJobsError(w, &jobs.Error{Code: jobs.JB509, Msg: fmt.Sprintf("invalid delay %q: %v", in, err), Fix: "give a Go duration, e.g. --in 10m"})
				return
			}
			opts = append(opts, jobs.In(d))
		}
		res, err := a.jobs.EnqueueJSON(r.Context(), typ, body, opts...)
		if err != nil {
			writeJobsError(w, err)
			return
		}
		writeJSON(w, res)
	})
	m.HandleFunc("GET /_loom/dev/queues", func(w http.ResponseWriter, r *http.Request) {
		if a.jobs == nil {
			writeJSON(w, []jobs.QueueInfo{})
			return
		}
		qs, err := a.jobs.QueueInfo(r.Context())
		if err != nil {
			writeJobsError(w, err)
			return
		}
		writeJSON(w, qs)
	})
	m.HandleFunc("GET /_loom/dev/nodes", func(w http.ResponseWriter, r *http.Request) {
		if a.jobs == nil {
			writeJSON(w, []jobs.NodeInfo{})
			return
		}
		ns, err := a.jobs.Nodes(r.Context())
		if err != nil {
			writeJobsError(w, err)
			return
		}
		writeJSON(w, ns)
	})
	m.HandleFunc("GET /_loom/dev/cron", func(w http.ResponseWriter, r *http.Request) {
		if a.jobs == nil {
			writeJSON(w, []jobs.CronInfo{})
			return
		}
		cs, err := a.jobs.CronInfo(r.Context())
		if err != nil {
			writeJobsError(w, err)
			return
		}
		writeJSON(w, cs)
	})
}

// jobID reads the job id in the path, answering an error if there is no
// such job.
func (a *App) jobID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJobsError(w, &jobs.Error{Code: jobs.JB507, Msg: fmt.Sprintf("%q is not a job id", r.PathValue("id")), Fix: "job ids are numbers; list jobs with `loom jobs`"})
		return 0, false
	}
	if a.jobs == nil {
		writeJobsError(w, &jobs.Error{Code: jobs.JB507, Msg: fmt.Sprintf("no job %d: this app has no jobs", id), Fix: "register job types at startup with app.Jobs(...)"})
		return 0, false
	}
	return id, true
}

// withJob runs op on the job named in the path, then answers with the
// job's details.
func (a *App) withJob(w http.ResponseWriter, r *http.Request, op func(id int64) error) {
	id, ok := a.jobID(w, r)
	if !ok {
		return
	}
	if err := op(id); err != nil {
		writeJobsError(w, err)
		return
	}
	d, err := a.jobs.Job(r.Context(), id)
	if err != nil {
		writeJobsError(w, err)
		return
	}
	writeJSON(w, d)
}

func writeJobsError(w http.ResponseWriter, err error) {
	var je *jobs.Error
	if errors.As(err, &je) {
		status := http.StatusBadRequest
		if je.Code == jobs.JB507 {
			status = http.StatusNotFound
		}
		writeError(w, status, &Error{Code: je.Code, Msg: je.Msg, Fix: je.Fix})
		return
	}
	writeError(w, http.StatusInternalServerError, &Error{Code: jobs.JB513, Msg: "the job store failed: " + err.Error(), Fix: "check the store's database is reachable; the app's log has details"})
}
