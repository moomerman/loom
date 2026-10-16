// Package pubsub delivers messages on named topics, within one process.
// The live runtime uses it so pages can hear about things that happen
// elsewhere (a background job finishing, another user's change), and jobs
// use it to notify pages.
//
// Messages are an event name and a string payload, like browser events,
// so they can cross process boundaries unchanged when a networked backend
// (Postgres NOTIFY, say) implements PubSub.
package pubsub

import "sync"

// Message is published on a topic.
type Message struct {
	Topic   string            `json:"topic"`
	Event   string            `json:"event"`
	Payload map[string]string `json:"payload"`
	// Trace is the trace id of the action that published it, if known
	// (a job's Broadcast sets it), so the page that handles it carries
	// on the same trace. See docs/events.md.
	Trace string `json:"trace,omitempty"`
}

// PubSub publishes messages to subscribers of a topic.
type PubSub interface {
	// Publish sends m to every current subscriber of m.Topic. It never
	// blocks on a slow subscriber.
	Publish(m Message) error
	// Subscribe calls fn for each message published on topic until
	// unsubscribe is called. fn must not block.
	Subscribe(topic string, fn func(Message)) (unsubscribe func())
}

// Local is an in-process PubSub. The zero value is ready to use.
type Local struct {
	mu   sync.Mutex
	next int
	subs map[string]map[int]func(Message)
}

// NewLocal returns an in-process PubSub.
func NewLocal() *Local { return &Local{} }

// Publish calls each subscriber of m.Topic, outside the lock.
func (l *Local) Publish(m Message) error {
	l.mu.Lock()
	fns := make([]func(Message), 0, len(l.subs[m.Topic]))
	for _, fn := range l.subs[m.Topic] {
		fns = append(fns, fn)
	}
	l.mu.Unlock()
	for _, fn := range fns {
		fn(m)
	}
	return nil
}

// Subscribers returns how many subscribers topic has.
func (l *Local) Subscribers(topic string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.subs[topic])
}

// Subscribers returns how many subscribers p has for topic, if p can
// tell (it has a Subscribers(topic) int method, as Local does), or -1.
func Subscribers(p PubSub, topic string) int {
	if c, ok := p.(interface{ Subscribers(string) int }); ok {
		return c.Subscribers(topic)
	}
	return -1
}

// Subscribe registers fn for topic.
func (l *Local) Subscribe(topic string, fn func(Message)) func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.subs == nil {
		l.subs = map[string]map[int]func(Message){}
	}
	if l.subs[topic] == nil {
		l.subs[topic] = map[int]func(Message){}
	}
	l.next++
	id := l.next
	l.subs[topic][id] = fn
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			delete(l.subs[topic], id)
			if len(l.subs[topic]) == 0 {
				delete(l.subs, topic)
			}
		})
	}
}
