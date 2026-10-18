package trace

import (
	"math"
	"time"
)

// Timing accumulates how often something happened and how long it
// took: a count, a total, a maximum, a histogram for the 95th
// percentile, and a count for each of the last 60 seconds for the
// current rate. Event stats and query stats (the data package) both use
// it, so `loom stats` and `loom queries` mean the same by each column.
// It isn't safe for concurrent use; its owner locks it.
type Timing struct {
	count, timed uint64
	total, max   float64
	buckets      [timingBuckets]uint64
	secs         [60]uint64 // counts per second, by unix second mod 60
	secAt        [60]int64  // the unix second each count belongs to
}

// The histogram's buckets grow by a quarter from 0.1 ms, so the p95 it
// gives is within 25% of the true one, up to about a minute.
const (
	timingBuckets = 64
	timingBase    = 0.1
	timingGrowth  = 1.25
)

// Add counts one occurrence at at, timed as ms if timed is true.
func (t *Timing) Add(at time.Time, ms float64, timed bool) {
	t.count++
	s := at.Unix()
	i := int(s % 60)
	if i < 0 {
		i += 60
	}
	if t.secAt[i] != s {
		t.secAt[i], t.secs[i] = s, 0
	}
	t.secs[i]++
	if !timed {
		return
	}
	t.timed++
	t.total += ms
	t.max = max(t.max, ms)
	t.buckets[bucketOf(ms)]++
}

func bucketOf(ms float64) int {
	if ms <= timingBase {
		return 0
	}
	b := int(math.Ceil(math.Log(ms/timingBase) / math.Log(timingGrowth)))
	return min(b, timingBuckets-1)
}

// TimingSummary is a Timing as the dev endpoints show it. Milliseconds
// are to a tenth.
type TimingSummary struct {
	Count      uint64  `json:"count"`
	LastMinute uint64  `json:"last_minute"`        // of Count, in the last 60 seconds: the rate
	Timed      uint64  `json:"timed,omitempty"`    // how many were timed
	TotalMS    float64 `json:"total_ms,omitempty"` // over Timed
	MeanMS     float64 `json:"mean_ms,omitempty"`  // over Timed
	P95MS      float64 `json:"p95_ms,omitempty"`   // within 25%, never above MaxMS
	MaxMS      float64 `json:"max_ms,omitempty"`
}

// Summary returns the totals and, as of now, the rate.
func (t *Timing) Summary(now time.Time) TimingSummary {
	s := TimingSummary{Count: t.count, Timed: t.timed, MaxMS: tenth(t.max), TotalMS: tenth(t.total)}
	for i, at := range t.secAt {
		if d := now.Unix() - at; d >= 0 && d < 60 {
			s.LastMinute += t.secs[i]
		}
	}
	if t.timed == 0 {
		return s
	}
	s.MeanMS = tenth(t.total / float64(t.timed))
	want := uint64(math.Ceil(float64(t.timed) * 0.95))
	var seen uint64
	for b, n := range t.buckets {
		if seen += n; seen >= want {
			s.P95MS = tenth(min(timingBase*math.Pow(timingGrowth, float64(b)), t.max))
			break
		}
	}
	return s
}

func tenth(f float64) float64 { return math.Round(f*10) / 10 }
