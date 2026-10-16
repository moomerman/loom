package jobs

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/moomerman/loom/pubsub"
	"github.com/moomerman/loom/trace"
)

// The node registry: every started runner records itself in the store
// (loom_nodes with Postgres) when it starts, every third of its
// LeaseDuration while it runs, and when it gains or loses leadership,
// and removes itself when it shuts down. A node that stops reporting
// goes stale when its lease ends, the same moment the jobs it holds may
// be rescued, and the built-in prune job deletes it NodeRetention later.
// jobsweb, `loom nodes` and /_loom/health show it, so "which nodes run
// which queues, and which one leads" has an answer that isn't a guess.

// Node is a runner's entry in the node registry.
type Node struct {
	Name      string      `json:"name"`
	Queues    []NodeQueue `json:"queues"`
	Leader    bool        `json:"leader"` // it ran cron and rescue for the cluster at its last heartbeat
	StartedAt time.Time   `json:"started_at"`
	SeenAt    time.Time   `json:"seen_at"` // its latest heartbeat
	// LeaseUntil is SeenAt plus the runner's LeaseDuration: past it, the
	// node is stale, and the jobs it holds are rescued.
	LeaseUntil time.Time `json:"lease_until"`
}

// NodeQueue is a queue's workers on one node.
type NodeQueue struct {
	Name        string `json:"name"`
	Concurrency int    `json:"concurrency"`
	Running     int    `json:"running"` // jobs executing on the node at its latest heartbeat
}

// NodeInfo is a node as `loom nodes` and jobsweb show it.
type NodeInfo struct {
	Node
	// State is "up", or "stale" once its lease has ended: it crashed,
	// hung or lost the database. A stale node's Leader is false.
	State string `json:"state"`
	Self  bool   `json:"self"` // the node answering
}

// Node states.
const (
	NodeUp    = "up"
	NodeStale = "stale"
)

// NodesTopic is the PubSub topic on which a runner announces its own
// registry entry changed: each heartbeat, a leader change, and its
// shutdown. Pages showing nodes, such as jobsweb's overview, subscribe
// to it; jobs.Topic carries job changes only.
const NodesTopic = "loom:jobs:nodes"

// NodesEvent is the event of messages on NodesTopic. Its payload's
// "node" names the node that changed.
const NodesEvent = "nodes"

// DefaultNodeRetention is a Runner's NodeRetention unless set.
const DefaultNodeRetention = time.Hour

// Heartbeat records this node in the registry now, as the runner does
// every third of its LeaseDuration once started, and announces it on
// NodesTopic. Tests of a Manual runner call it themselves.
func (r *Runner) Heartbeat(ctx context.Context) error {
	n := r.Self()
	now := Round(r.clock.Now())
	if n.StartedAt.IsZero() {
		n.StartedAt = now
	}
	n.SeenAt, n.LeaseUntil = now, now.Add(r.LeaseDuration)
	if err := r.store.Heartbeat(ctx, n); err != nil {
		return err
	}
	r.mu.Lock()
	r.startedAt, r.seenAt = n.StartedAt, now
	r.mu.Unlock()
	r.nodeChanged()
	return nil
}

// beat runs Heartbeat with a bounded context, reporting a failure.
func (r *Runner) beat() {
	ctx, cancel := r.storeCtx()
	defer cancel()
	if err := r.Heartbeat(ctx); err != nil {
		r.fail(ctx, "loom jobs: recording this node in the registry", "err", err)
	}
}

// Self is this node as its next heartbeat records it, from memory: no
// store query, so /_loom/health can show it on every request. SeenAt
// is its latest successful heartbeat, zero before the first.
func (r *Runner) Self() Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := Node{Name: r.Node, Queues: []NodeQueue{}, Leader: r.leader, StartedAt: r.startedAt, SeenAt: r.seenAt}
	if !r.seenAt.IsZero() {
		n.LeaseUntil = r.seenAt.Add(r.LeaseDuration)
	}
	for _, q := range r.queues {
		n.Queues = append(n.Queues, NodeQueue{Name: q.Name, Concurrency: q.Concurrency, Running: q.running})
	}
	slices.SortFunc(n.Queues, func(a, b NodeQueue) int { return cmp.Compare(a.Name, b.Name) })
	return n
}

// Nodes lists the node registry: each node up or stale, by name.
func (r *Runner) Nodes(ctx context.Context) ([]NodeInfo, error) {
	nodes, err := r.store.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	now := r.clock.Now()
	out := make([]NodeInfo, 0, len(nodes))
	for _, n := range nodes {
		ni := NodeInfo{Node: n, State: NodeUp, Self: n.Name == r.Node}
		if ni.Queues == nil {
			ni.Queues = []NodeQueue{}
		}
		if n.LeaseUntil.Before(now) {
			ni.State, ni.Leader = NodeStale, false
		}
		out = append(out, ni)
	}
	return out, nil
}

// nodeChanged announces on NodesTopic that this node's entry changed.
// The app's PubSub carries it to every node's pages.
func (r *Runner) nodeChanged() {
	if r.PubSub != nil {
		r.PubSub.Publish(pubsub.Message{Topic: NodesTopic, Event: NodesEvent, Payload: map[string]string{"node": r.Node}})
	}
}

// leave removes this node from the registry at shutdown.
func (r *Runner) leave() {
	ctx, cancel := r.storeCtx()
	defer cancel()
	if err := r.store.RemoveNode(ctx, r.Node); err != nil {
		r.stream().Emit(ctx, trace.Error, "msg", "loom jobs: removing this node from the registry", "err", err)
		return
	}
	r.nodeChanged()
}
