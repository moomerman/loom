package loomtest

import (
	"fmt"
	"testing"
	"time"

	"github.com/moomerman/loom"
)

// Crowd is many pages open on one route, as many browsers would hold
// them: the load a watched table or a broadcast puts on the app grows
// with it, and one browser in dev never shows that. A change re-renders
// every page, so a query in Assigns runs once per page; Crowd measures
// that, so a test can hold a route to a budget before production finds
// the cost.
//
//	c := loomtest.MountCrowd(t, app, "/", 50)
//	if load := c.TableChanged("tune_files"); load.Queries > 1 {
//		t.Errorf("a tune_files change ran %d queries for %d pages; share the read (one per change, not per page)", load.Queries, load.Pages)
//	}
type Crowd struct {
	t     testing.TB
	Pages []*Page
}

// Load is what one change cost a crowd: the statements every page ran
// re-rendering for it, their time, and the time in all.
type Load struct {
	Pages   int
	Queries int64
	QueryMS float64
	MS      float64
}

// PerPage is the queries each page ran on average.
func (l Load) PerPage() float64 { return float64(l.Queries) / float64(max(l.Pages, 1)) }

func (l Load) String() string {
	return fmt.Sprintf("%d pages: %d queries (%.1f each) taking %.1f ms, %.1f ms in all", l.Pages, l.Queries, l.PerPage(), l.QueryMS, l.MS)
}

// MountCrowd mounts n pages at path, each as Mount does, with opts.
func MountCrowd(t testing.TB, app *loom.App, path string, n int, opts ...Option) *Crowd {
	t.Helper()
	c := &Crowd{t: t}
	for range n {
		c.Pages = append(c.Pages, Mount(t, app, path, opts...))
	}
	return c
}

// TableChanged re-renders every page as the runtime does when a table
// the route watches changes (Page.TableChanged), and returns what that
// cost.
func (c *Crowd) TableChanged(table string) Load {
	c.t.Helper()
	return c.each(func(p *Page) { p.TableChanged(table) })
}

// Info delivers a message to every page, as a broadcast on a topic they
// subscribe to would (Page.Info), and returns what that cost.
func (c *Crowd) Info(msg loom.Message) Load {
	c.t.Helper()
	return c.each(func(p *Page) { p.Info(msg) })
}

func (c *Crowd) each(f func(p *Page)) Load {
	c.t.Helper()
	start := time.Now()
	l := Load{Pages: len(c.Pages)}
	var d time.Duration
	for _, p := range c.Pages {
		f(p)
		l.Queries += p.queries
		d += p.queryTime
	}
	l.QueryMS = float64(d.Microseconds()) / 1000
	l.MS = float64(time.Since(start).Microseconds()) / 1000
	return l
}
