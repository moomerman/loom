package jobsweb

import (
	"fmt"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
)

// OverviewPage answers "is the job system healthy?" at a glance: each
// queue's backlog, workers and last minute's throughput, the nodes
// running jobs, and the latest failures. It subscribes to jobs.Topic,
// so counts move as jobs run; the runner coalesces those announcements
// and shares one count query between pages, so an open dashboard costs
// little on a busy system. Nodes have a topic of their own,
// jobs.NodesTopic, so their heartbeats don't recount the jobs.
type OverviewPage struct {
	app     *App // shared by every copy of the page
	flash   string
	confirm *confirmation
	// nodes is the node registry, read on Mount and when a node reports
	// (loadNodes), not on every render: jobs change far more often.
	nodes    []jobs.NodeInfo
	nodesErr error
}

// QueueRow is a queue on the overview.
type QueueRow struct {
	Name        string
	Available   int
	Scheduled   int
	Retryable   int
	Running     int // executing on any node
	Concurrency int // workers on this node
	Completed   int // attempts that succeeded on this node in the last minute
	Failed      int // and that failed
	ErrorRate   string
	// Waiting is how long the oldest due job has waited for a worker,
	// and Stuck that it is over a minute: workers busy, hung or absent.
	Waiting string
	Stuck   bool
}

// NodeRow is a node on the overview.
type NodeRow struct {
	Name    string
	State   string // "up" or "stale"
	Leader  bool
	Self    bool // the node serving this dashboard
	Started string
	Seen    string
	Queues  []NodeQueueRow
}

// NodeQueueRow is a queue's workers on one node.
type NodeQueueRow struct {
	Name        string
	Running     int
	Concurrency int
}

// Mount subscribes to job and node changes.
func (p *OverviewPage) Mount(c *loom.Ctx) {
	section(c, "overview")
	c.Title("Overview")
	c.Subscribe(jobs.Topic)
	c.Subscribe(jobs.NodesTopic)
	p.loadNodes(c)
}

// loadNodes reads the node registry. A node that dies sends nothing, so
// the page also sets a timer for the end of the soonest lease, when that
// node turns stale unless its next heartbeat arrives first; the
// heartbeat reloads the nodes and moves the timer.
func (p *OverviewPage) loadNodes(c *loom.Ctx) {
	r := p.app.runner()
	p.nodes, p.nodesErr = r.Nodes(c.Context())
	now := r.Clock().Now()
	var next time.Time
	for _, n := range p.nodes {
		if n.State == jobs.NodeUp && (next.IsZero() || n.LeaseUntil.Before(next)) {
			next = n.LeaseUntil
		}
	}
	if next.IsZero() {
		c.CancelTimer("refresh_nodes")
		return
	}
	// A lease ends strictly after LeaseUntil, and the store's times
	// are rounded to the second.
	c.SendAfter(next.Sub(now)+time.Second, "refresh_nodes", nil)
}

// Events runs the overview's one action, retrying every discarded job,
// after the operator confirms it.
func (p *OverviewPage) Events() loom.Events {
	return loom.Events{
		"retry_discarded": func(c *loom.Ctx, _ loom.Payload) {
			p.flash, p.confirm = "", &confirmation{action: "retry_discarded", prompt: "Retry every discarded job?"}
		},
		"dismiss":       func(c *loom.Ctx, _ loom.Payload) { p.flash, p.confirm = "", nil },
		"refresh_nodes": func(c *loom.Ctx, _ loom.Payload) { p.loadNodes(c) }, // the page's own timer
		"confirm": func(c *loom.Ctx, _ loom.Payload) {
			if p.confirm != nil {
				p.flash, p.confirm = p.run(c, p.confirm.action), nil
			}
		},
	}
}

// run runs a confirmed action. Read-only operators never see the button,
// but an event can be sent without it, so the check is here too.
func (p *OverviewPage) run(c *loom.Ctx, action string) string {
	if p.app.readOnly(c) {
		return "You have read-only access."
	}
	n, err := p.app.runner().RetryDiscarded(c.Context(), "")
	if err != nil {
		return errorMessage(err)
	}
	return fmt.Sprintf("Retried %d discarded jobs.", n)
}

// HandleInfo reloads the nodes when one reports. Job changes need
// nothing here: Assigns reads the counts on every render.
func (p *OverviewPage) HandleInfo(c *loom.Ctx, msg loom.Message) {
	if msg.Topic == jobs.NodesTopic {
		p.loadNodes(c)
	}
}

// Assigns reads the queues and recent failures for the template, and
// totals the queues' counts for the cards at the top.
func (p *OverviewPage) Assigns(c *loom.Ctx) OverviewAssigns {
	r := p.app.runner()
	ctx := c.Context()
	a := OverviewAssigns{ReadOnly: p.app.readOnly(c), Flash: p.flash, Node: r.Node}
	if p.confirm != nil {
		a.Confirm = p.confirm.prompt
	}
	queues, err := r.QueueInfo(ctx)
	if err != nil {
		a.Flash = errorMessage(err)
		return a
	}
	for _, q := range queues {
		row := QueueRow{
			Name: q.Name, Available: q.States[jobs.Available], Scheduled: q.States[jobs.Scheduled],
			Retryable: q.States[jobs.Retryable], Running: q.States[jobs.Executing], Concurrency: q.Concurrency,
			Completed: q.Completed, Failed: q.Failed, ErrorRate: "–", Waiting: "–",
		}
		if q.OldestWaiting >= 1 {
			wait := time.Duration(q.OldestWaiting) * time.Second
			row.Waiting, row.Stuck = short(wait), wait > time.Minute
		}
		if total := q.Completed + q.Failed; total > 0 {
			row.ErrorRate = fmt.Sprintf("%d%%", q.Failed*100/total)
		}
		a.Running += row.Running
		a.Available += row.Available
		a.Scheduled += row.Scheduled
		a.Retryable += row.Retryable
		a.Discarded += q.States[jobs.Discarded]
		a.Done += row.Completed
		a.Queues = append(a.Queues, row)
	}
	now := r.Clock().Now()
	if p.nodesErr != nil {
		a.Flash = errorMessage(p.nodesErr)
	}
	for _, n := range p.nodes {
		row := NodeRow{Name: n.Name, State: n.State, Leader: n.Leader, Self: n.Self, Started: ago(now, n.StartedAt), Seen: ago(now, n.SeenAt)}
		for _, q := range n.Queues {
			row.Queues = append(row.Queues, NodeQueueRow{Name: q.Name, Running: q.Running, Concurrency: q.Concurrency})
		}
		a.Nodes = append(a.Nodes, row)
	}
	failures, err := r.RecentFailures(ctx, 10)
	if err != nil {
		a.Flash = errorMessage(err)
	}
	for _, rec := range failures {
		a.Failures = append(a.Failures, p.app.row(rec, now))
	}
	return a
}
