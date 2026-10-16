package jobsweb

import (
	"net/url"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
)

// pageSize is how many jobs the list shows at once.
const pageSize = 50

// JobsPage lists jobs, newest first, filtered by queue, state, type and
// a search of their args. The filters live in the URL,
// /jobs?state=discarded&queue=mail, so a filtered list can be shared and
// reloaded, the overview and sidebar can link straight to one, and the
// back button undoes a filter. The state tabs are links to the same
// route, which patch the page without a load; the selects and the search
// box send "filter", which patches the URL (c.Patch) the same way, and
// HandleParams reads it back.
//
// It pages with keyset pagination (the id of the last job shown) rather
// than offsets, so a page is one indexed query however big the table is,
// and jobs inserted while you read don't shift the page under you.
type JobsPage struct {
	app *App

	queue, state, typ, search string  // from the URL, set by HandleParams
	before                    int64   // show jobs with ids below this; 0 for the newest
	newer                     []int64 // the before of each newer page, to go back
	older                     int64   // the before of the next older page; 0 if none
}

// Mount subscribes to job changes.
func (p *JobsPage) Mount(c *loom.Ctx) {
	section(c, "jobs")
	c.Title("Jobs")
	c.Subscribe(jobs.Topic)
}

// HandleParams reads the filters from the URL: after Mount, and whenever
// the URL changes. New filters start from the newest page.
func (p *JobsPage) HandleParams(c *loom.Ctx, query url.Values) {
	p.queue, p.state, p.typ, p.search = query.Get("queue"), query.Get("state"), query.Get("type"), query.Get("search")
	p.before, p.newer = 0, nil
}

// Events changes the filters and pages.
func (p *JobsPage) Events() loom.Events {
	return loom.Events{"filter": p.filter, "older": p.toOlder, "newer": p.toNewer, "newest": p.toNewest}
}

// filter patches the URL with one changed filter; HandleParams applies
// it. Each keystroke of a search replaces the history entry rather than
// adding one.
func (p *JobsPage) filter(c *loom.Ctx, payload loom.Payload) {
	key := map[string]string{"queue": "queue", "state": "state", "type": "type", "search": "search"}[payload["field"]]
	if key == "" {
		return
	}
	q := p.query()
	setParam(q, key, payload["value"])
	if key == "search" {
		c.Patch(p.href(c, q), loom.Replace())
		return
	}
	c.Patch(p.href(c, q))
}

// query is the URL query holding the current filters, without empty ones.
func (p *JobsPage) query() url.Values {
	q := url.Values{}
	setParam(q, "queue", p.queue)
	setParam(q, "state", p.state)
	setParam(q, "type", p.typ)
	setParam(q, "search", p.search)
	return q
}

// href is the job list's URL with query q, under the mount prefix.
func (p *JobsPage) href(c *loom.Ctx, q url.Values) string {
	if len(q) == 0 {
		return c.Path("/jobs")
	}
	return c.Path("/jobs?" + q.Encode())
}

// setParam sets key to value in q, or removes it when value is empty, so
// URLs hold only the filters in use.
func setParam(q url.Values, key, value string) {
	if value == "" {
		q.Del(key)
		return
	}
	q.Set(key, value)
}

func (p *JobsPage) toOlder(c *loom.Ctx, _ loom.Payload) {
	if p.older != 0 {
		p.newer = append(p.newer, p.before)
		p.before = p.older
	}
}

func (p *JobsPage) toNewer(c *loom.Ctx, _ loom.Payload) {
	if len(p.newer) > 0 {
		p.before, p.newer = p.newer[len(p.newer)-1], p.newer[:len(p.newer)-1]
	}
}

func (p *JobsPage) toNewest(c *loom.Ctx, _ loom.Payload) { p.before, p.newer = 0, nil }

// HandleInfo has nothing to change: Assigns reads the current page of
// jobs on every render.
func (p *JobsPage) HandleInfo(c *loom.Ctx, msg loom.Message) {}

// Assigns reads the current page of jobs for the template.
func (p *JobsPage) Assigns(c *loom.Ctx) JobListAssigns {
	r := p.app.runner()
	ctx := c.Context()
	a := JobListAssigns{State: p.state, AllStates: p.stateHref(c, ""), Search: p.search, CanSearch: p.app.cfg.RedactArgs == nil, Newer: len(p.newer) > 0}
	if !a.CanSearch {
		p.search = ""
	}
	if queues, err := r.QueueInfo(ctx); err == nil {
		for _, q := range queues {
			a.Queues = append(a.Queues, Option{Value: q.Name, Label: q.Name, Selected: q.Name == p.queue})
		}
	}
	for _, s := range jobs.States {
		a.States = append(a.States, Option{Value: string(s), Label: string(s), Selected: string(s) == p.state, Href: p.stateHref(c, string(s))})
	}
	for _, t := range r.TypeNames() {
		a.Types = append(a.Types, Option{Value: t, Label: t, Selected: t == p.typ})
	}

	// Ask for one more than a page, to know whether there is an older one.
	recs, err := r.Jobs(ctx, jobs.Filter{Queue: p.queue, State: jobs.State(p.state), Type: p.typ, Search: p.search, Before: p.before, Limit: pageSize + 1})
	if err != nil {
		a.Flash = errorMessage(err)
		return a
	}
	p.older = 0
	if len(recs) > pageSize {
		recs = recs[:pageSize]
		p.older = recs[pageSize-1].ID
	}
	a.Older = p.older != 0
	now := r.Clock().Now()
	for _, rec := range recs {
		a.Jobs = append(a.Jobs, p.app.row(rec, now))
	}
	return a
}

// stateHref is the URL of a state tab: the current filters with that
// state ("" for all).
func (p *JobsPage) stateHref(c *loom.Ctx, state string) string {
	q := p.query()
	setParam(q, "state", state)
	return p.href(c, q)
}
