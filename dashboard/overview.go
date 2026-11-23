package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/trace"
)

// OverviewPage answers "is the app alive and well, and what is it
// doing?" at a glance. Everything it shows is in the app's memory, read
// again on every tick: the counters behind `loom stats`, the event ring
// behind `loom events`, and the open sessions.
type OverviewPage struct {
	app     *App
	hasJobs bool   // the app registered job types
	jobsURL string // the jobs dashboard, if the app mounts one
}

// chartBuckets and chartStep shape the activity chart: five minutes in
// ten-second bars.
const (
	chartBuckets = 30
	chartStep    = 10 * time.Second
	chartHeight  = 96 // the SVG's height, in its own units
	barWidth     = 8
	barGap       = 2
)

// Bar is one ten-second bar of the activity chart: page actions at the
// bottom, jobs finished stacked on top. Y and H are in the SVG's units.
type Bar struct {
	X     int
	PageY int
	PageH int
	JobY  int
	JobH  int
	Title string // e.g. "14:03:10 · 12 page actions, 3 jobs"
}

// RouteRow is a route's activity since the app started.
type RouteRow struct {
	Route      string
	Href       string // a link to it, if it has no parameters
	Loads      int
	Events     int
	LastMinute int
	P95        string
	Errors     int
}

// QueueRow is a job queue's state.
type QueueRow struct {
	Name      string
	Running   int
	Workers   int
	Available int
	Scheduled int
	Retryable int
	Discarded int
}

// Mount starts the refresh, and finds the jobs dashboard to link to.
func (p *OverviewPage) Mount(c *loom.Ctx) {
	section(c, "overview")
	c.Title("Overview")
	p.hasJobs = len(p.app.app.Manifest().Jobs.Types) > 0
	for _, l := range p.app.mountLinks() {
		if l.Label == "jobsweb" {
			p.jobsURL = l.Href
		}
	}
	p.app.tick(c)
}

// Events has the refresh's timer.
func (p *OverviewPage) Events() loom.Events {
	return loom.Events{"tick": func(c *loom.Ctx, _ loom.Payload) { p.app.tick(c) }}
}

// Assigns reads the app's numbers.
func (p *OverviewPage) Assigns(c *loom.Ctx) OverviewAssigns {
	now := time.Now()
	stats := p.app.app.Stats()
	rt := stats.Runtime
	a := OverviewAssigns{
		Uptime:     short(now.Sub(stats.Started)),
		Started:    stats.Started.Local().Format("Mon 2 Jan 15:04"),
		Goroutines: rt.Goroutines,
		Heap:       size(rt.HeapBytes),
		Memory:     size(rt.Memory.RSSBytes),
		GcRuns:     int(rt.GCRuns),
		GcPause:    millis(rt.GCPauseMS),
		HasJobs:    p.hasJobs,
		JobsURL:    p.jobsURL,
	}
	if rt.Memory.RSSBytes == 0 {
		a.Memory = a.Heap // not Linux: the heap is the best measure there is
	}
	if rt.DB != nil {
		a.HasDb = true
		a.DbInUse, a.DbMax, a.DbWaits = rt.DB.InUse, rt.DB.Max, int(rt.DB.Waits)
	}
	for _, s := range p.app.app.Sessions() {
		if !p.app.own(s.Path) {
			a.Pages++
			a.Browsers += s.Connected
		}
	}

	routes := map[string]*RouteRow{}
	lastMinute := 0 // errors
	for _, s := range stats.Events {
		switch {
		case s.Name == trace.Error:
			a.Errors += int(s.Count)
			lastMinute += int(s.LastMinute)
		case s.Name == trace.JobFinish:
			a.JobsLastMinute += int(s.LastMinute)
		case (s.Name == trace.PageMount || s.Name == trace.PageEvent) && s.By == "route" && !p.app.own(s.Key):
			a.ActionsLastMinute += int(s.LastMinute)
			r := routes[s.Key]
			if r == nil {
				r = &RouteRow{Route: s.Key, P95: "–"}
				if !strings.Contains(s.Key, "{") && s.Key != "other" {
					r.Href = s.Key
				}
				routes[s.Key] = r
			}
			if s.Name == trace.PageMount {
				r.Loads = int(s.Count)
			} else {
				r.Events = int(s.Count)
				r.P95 = millis(s.P95MS)
			}
			r.LastMinute += int(s.LastMinute)
			r.Errors += int(s.Errors)
		}
	}
	a.ErrorsHint = "none since start"
	if a.Errors > 0 {
		a.ErrorsHint = fmt.Sprintf("%d in the last minute", lastMinute)
	}
	for _, r := range routes {
		a.Routes = append(a.Routes, *r)
	}
	sort.Slice(a.Routes, func(i, j int) bool {
		x, y := a.Routes[i], a.Routes[j]
		if x.Loads+x.Events != y.Loads+y.Events {
			return x.Loads+x.Events > y.Loads+y.Events
		}
		return x.Route < y.Route
	})
	if len(a.Routes) > 8 {
		a.Routes = a.Routes[:8]
	}

	events := p.app.app.Stream().Since(0, trace.Filter{})
	a.Bars, a.Peak = p.chart(events, now)
	for i := len(events) - 1; i >= 0 && len(a.RecentErrors) < 5; i-- {
		if e := events[i]; kind(e) == "error" && !p.app.own(e.String("route")) {
			a.RecentErrors = append(a.RecentErrors, eventRow(e, now))
		}
	}

	if p.hasJobs {
		a.Queues, a.JobsError = p.queues(c)
	}
	return a
}

// chart counts the kept events into ten-second bars, page actions and
// jobs finished, for the last five minutes, scaled to the tallest bar.
// The event ring keeps the latest 2000 events, so on a busy app the
// chart's left end may be short.
func (p *OverviewPage) chart(events []trace.Event, now time.Time) ([]Bar, int) {
	end := now.Truncate(chartStep).Add(chartStep)
	start := end.Add(-chartBuckets * chartStep)
	var pages, jobsDone [chartBuckets]int
	for _, e := range events {
		if e.Time.Before(start) {
			continue
		}
		i := int(e.Time.Sub(start) / chartStep)
		if i < 0 || i >= chartBuckets {
			continue
		}
		switch e.Name {
		case trace.PageMount, trace.PageEvent:
			if !p.app.own(e.String("route")) {
				pages[i]++
			}
		case trace.JobFinish:
			jobsDone[i]++
		}
	}
	peak := 1
	for i := range pages {
		peak = max(peak, pages[i]+jobsDone[i])
	}
	bars := make([]Bar, chartBuckets)
	for i := range bars {
		ph := pages[i] * chartHeight / peak
		jh := jobsDone[i] * chartHeight / peak
		if pages[i] > 0 && ph == 0 {
			ph = 1
		}
		if jobsDone[i] > 0 && jh == 0 {
			jh = 1
		}
		at := start.Add(time.Duration(i) * chartStep).Local().Format("15:04:05")
		bars[i] = Bar{
			X: i * (barWidth + barGap), PageY: chartHeight - ph, PageH: ph, JobY: chartHeight - ph - jh, JobH: jh,
			Title: fmt.Sprintf("%s · %d page actions, %d jobs", at, pages[i], jobsDone[i]),
		}
	}
	if peak == 1 && pages == [chartBuckets]int{} && jobsDone == [chartBuckets]int{} {
		peak = 0
	}
	return bars, peak
}

// queues reads each job queue's counts.
func (p *OverviewPage) queues(c *loom.Ctx) ([]QueueRow, string) {
	info, err := p.app.app.JobRunner().QueueInfo(c.Context())
	if err != nil {
		return nil, err.Error()
	}
	out := []QueueRow{}
	for _, q := range info {
		out = append(out, QueueRow{
			Name: q.Name, Running: q.States[jobs.Executing], Workers: q.Concurrency,
			Available: q.States[jobs.Available], Scheduled: q.States[jobs.Scheduled],
			Retryable: q.States[jobs.Retryable], Discarded: q.States[jobs.Discarded],
		})
	}
	return out, ""
}
