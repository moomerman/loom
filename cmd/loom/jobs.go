package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/moomerman/loom/jobs"
)

// The jobs commands ask a running app about its background jobs through
// its /_loom/dev/ endpoints, like the other introspection commands.

func runJobs(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "inspect":
			return runJobsInspect(args[1:], stdout, stderr)
		case "retry", "cancel":
			return runJobsChange(args[0], args[1:], stdout, stderr)
		case "delete":
			return runJobsDelete(args[1:], stdout, stderr)
		case "retry-discarded":
			return runJobsRetryDiscarded(args[1:], stdout, stderr)
		case "enqueue":
			return runJobsEnqueue(args[1:], stdout, stderr)
		}
	}
	var queue, state, typ, search string
	var limit int
	var before int64
	return introspectFlags("jobs", "jobs [--json] [--url URL] [--queue Q] [--state S] [--type T] [--search TEXT] [--before ID] [--limit N]", args, stdout, stderr, 0,
		func(fs *flag.FlagSet) {
			fs.StringVar(&queue, "queue", "", "only jobs in this queue")
			fs.StringVar(&state, "state", "", "only jobs in this state: scheduled, available, executing, completed, retryable, discarded or cancelled")
			fs.StringVar(&typ, "type", "", "only jobs of this type, e.g. todo.PruneCompleted")
			fs.StringVar(&search, "search", "", "only jobs whose args contain this text, ignoring case")
			fs.Int64Var(&before, "before", 0, "only jobs with ids below this: the next page after the last id shown")
			fs.IntVar(&limit, "limit", 100, "at most this many jobs, newest first")
		},
		func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
			q := url.Values{}
			b := ""
			if before > 0 {
				b = strconv.FormatInt(before, 10)
			}
			for k, v := range map[string]string{"queue": queue, "state": state, "type": typ, "search": search, "before": b, "limit": strconv.Itoa(limit)} {
				if v != "" {
					q.Set(k, v)
				}
			}
			data, err := c.do("GET", "/_loom/dev/jobs?"+q.Encode(), nil)
			return data, func(w io.Writer) error {
				var list []jobs.Record
				if err := json.Unmarshal(data, &list); err != nil {
					return err
				}
				if len(list) == 0 {
					fmt.Fprintln(w, "no jobs")
					return nil
				}
				tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
				fmt.Fprintln(tw, "ID\tTYPE\tQUEUE\tSTATE\tATTEMPT\tSCHEDULED\tLAST ERROR")
				for _, j := range list {
					last := ""
					if n := len(j.Errors); n > 0 {
						last = oneLine(j.Errors[n-1].Error, 60)
					}
					fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d/%d\t%s\t%s\n", j.ID, j.Type, j.Queue, j.State, j.Attempt, j.MaxAttempts, clock(j.ScheduledAt), last)
				}
				return tw.Flush()
			}, err
		})
}

func clock(t time.Time) string { return t.Local().Format("2006-01-02 15:04:05") }

func oneLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		s = s[:n-1] + "…"
	}
	return s
}

// printJob prints a job's details for people.
func printJob(w io.Writer, d *jobs.JobDetail) {
	args, _ := json.MarshalIndent(d.Args, "", "  ")
	fmt.Fprintf(w, "job %d: %s in queue %s, %s, attempt %d of %d\n\nargs:\n%s\n\n", d.ID, d.Type, d.Queue, d.State, d.Attempt, d.MaxAttempts, args)
	fmt.Fprintf(w, "inserted:  %s\nscheduled: %s\n", clock(d.InsertedAt), clock(d.ScheduledAt))
	if d.AttemptedAt != nil {
		fmt.Fprintf(w, "attempted: %s on %s", clock(*d.AttemptedAt), d.AttemptedBy)
		if d.WaitSeconds != nil {
			fmt.Fprintf(w, " (waited %.3fs)", *d.WaitSeconds)
		}
		fmt.Fprintln(w)
	}
	if d.FinishedAt != nil {
		fmt.Fprintf(w, "finished:  %s", clock(*d.FinishedAt))
		if d.RunSeconds != nil {
			fmt.Fprintf(w, " (ran %.3fs)", *d.RunSeconds)
		}
		fmt.Fprintln(w)
	}
	if d.LeaseUntil != nil {
		fmt.Fprintf(w, "lease:     until %s\n", clock(*d.LeaseUntil))
	}
	if len(d.Errors) == 0 {
		return
	}
	fmt.Fprintln(w, "\nerrors:")
	for _, e := range d.Errors {
		code := ""
		if e.Code != "" {
			code = " " + e.Code
		}
		fmt.Fprintf(w, "  attempt %d at %s%s: %s\n", e.Attempt, clock(e.At), code, e.Error)
		if e.Stack != "" {
			fmt.Fprintf(w, "    %s\n", strings.ReplaceAll(strings.TrimSpace(e.Stack), "\n", "\n    "))
		}
	}
}

func jobDetail(data []byte) func(io.Writer) error {
	return func(w io.Writer) error {
		var d jobs.JobDetail
		if err := json.Unmarshal(data, &d); err != nil {
			return err
		}
		printJob(w, &d)
		return nil
	}
}

func runJobsInspect(args []string, stdout, stderr io.Writer) int {
	return introspect("jobs inspect", "jobs inspect [--json] [--url URL] ID", args, stdout, stderr, 1, func(c *devClient, args []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/jobs/"+url.PathEscape(args[0]), nil)
		return data, jobDetail(data), err
	})
}

func runJobsChange(op string, args []string, stdout, stderr io.Writer) int {
	return introspect("jobs "+op, "jobs "+op+" [--json] [--url URL] ID", args, stdout, stderr, 1, func(c *devClient, args []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("POST", "/_loom/dev/jobs/"+url.PathEscape(args[0])+"/"+op, nil)
		return data, func(w io.Writer) error {
			var d jobs.JobDetail
			if err := json.Unmarshal(data, &d); err != nil {
				return err
			}
			fmt.Fprintf(w, "job %d (%s) is now %s\n", d.ID, d.Type, d.State)
			return nil
		}, err
	})
}

func runJobsDelete(args []string, stdout, stderr io.Writer) int {
	return introspect("jobs delete", "jobs delete [--json] [--url URL] ID", args, stdout, stderr, 1, func(c *devClient, args []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("POST", "/_loom/dev/jobs/"+url.PathEscape(args[0])+"/delete", nil)
		return data, func(w io.Writer) error {
			fmt.Fprintf(w, "deleted job %s\n", args[0])
			return nil
		}, err
	})
}

func runJobsRetryDiscarded(args []string, stdout, stderr io.Writer) int {
	var queue string
	return introspectFlags("jobs retry-discarded", "jobs retry-discarded [--json] [--url URL] [--queue Q]", args, stdout, stderr, 0,
		func(fs *flag.FlagSet) { fs.StringVar(&queue, "queue", "", "only discarded jobs in this queue") },
		func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
			data, err := c.do("POST", "/_loom/dev/jobs/retry-discarded?"+url.Values{"queue": {queue}}.Encode(), nil)
			return data, func(w io.Writer) error {
				var res struct{ Retried int }
				if err := json.Unmarshal(data, &res); err != nil {
					return err
				}
				fmt.Fprintf(w, "retried %d discarded %s\n", res.Retried, map[bool]string{true: "job", false: "jobs"}[res.Retried == 1])
				return nil
			}, err
		})
}

func runCronRun(args []string, stdout, stderr io.Writer) int {
	return introspect("cron run", "cron run [--json] [--url URL] INDEX", args, stdout, stderr, 1, func(c *devClient, args []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("POST", "/_loom/dev/cron/"+url.PathEscape(args[0])+"/run", nil)
		return data, func(w io.Writer) error {
			var res jobs.Result
			if err := json.Unmarshal(data, &res); err != nil {
				return err
			}
			fmt.Fprintf(w, "enqueued job %d\n", res.ID)
			return nil
		}, err
	})
}

func runJobsEnqueue(args []string, stdout, stderr io.Writer) int {
	var argsFile, in string
	return introspectFlags("jobs enqueue", "jobs enqueue [--json] [--url URL] [--args FILE|-] [--in DURATION] TYPE", args, stdout, stderr, 1,
		func(fs *flag.FlagSet) {
			fs.StringVar(&argsFile, "args", "", "a JSON file of the job's args, or - for stdin (default {})")
			fs.StringVar(&in, "in", "", "run after this delay, e.g. 10m")
		},
		func(c *devClient, args []string) ([]byte, func(io.Writer) error, error) {
			body := []byte("{}")
			var err error
			switch argsFile {
			case "":
			case "-":
				body, err = io.ReadAll(os.Stdin)
			default:
				body, err = os.ReadFile(argsFile)
			}
			if err != nil {
				return nil, nil, fmt.Errorf("%w: reading args: %v", errNoApp, err)
			}
			q := url.Values{"type": {args[0]}}
			if in != "" {
				q.Set("in", in)
			}
			data, err := c.do("POST", "/_loom/dev/jobs?"+q.Encode(), body)
			return data, func(w io.Writer) error {
				var res jobs.Result
				if err := json.Unmarshal(data, &res); err != nil {
					return err
				}
				if res.Duplicate {
					fmt.Fprintf(w, "not enqueued: duplicate of unique job %d\n", res.ID)
				} else {
					fmt.Fprintf(w, "enqueued job %d\n", res.ID)
				}
				return nil
			}, err
		})
}

func runQueues(args []string, stdout, stderr io.Writer) int {
	return introspect("queues", "queues [--json] [--url URL]", args, stdout, stderr, 0, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/queues", nil)
		return data, func(w io.Writer) error {
			var list []jobs.QueueInfo
			if err := json.Unmarshal(data, &list); err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Fprintln(w, "no queues: the app has no jobs")
				return nil
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "QUEUE\tRUNNING\tAVAILABLE\tSCHEDULED\tRETRYABLE\tOLDEST WAIT\tCOMPLETED/MIN\tFAILED/MIN")
			for _, q := range list {
				wait := "-"
				if q.OldestWaiting > 0 {
					wait = (time.Duration(q.OldestWaiting) * time.Second).String()
				}
				fmt.Fprintf(tw, "%s\t%d/%d\t%d\t%d\t%d\t%s\t%d\t%d\n", q.Name, q.Running, q.Concurrency, q.States[jobs.Available], q.States[jobs.Scheduled], q.States[jobs.Retryable], wait, q.Completed, q.Failed)
			}
			fmt.Fprintln(tw, "(running and per-minute counts are this node's)")
			return tw.Flush()
		}, err
	})
}

func runNodes(args []string, stdout, stderr io.Writer) int {
	return introspect("nodes", "nodes [--json] [--url URL]", args, stdout, stderr, 0, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/nodes", nil)
		return data, func(w io.Writer) error {
			var list []jobs.NodeInfo
			if err := json.Unmarshal(data, &list); err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Fprintln(w, "no nodes: none runs jobs (enqueue-only, or jobs not started yet)")
				return nil
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NODE\tSTATE\tLEADER\tQUEUES (BUSY/WORKERS)\tSTARTED\tLAST SEEN")
			for _, n := range list {
				name := n.Name
				if n.Self {
					name += " (this one)"
				}
				leader := "-"
				if n.Leader {
					leader = "leader"
				}
				var qs []string
				for _, q := range n.Queues {
					qs = append(qs, fmt.Sprintf("%s %d/%d", q.Name, q.Running, q.Concurrency))
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", name, n.State, leader, strings.Join(qs, ", "), clock(n.StartedAt), clock(n.SeenAt))
			}
			return tw.Flush()
		}, err
	})
}

func runCron(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "run" {
		return runCronRun(args[1:], stdout, stderr)
	}
	return introspect("cron", "cron [--json] [--url URL]", args, stdout, stderr, 0, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/cron", nil)
		return data, func(w io.Writer) error {
			var list []jobs.CronInfo
			if err := json.Unmarshal(data, &list); err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Fprintln(w, "no cron entries")
				return nil
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "INDEX\tSCHEDULE (UTC)\tTYPE\tNEXT RUN (LOCAL TIME)\tLAST RUN\tLAST RESULT")
			for _, e := range list {
				typ := e.Type
				if e.Builtin {
					typ += " (built in)"
				}
				last, result := "-", "-"
				if e.LastTick != nil {
					last = clock(*e.LastTick)
					result = fmt.Sprintf("job %d %s", *e.LastJob, e.LastState)
				}
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", e.Index, e.Expr, typ, clock(e.NextRun), last, result)
			}
			return tw.Flush()
		}, err
	})
}
