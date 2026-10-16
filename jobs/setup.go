package jobs

import (
	"encoding/json"
	"reflect"
	"sort"
)

// Setup is what the app registered with the runner: job types, queues
// and cron entries, without the built-in prune. It reads no store, so
// it is the same on every run; the app's manifest (loom.App.Manifest)
// shows it for review.
type Setup struct {
	// Mode is what the node the manifest was made on does with jobs:
	// "run", "enqueue-only" or "jobs-only" (loom.Config.Jobs).
	Mode   string      `json:"mode"`
	Types  []TypeSetup `json:"types"`
	Queues []Queue     `json:"queues"`
	Cron   []CronSetup `json:"cron"`
}

// TypeSetup is a registered job type and its options.
type TypeSetup struct {
	Type        string   `json:"type"`
	Queue       string   `json:"queue"`
	MaxAttempts int      `json:"max_attempts"`
	Timeout     string   `json:"timeout"`
	Args        []string `json:"args"` // the JSON names of the args it carries
}

// CronSetup is a cron entry.
type CronSetup struct {
	Expr string `json:"expr"`
	Type string `json:"type"`
	Args string `json:"args"` // JSON
}

// Setup describes the registered types, queues and cron entries, sorted
// by type and queue name; cron entries in registration order. The
// default queue is listed even when it isn't configured, as it always
// runs.
func (r *Runner) Setup() Setup {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Setup{Types: []TypeSetup{}, Queues: []Queue{}, Cron: []CronSetup{}}
	prune := TypeName(Prune{})
	for _, name := range r.typeNames() {
		if name == prune {
			continue
		}
		j := r.types[name]
		o := OptionsOf(j)
		s.Types = append(s.Types, TypeSetup{Type: name, Queue: o.Queue, MaxAttempts: o.MaxAttempts, Timeout: o.Timeout.String(), Args: argNames(j)})
	}
	hasDefault := false
	for _, q := range r.queues {
		s.Queues = append(s.Queues, q.Queue)
		hasDefault = hasDefault || q.Name == DefaultQueue
	}
	if !hasDefault {
		s.Queues = append(s.Queues, Queue{Name: DefaultQueue, Concurrency: DefaultConcurrency})
	}
	sort.Slice(s.Queues, func(i, j int) bool { return s.Queues[i].Name < s.Queues[j].Name })
	for _, e := range r.cron {
		if !e.builtin {
			s.Cron = append(s.Cron, CronSetup{Expr: e.sched.expr, Type: e.typ, Args: string(e.args)})
		}
	}
	return s
}

// argNames returns the JSON names of a job type's args: the keys of its
// zero value encoded, so fields tagged `json:"-"` (dependencies) and
// unexported fields, which never reach the store, are left out.
func argNames(j Job) []string {
	zero := reflect.New(reflect.TypeOf(j)).Elem().Interface()
	b, err := json.Marshal(zero)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	names := []string{}
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
