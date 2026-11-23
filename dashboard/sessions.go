package dashboard

import (
	"fmt"
	"sort"
	"time"

	"github.com/moomerman/loom"
)

// SessionsPage lists the live pages open in browsers now, as `loom
// sessions` does, each a link back to the URL it shows. A page that has
// restarted is failing in a handler; one that is busy for long is stuck
// in one.
type SessionsPage struct {
	app *App
}

// SessionRow is one open page.
type SessionRow struct {
	SID       string
	Path      string // the route
	URL       string // the page's URL now
	Page      string // its Go type
	State     string // connected, idle or busy
	Connected int
	Idle      string
	Started   string
	Restarts  int
	Version   int
	Own       bool // one of this dashboard's
	start     time.Time
}

// Mount starts the refresh.
func (p *SessionsPage) Mount(c *loom.Ctx) {
	section(c, "sessions")
	c.Title("Sessions")
	p.app.tick(c)
}

// Events has the refresh's timer.
func (p *SessionsPage) Events() loom.Events {
	return loom.Events{"tick": func(c *loom.Ctx, _ loom.Payload) { p.app.tick(c) }}
}

// Assigns reads the open sessions: the app's with a browser connected
// first, then its idle ones, newest first, and the dashboard's own last.
func (p *SessionsPage) Assigns(c *loom.Ctx) SessionsAssigns {
	now := time.Now()
	a := SessionsAssigns{}
	var own []SessionRow
	for _, s := range p.app.app.Sessions() {
		row := SessionRow{
			SID: s.SID, Path: s.Path, URL: s.URL, Page: s.Page, Connected: s.Connected, Restarts: s.Restarts, Version: s.Version,
			Started: ago(now, s.StartedAt), Idle: "–", State: "connected", Own: p.app.own(s.Path), start: s.StartedAt,
		}
		if row.URL == "" {
			row.URL = s.Path
		}
		switch {
		case s.BusySeconds > 1:
			row.State = fmt.Sprintf("busy %.0fs", s.BusySeconds)
		case s.Connected == 0:
			row.State = "idle"
			row.Idle = short(time.Duration(s.IdleSeconds * float64(time.Second)))
		}
		if row.Own {
			own = append(own, row)
			continue
		}
		a.Browsers += s.Connected
		a.Sessions = append(a.Sessions, row)
	}
	sort.SliceStable(a.Sessions, func(i, j int) bool {
		x, y := a.Sessions[i].Connected > 0, a.Sessions[j].Connected > 0
		if x != y {
			return x
		}
		return a.Sessions[i].start.After(a.Sessions[j].start)
	})
	a.Sessions = append(a.Sessions, own...)
	a.Summary = fmt.Sprintf("%d pages open, %d browsers connected, and %d of this dashboard's", len(a.Sessions)-len(own), a.Browsers, len(own))
	return a
}
