package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"time"

	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/pubsub"
	"github.com/moomerman/loom/trace"
)

// Ctx is a running job's context.
type Ctx struct {
	id          int64
	attempt     int
	maxAttempts int
	clock       Clock
	store       Store
	ps          pubsub.PubSub
	trace       string        // the action that enqueued the job
	events      *trace.Stream // the runner's
}

// ID is the job's id, stable across attempts: use it (with Attempt, if
// each attempt must differ) as an idempotency key.
func (c *Ctx) ID() int64 { return c.id }

// Attempt is the attempt number, starting at 1. Snoozes don't count.
func (c *Ctx) Attempt() int { return c.attempt }

// MaxAttempts is how many attempts the job gets before it is discarded.
func (c *Ctx) MaxAttempts() int { return c.maxAttempts }

// Now is the current time on the runner's clock. Use it instead of
// time.Now so tests with a fake clock control it.
func (c *Ctx) Now() time.Time { return c.clock.Now() }

// Store is the store the job came from.
func (c *Ctx) Store() Store { return c.store }

// DB is the store's database, for enqueueing follow-up jobs:
//
//	c.DB().Tx(ctx, func(tx loom.Tx) error {
//		_, err := jobs.Enqueue(tx, NextStep{ID: j.ID})
//		return err
//	})
func (c *Ctx) DB() *data.DB { return c.store.DB() }

// Broadcast publishes an event to the live pages subscribed to topic (see
// loom.Ctx.Subscribe), which receive it in HandleInfo:
//
//	c.Broadcast("exports:"+j.UserID, "export_ready", map[string]string{"url": url})
func (c *Ctx) Broadcast(topic, event string, payload map[string]string) error {
	if c.ps == nil {
		return nil
	}
	c.events.Emit(context.Background(), trace.PubSubPublish, "topic", topic, "event", event, "subscribers", pubsub.Subscribers(c.ps, topic), "trace", c.trace)
	return c.ps.Publish(pubsub.Message{Topic: topic, Event: event, Payload: payload, Trace: c.trace})
}

// decodeJob decodes a job's args into a copy of its type's prototype, so
// fields tagged `json:"-"` keep the prototype's values. strict rejects
// unknown fields, for args typed by people.
func decodeJob(proto Job, args []byte, strict bool) (Job, error) {
	v := reflect.New(reflect.TypeOf(proto))
	v.Elem().Set(reflect.ValueOf(proto))
	dec := json.NewDecoder(bytes.NewReader(args))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(v.Interface()); err != nil {
		return nil, err
	}
	return v.Elem().Interface().(Job), nil
}

// panicked is a recovered panic in Perform.
type panicked struct {
	value any
	stack string
}

func (p *panicked) Error() string { return fmt.Sprintf("Perform panicked: %v", p.value) }

// callPerform runs Perform, turning a panic into a *panicked error.
func callPerform(ctx context.Context, job Job, c *Ctx) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = &panicked{value: p, stack: string(debug.Stack())}
		}
	}()
	return job.Perform(ctx, c)
}

// TB is the part of testing.TB that PerformNow uses.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
}

// PerformNow runs one job's Perform directly, for unit tests, and returns
// its error. Like the runner, it encodes the job's args as JSON and
// decodes them into a copy of job first, so a job that wouldn't survive
// the round trip fails here too. A panic fails the test.
//
//	err := jobs.PerformNow(t, SendWelcome{UserID: "u1"})
func PerformNow(t TB, job Job) error {
	t.Helper()
	if err := checkType(job); err != nil {
		t.Fatalf("%v", err)
	}
	args, err := encodeArgs(job)
	if err != nil {
		t.Fatalf("%v", err)
	}
	decoded, err := decodeJob(job, args, false)
	if err != nil {
		t.Fatalf("decoding the args of %s: %v", TypeName(job), err)
	}
	opts := OptionsOf(job)
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	c := &Ctx{id: 1, attempt: 1, maxAttempts: opts.MaxAttempts, clock: RealClock{}, store: NewMemoryStore(MemoryOptions{}), ps: pubsub.NewLocal()}
	err = callPerform(ctx, decoded, c)
	var p *panicked
	if errors.As(err, &p) {
		t.Fatalf("%s: %v\n%s", TypeName(job), p, p.stack)
	}
	return err
}
