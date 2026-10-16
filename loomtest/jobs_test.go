package loomtest_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/loomtest"
)

type sendWelcome struct {
	UserID string `json:"user_id"`
	Fail   bool   `json:"fail"`
}

func (j sendWelcome) Options() jobs.Options { return jobs.Options{Queue: "mail", MaxAttempts: 2} }

func (j sendWelcome) Perform(ctx context.Context, c *jobs.Ctx) error {
	if j.Fail {
		return errors.New("smtp down")
	}
	return c.Broadcast("users:"+j.UserID, "welcomed", map[string]string{"user": j.UserID})
}

type nightly struct{}

func (nightly) Perform(ctx context.Context, c *jobs.Ctx) error { return nil }

func signUp(t *testing.T, db *loom.DB, id string) {
	err := db.Tx(context.Background(), func(tx loom.Tx) error {
		_, err := jobs.Enqueue(tx, sendWelcome{UserID: id}, jobs.In(10*time.Minute))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestJobQueue(t *testing.T) {
	q := loomtest.Jobs(t)
	q.Jobs(sendWelcome{}, nightly{})
	signUp(t, q.DB(), "u1")
	q.AssertEnqueued(t, sendWelcome{UserID: "u1"})
	q.AssertEnqueued(t, sendWelcome{})
	q.RefuteEnqueued(t, sendWelcome{UserID: "u2"})

	if n := q.Drain(t); n != 0 {
		t.Fatalf("ran %d jobs before they were due", n)
	}
	q.Advance(10 * time.Minute)
	if n := q.Drain(t); n != 1 {
		t.Fatalf("ran %d jobs, want 1", n)
	}
	q.AssertCompleted(t, sendWelcome{UserID: "u1"})
	q.AssertBroadcast(t, "users:u1", "welcomed")

	// Failures are retried after Advance, then discarded.
	q.Enqueue(sendWelcome{UserID: "u3", Fail: true})
	q.Drain(t)
	q.AssertState(t, sendWelcome{UserID: "u3"}, jobs.Retryable)
	q.Advance(time.Minute)
	q.Drain(t)
	q.AssertState(t, sendWelcome{UserID: "u3"}, jobs.Discarded)

	// Cron ticks are enqueued as the clock passes them.
	q.Cron("0 3 * * *", nightly{})
	q.Advance(3 * 24 * time.Hour)
	if got := len(q.Matching(nightly{})); got != 3 {
		t.Fatalf("%d nightly jobs after three days, want 3", got)
	}
	q.Drain(t)
	q.AssertCompleted(t, nightly{})
	if !q.Clock().Now().Equal(loomtest.JobsEpoch.Add(10*time.Minute + time.Minute + 72*time.Hour)) {
		t.Fatalf("clock at %v", q.Clock().Now())
	}
}

func TestJobQueueFailureMessages(t *testing.T) {
	q := loomtest.Jobs(t)
	q.Jobs(sendWelcome{})
	q.Enqueue(sendWelcome{UserID: "u1"})
	ft := &fakeT{TB: t}
	q.AssertCompleted(ft, sendWelcome{UserID: "u1"})
	if len(ft.errors) != 1 {
		t.Fatalf("errors: %q", ft.errors)
	}
	want := "no completed job matches loomtest_test.sendWelcome {\"user_id\":\"u1\",\"fail\":false}\njobs of that type:\n  job 1 {\"user_id\":\"u1\",\"fail\":false} available, attempt 0\n"
	if ft.errors[0] != want {
		t.Fatalf("message:\n%s\nwant:\n%s", ft.errors[0], want)
	}
}

type fakeT struct {
	testing.TB
	errors []string
}

func (f *fakeT) Helper() {}
func (f *fakeT) Errorf(format string, args ...any) {
	f.errors = append(f.errors, sprintf(format, args...))
}

func TestPerformNow(t *testing.T) {
	if err := jobs.PerformNow(t, sendWelcome{UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := jobs.PerformNow(t, sendWelcome{Fail: true}); err == nil || err.Error() != "smtp down" {
		t.Fatalf("got %v", err)
	}
}

// welcomePage shows whether the user was welcomed.
type welcomePage struct{ status string }

func (p *welcomePage) Mount(c *loom.Ctx) {
	c.Subscribe("users:u1")
	p.status = "waiting"
}

func (p *welcomePage) Assigns(c *loom.Ctx) hxtest.BadgeAssigns {
	return hxtest.BadgeAssigns{Label: p.status}
}

func (p *welcomePage) HandleInfo(c *loom.Ctx, msg loom.Message) {
	p.status = "welcomed " + msg.Payload["user"]
}

func TestPageListensToJobs(t *testing.T) {
	q := loomtest.Jobs(t)
	q.Jobs(sendWelcome{})
	app := loom.New()
	app.Live("/", &welcomePage{}, hxtest.Badge)
	p := loomtest.Mount(t, app, "/")
	p.Listen(q.PubSub())
	q.Enqueue(sendWelcome{UserID: "u1"})
	q.Drain(t)
	if got := p.HTML(); !contains(got, "welcomed u1") {
		t.Fatalf("html: %s", got)
	}
	p.Info(loom.Message{Topic: "users:u1", Event: "welcomed", Payload: map[string]string{"user": "again"}})
	if got := p.HTML(); !contains(got, "welcomed again") {
		t.Fatalf("html: %s", got)
	}
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
func contains(s, sub string) bool               { return strings.Contains(s, sub) }
