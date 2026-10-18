package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/trace"
)

// The event stream (docs/events.md): `loom events` lists and follows it,
// `loom trace ID` shows one action's events, `loom stats` counts them.

// eventQuery is the query string of /_loom/dev/events.
func eventQuery(since uint64, name, sid, traceID, level string) string {
	q := url.Values{}
	if since > 0 {
		q.Set("since", fmt.Sprint(since))
	}
	for k, v := range map[string]string{"name": name, "sid": sid, "trace": traceID, "level": level} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

func runEvents(args []string, stdout, stderr io.Writer) int {
	var since uint64
	var name, sid, traceID, level string
	var follow bool
	setup := func(fs *flag.FlagSet) {
		fs.Uint64Var(&since, "since", 0, "only events after this sequence number: the next of an earlier --json call")
		fs.StringVar(&name, "name", "", `only events of this name, or group ("job.*")`)
		fs.StringVar(&sid, "sid", "", "only events of this session")
		fs.StringVar(&traceID, "trace", "", "only events of this trace")
		fs.StringVar(&level, "level", "", "only events at this level or above: debug, info, warn or error")
		fs.BoolVar(&follow, "follow", false, "keep printing events as they happen (--json: one per line)")
	}
	usageLine := "events [--json] [--url URL] [--since N] [--name NAME] [--sid SID] [--trace ID] [--level L] [--follow]"
	if hasFlag(args, "follow") {
		return followEvents(usageLine, args, stdout, stderr, setup)
	}
	return introspectFlags("events", usageLine, args, stdout, stderr, 0, setup, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/events"+eventQuery(since, name, sid, traceID, level), nil)
		return data, func(w io.Writer) error {
			var r loom.EventsResult
			if err := json.Unmarshal(data, &r); err != nil {
				return err
			}
			if len(r.Events) == 0 {
				fmt.Fprintln(w, "no events")
			}
			for _, e := range r.Events {
				printEvent(w, e, time.Time{})
			}
			return nil
		}, err
	})
}

// hasFlag reports whether args set the bool flag name.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "-"+name || a == "--"+name || strings.HasPrefix(a, "--"+name+"=") || strings.HasPrefix(a, "-"+name+"=") {
			return !strings.HasSuffix(a, "=false")
		}
	}
	return false
}

// followEvents prints the stream's events as they happen, until the app
// stops or the command is interrupted.
func followEvents(usageLine string, args []string, stdout, stderr io.Writer, setup func(*flag.FlagSet)) int {
	fs := newCommand("events", usageLine, "print each event's JSON on a line of its own", stderr)
	asJSON := fs.JSON
	base := fs.String("url", appURL(), "the app's URL (default $LOOM_URL, or http://localhost:$PORT, or "+defaultURL+")")
	token := fs.String("token", os.Getenv("LOOM_INSPECT_TOKEN"), "a deployed app's inspect token (default $LOOM_INSPECT_TOKEN)")
	setup(fs.FlagSet)
	if !fs.parse(args, 0, 0) {
		return 2
	}
	get := func(name string) string { return fs.Lookup(name).Value.String() }
	var since uint64
	fmt.Sscan(get("since"), &since)
	q := eventQuery(since, get("name"), get("sid"), get("trace"), get("level"))
	if q == "" {
		q = "?follow=1"
	} else {
		q += "&follow=1"
	}
	req, err := http.NewRequest("GET", strings.TrimRight(*base, "/")+"/_loom/dev/events"+q, nil)
	if err != nil {
		fmt.Fprintf(stderr, "loom events: %v\n", err)
		return 2
	}
	(&devClient{token: *token}).authorize(req)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "loom events: nothing answered at %s (%v); start the app with `loom dev`, or pass --url\n", *base, err)
		return 2
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "loom events: %s: the app isn't in dev mode; run it with `loom dev`, or set App.Dev\n", res.Status)
		return 2
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		if *asJSON {
			fmt.Fprintln(stdout, data)
			continue
		}
		var e trace.Event
		if json.Unmarshal([]byte(data), &e) == nil {
			printEvent(stdout, e, time.Time{})
		}
	}
	return 0
}

// printEvent prints an event on one line for people: its time (or its
// offset from start, if set), level, name and attributes.
func printEvent(w io.Writer, e trace.Event, start time.Time) {
	when := e.Time.Local().Format("15:04:05.000")
	if !start.IsZero() {
		when = fmt.Sprintf("%+8.1fms", float64(e.Time.Sub(start).Microseconds())/1000)
	}
	keys := make([]string, 0, len(e.Attrs))
	for k := range e.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := fmt.Sprint(e.Attrs[k])
		if strings.ContainsAny(v, " \t\"=") || v == "" {
			v = fmt.Sprintf("%q", v)
		}
		fmt.Fprintf(&b, " %s=%s", k, v)
	}
	level := ""
	if e.Level.String() != "INFO" && e.Level.String() != "DEBUG" {
		level = " " + e.Level.String()
	}
	tr := ""
	if e.Trace != "" && start.IsZero() {
		tr = " trace=" + e.Trace
	}
	fmt.Fprintf(w, "%s%s %s%s%s\n", when, level, e.Name, b.String(), tr)
}

func runTrace(args []string, stdout, stderr io.Writer) int {
	return introspect("trace", "trace [--json] [--url URL] ID", args, stdout, stderr, 1, func(c *devClient, args []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/events"+eventQuery(0, "", "", args[0], ""), nil)
		return data, func(w io.Writer) error {
			var r loom.EventsResult
			if err := json.Unmarshal(data, &r); err != nil {
				return err
			}
			if len(r.Events) == 0 {
				fmt.Fprintf(w, "no events with trace %s: the app keeps the last %d events\n", args[0], trace.RingSize)
				return nil
			}
			fmt.Fprintf(w, "trace %s, from %s:\n", args[0], r.Events[0].Time.Local().Format("15:04:05.000"))
			for _, e := range r.Events {
				printEvent(w, e, r.Events[0].Time)
			}
			return nil
		}, err
	})
}

func runStats(args []string, stdout, stderr io.Writer) int {
	return introspect("stats", "stats [--json] [--url URL]", args, stdout, stderr, 0, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/stats", nil)
		return data, func(w io.Writer) error {
			var r loom.StatsResult
			if err := json.Unmarshal(data, &r); err != nil {
				return err
			}
			fmt.Fprintf(w, "since %s (%s), %d live pages open\n\n", r.Started.Local().Format("15:04:05"), time.Since(r.Started).Round(time.Second), r.Sessions)
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "EVENT\tKEY\tCOUNT\tLAST MIN\tERRORS\tMEAN MS\tP95 MS\tMAX MS\tQUERIES EACH\t")
			for _, s := range r.Events {
				mean, p95, most := timings(s.TimingSummary)
				each := "-"
				if s.Queries > 0 {
					each = fmt.Sprintf("%.1f", float64(s.Queries)/float64(s.Count))
				}
				fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\t%s\t\n", s.Name, s.Key, s.Count, s.LastMinute, s.Errors, mean, p95, most, each)
			}
			return tw.Flush()
		}, err
	})
}

// timings formats a summary's mean, p95 and max, or dashes if nothing
// was timed.
func timings(s trace.TimingSummary) (mean, p95, most string) {
	if s.Timed == 0 {
		return "-", "-", "-"
	}
	return fmt.Sprintf("%.1f", s.MeanMS), fmt.Sprintf("%.1f", s.P95MS), fmt.Sprintf("%.1f", s.MaxMS)
}

// queriesShown is how many statements `loom queries` prints; --json
// has them all.
const queriesShown = 25

func runQueries(args []string, stdout, stderr io.Writer) int {
	return introspect("queries", "queries [--json] [--url URL]", args, stdout, stderr, 0, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/queries", nil)
		return data, func(w io.Writer) error {
			var r loom.QueriesResult
			if err := json.Unmarshal(data, &r); err != nil {
				return err
			}
			fmt.Fprintf(w, "since %s (%s), the busiest first\n\n", r.Started.Local().Format("15:04:05"), time.Since(r.Started).Round(time.Second))
			if len(r.Queries) == 0 {
				fmt.Fprintln(w, "no statements yet (or no database)")
				return nil
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "TOTAL MS\tCOUNT\tLAST MIN\tMEAN MS\tP95 MS\tMAX MS\tROWS\tERRORS\tQUERY")
			for i, q := range r.Queries {
				if i == queriesShown {
					break
				}
				mean, p95, most := timings(q.TimingSummary)
				text := q.Query
				if len(text) > 100 {
					text = text[:100] + "…"
				}
				fmt.Fprintf(tw, "%.1f\t%d\t%d\t%s\t%s\t%s\t%d\t%d\t%s\n", q.TotalMS, q.Count, q.LastMinute, mean, p95, most, q.Rows, q.Errors, text)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if n := len(r.Queries) - queriesShown; n > 0 {
				fmt.Fprintf(w, "\n%d more; --json lists them all\n", n)
			}
			return nil
		}, err
	})
}
