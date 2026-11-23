package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/moomerman/loom/trace"
)

// EventRow is one event, as the activity feed and the overview's recent
// errors show it.
type EventRow struct {
	Seq     string
	Time    string // e.g. 14:03:07
	Ago     string // e.g. 12s ago
	Name    string // e.g. page.event
	Kind    string // page, job, data, error or other: its dot's color
	Subject string // what it was about: a route, a job type, a table
	Detail  string // the rest: the event's name, its outcome
	MS      string // e.g. 3.2 ms; "" if untimed
	Trace   string
	Error   bool
}

// kind groups event names for the feed's tabs and colors.
func kind(e trace.Event) string {
	switch {
	case e.Level.String() == "ERROR" || e.Name == trace.Error:
		return "error"
	case strings.HasPrefix(e.Name, "page."), strings.HasPrefix(e.Name, "stream."), strings.HasPrefix(e.Name, "event."), strings.HasPrefix(e.Name, "user."):
		return "page"
	case strings.HasPrefix(e.Name, "job."), strings.HasPrefix(e.Name, "jobs."), e.Name == trace.CronTick:
		return "job"
	case strings.HasPrefix(e.Name, "tx."), strings.HasPrefix(e.Name, "db."), strings.HasPrefix(e.Name, "watch."):
		return "data"
	}
	return "other"
}

// eventRow says what an event was about in a line: the attributes that
// matter for its name (docs/events.md), not all of them.
func eventRow(e trace.Event, now time.Time) EventRow {
	r := EventRow{
		Seq: fmt.Sprint(e.Seq), Time: e.Time.Local().Format("15:04:05"), Ago: ago(now, e.Time),
		Name: e.Name, Kind: kind(e), Trace: e.Trace, Error: e.Level.String() == "ERROR",
	}
	if ms, ok := e.Attrs["ms"].(float64); ok {
		r.MS = fmt.Sprintf("%.1f ms", ms)
	}
	join := func(parts ...string) string {
		out := []string{}
		for _, p := range parts {
			if p != "" {
				out = append(out, p)
			}
		}
		return strings.Join(out, " · ")
	}
	switch {
	case e.Name == trace.Error:
		r.Subject = join(e.String("code"), e.String("route"))
		r.Detail = e.String("msg")
	case strings.HasPrefix(e.Name, "page."):
		r.Subject = e.String("route")
		r.Detail = join(e.String("event"), e.String("topic"), e.String("url"), e.String("reason"), outcome(e))
	case strings.HasPrefix(e.Name, "job."), e.Name == trace.CronTick:
		r.Subject = e.String("type")
		r.Detail = join(e.String("queue"), e.String("cron"), e.String("state"), e.String("code"))
	case e.Name == trace.TxCommit, e.Name == trace.TxRollback:
		r.Subject = e.String("statements") + " statements"
		r.Detail = e.String("reason")
	case e.Name == trace.DBSlow:
		r.Subject = "slow query"
		r.Detail = e.String("query")
	case e.Name == trace.WatchNotify:
		r.Subject = e.String("table")
		r.Detail = e.String("sessions") + " sessions"
	case e.Name == trace.PubSubPublish:
		r.Subject = e.String("topic")
		r.Detail = join(e.String("event"), e.String("subscribers")+" subscribers")
	default:
		r.Subject = join(e.String("route"), e.String("user"), e.String("to"), e.String("addr"))
		r.Detail = join(e.String("event"), e.String("subject"), e.String("reason"), e.String("msg"))
	}
	return r
}

// outcome is a page event's outcome when it isn't the usual "ok".
func outcome(e trace.Event) string {
	if o := e.String("outcome"); o != "ok" {
		return o
	}
	return ""
}

// mountName shortens a sub-app's Go type for a label: *jobsweb.App is
// "jobsweb".
func mountName(typ string) string {
	typ = strings.TrimPrefix(typ, "*")
	if pkg, name, ok := strings.Cut(typ, "."); ok {
		if name == "App" {
			return pkg
		}
		return pkg + "." + name
	}
	return typ
}

// ago formats t relative to now: "12s ago".
func ago(now, t time.Time) string {
	d := now.Sub(t)
	if d < time.Second {
		return "just now"
	}
	return short(d) + " ago"
}

// short formats a duration to its largest unit: 12s, 5m, 3h, 2d.
func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours()/24), int(d.Hours())%24)
}

// size formats a number of bytes: 12.3 MB.
func size(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// millis formats a timing: "–" when there is none.
func millis(ms float64) string {
	if ms == 0 {
		return "–"
	}
	if ms >= 1000 {
		return fmt.Sprintf("%.2f s", ms/1000)
	}
	return fmt.Sprintf("%.1f ms", ms)
}
