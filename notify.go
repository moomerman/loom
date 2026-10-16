package loom

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moomerman/loom/pubsub"
	"github.com/moomerman/loom/trace"
)

// The app's PubSub across nodes (docs/deploy.md). Unless Config.PubSub
// is set, a message is delivered at once to this node's subscribers
// and, when the app has a Postgres database, sent to every other node
// with NOTIFY on channel loom_pubsub, which each node LISTENs on while a
// page subscribes. So app.Broadcast in AfterCommit, and c.Broadcast from
// a job on any node, reach every page subscribed to the topic, on every
// node, with nothing to configure. Without a database it is in-process.

// pubsubChannel is the NOTIFY channel messages cross nodes on.
const pubsubChannel = "loom_pubsub"

// maxNotify is the largest NOTIFY payload Postgres accepts, less one.
const maxNotify = 7999

// nodePubSub is the default PubSub: local delivery plus NOTIFY.
type nodePubSub struct {
	app   *App
	local pubsub.Local
	node  string // this node's id, so it skips its own notifications

	mu     sync.Mutex
	subs   int                // subscriptions on this node
	stop   context.CancelFunc // ends the LISTEN loop; nil when not listening
	done   chan struct{}
	lost   atomic.Bool // the LISTEN connection dropped and isn't back yet
	listen atomic.Bool // listening
}

// notification is a message as it crosses nodes.
type notification struct {
	Node    string         `json:"n"`
	Message pubsub.Message `json:"m"`
}

func newNodePubSub(a *App) *nodePubSub {
	b := make([]byte, 8)
	rand.Read(b)
	return &nodePubSub{app: a, node: hex.EncodeToString(b)}
}

// Publish delivers m to this node's subscribers, then, with a database,
// to the other nodes'. A message over NOTIFY's 8000 bytes is RT447: it
// reaches only this node.
func (p *nodePubSub) Publish(m pubsub.Message) error {
	p.local.Publish(m)
	db := p.app.db
	if db == nil || db.SQL() == nil {
		return nil
	}
	payload, err := json.Marshal(notification{Node: p.node, Message: m})
	if err != nil {
		return err
	}
	if len(payload) > maxNotify {
		return &Error{Code: RT447, Msg: fmt.Sprintf("the broadcast %q on topic %s is %d bytes, more than Postgres NOTIFY carries (8000), so it reached only this node's pages", m.Event, m.Topic, len(payload)),
			Fix: "broadcast what changed (an id) and have pages load the rest in HandleInfo"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = db.Exec(ctx, `SELECT pg_notify($1, $2)`, pubsubChannel, string(payload))
	return err
}

// Subscribe registers fn for topic on this node, starting the LISTEN
// connection for the first subscription (it returns once listening, so a
// message published after it is not missed) and stopping it after the
// last.
func (p *nodePubSub) Subscribe(topic string, fn func(pubsub.Message)) func() {
	unsub := p.local.Subscribe(topic, fn)
	p.mu.Lock()
	p.subs++
	var ready chan struct{}
	if db := p.app.db; p.stop == nil && db != nil && db.SQL() != nil {
		ctx, cancel := context.WithCancel(context.Background())
		p.stop, p.done, ready = cancel, make(chan struct{}), make(chan struct{})
		go p.run(ctx, ready, p.done)
	}
	p.mu.Unlock()
	if ready != nil {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			unsub()
			p.mu.Lock()
			p.subs--
			var stop context.CancelFunc
			var done chan struct{}
			if p.subs == 0 && p.stop != nil {
				stop, done = p.stop, p.done
				p.stop = nil
			}
			p.mu.Unlock()
			if stop != nil {
				stop()
				<-done
			}
		})
	}
}

// Subscribers returns how many subscribers topic has on this node.
func (p *nodePubSub) Subscribers(topic string) int { return p.local.Subscribers(topic) }

// run holds the LISTEN connection, reconnecting after errors. Messages
// published while it was down are lost; pages catch up on their next
// event or re-render, so the warning says so.
func (p *nodePubSub) run(ctx context.Context, ready, done chan struct{}) {
	defer close(done)
	defer p.lost.Store(false)
	defer p.listen.Store(false)
	var once sync.Once
	for ctx.Err() == nil {
		err := listenOnce(ctx, p.app.db.SQL(), pubsubChannel, func() {
			p.lost.Store(false)
			p.listen.Store(true)
			once.Do(func() { close(ready) })
		}, func(payload string) {
			var n notification
			if json.Unmarshal([]byte(payload), &n) != nil || n.Node == p.node {
				return
			}
			p.local.Publish(n.Message)
		})
		if ctx.Err() != nil {
			return
		}
		p.lost.Store(true)
		p.app.stream.Emit(ctx, trace.PubSubListenLost, "msg", "loom: PubSub LISTEN connection lost, so broadcasts from other nodes are missed until it is back; reconnecting", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// state is the LISTEN connection's state, for /_loom/health: "idle" (no
// subscriber yet: it opens with the first), "ok" or "reconnecting".
func (p *nodePubSub) state() string {
	switch {
	case p.lost.Load():
		return "reconnecting"
	case p.listen.Load():
		return "ok"
	}
	return "idle"
}
