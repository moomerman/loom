package jobs

import (
	"strconv"
	"strings"
	"time"

	"github.com/moomerman/loom/pubsub"
)

// Topic is the PubSub topic on which a runner announces that jobs
// changed: enqueued, started, finished, retried, cancelled or deleted.
// Dashboards such as jobsweb subscribe to it instead of polling. Each
// message is a ChangedEvent; announcements are coalesced to at most one
// per ChangeInterval, so a busy queue doesn't flood its subscribers.
const Topic = "loom:jobs"

// ChangedEvent is the event of messages on Topic. Its payload's "ids"
// lists the ids of the jobs that changed, comma-separated, or is empty
// when they aren't known (a job enqueued on another node) or are too
// many to list: then any job may have changed.
const ChangedEvent = "changed"

// maxChangedIDs is the most ids one ChangedEvent lists.
const maxChangedIDs = 100

// changed records that jobs changed (id 0: unknown jobs) and announces
// it on Topic, at once when ChangeInterval is zero, otherwise at the end
// of the current interval.
func (r *Runner) changed(ids ...int64) {
	if r.PubSub == nil {
		return
	}
	r.changeMu.Lock()
	if r.changedIDs == nil {
		r.changedIDs = map[int64]bool{}
	}
	for _, id := range ids {
		r.changedIDs[id] = true
	}
	if len(ids) == 0 {
		r.changedIDs[0] = true
	}
	if r.ChangeInterval <= 0 {
		r.changeMu.Unlock()
		r.announce()
		return
	}
	if !r.changeTimer {
		r.changeTimer = true
		time.AfterFunc(r.ChangeInterval, r.announce)
	}
	r.changeMu.Unlock()
}

// announce publishes the changes recorded since the last announcement.
func (r *Runner) announce() {
	r.changeMu.Lock()
	set := r.changedIDs
	r.changedIDs, r.changeTimer = nil, false
	r.changeMu.Unlock()
	if len(set) == 0 {
		return
	}
	// Counts are stale now. Subscribers render after this message, and
	// the first to ask counts afresh for all of them.
	r.statsMu.Lock()
	r.stats = nil
	r.statsMu.Unlock()
	var ids []string
	if !set[0] && len(set) <= maxChangedIDs {
		for id := range set {
			ids = append(ids, strconv.FormatInt(id, 10))
		}
	}
	r.PubSub.Publish(pubsub.Message{Topic: Topic, Event: ChangedEvent, Payload: map[string]string{"ids": strings.Join(ids, ",")}})
}

// Changed reports whether a ChangedEvent's payload may include job id.
func Changed(payload map[string]string, id int64) bool {
	ids := payload["ids"]
	if ids == "" {
		return true
	}
	want := strconv.FormatInt(id, 10)
	for _, s := range strings.Split(ids, ",") {
		if s == want {
			return true
		}
	}
	return false
}

// Watch makes the runner announce jobs enqueued through the store, by
// any node, on Topic. Start calls it; tests that run jobs with RunDue
// instead of Start call it to hear of enqueues. It returns a func that
// stops watching.
func (r *Runner) Watch() (stop func()) {
	return r.store.Subscribe(func(string) { r.changed() })
}
