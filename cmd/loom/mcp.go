package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/compiler"
	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/kit"
	"github.com/moomerman/loom/kit/manage"
)

// loom mcp serves Loom's tools to agents over the Model Context Protocol:
// JSON-RPC 2.0 messages, one per line, on stdin and stdout. Every tool
// returns the same JSON as the matching CLI command with --json (see
// docs/cli.md). Tools that ask the app need it running in dev mode.

const mcpProtocolVersion = "2025-06-18"

// mcpTool is a tool: its schema and how to call it.
type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	call        func(c *devClient, args map[string]any) ([]byte, error)
	// images, set instead of call, returns JSON and images to show.
	images func(c *devClient, args map[string]any) ([]byte, [][]byte, error)
}

func schema(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func argString(args map[string]any, key string) string {
	switch v := args[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func mcpTools() []mcpTool {
	get := func(path string) func(c *devClient, _ map[string]any) ([]byte, error) {
		return func(c *devClient, _ map[string]any) ([]byte, error) { return c.do("GET", path, nil) }
	}
	id := map[string]any{"type": "integer", "description": "the job's id"}
	jobOp := func(op string) func(c *devClient, args map[string]any) ([]byte, error) {
		return func(c *devClient, args map[string]any) ([]byte, error) {
			return c.do("POST", "/_loom/dev/jobs/"+url.PathEscape(argString(args, "id"))+"/"+op, nil)
		}
	}
	return []mcpTool{
		{
			Name:        "check",
			Description: "Parse and type-check .hx templates (like `loom check --json`). Returns a list of diagnostics, each with code, severity, file, line, col, message and fix; empty means every template is valid. Doesn't need a running app.",
			InputSchema: schema(nil, map[string]any{"path": str("directory or file to check (default .)")}),
			call: func(_ *devClient, args map[string]any) ([]byte, error) {
				path := argString(args, "path")
				if path == "" {
					path = "."
				}
				res, err := compiler.Check(path)
				if err != nil {
					return nil, err
				}
				return json.Marshal(diagnostics(res.Diagnostics))
			},
		},
		{
			Name:        "docs",
			Description: "Read Loom's documentation (like `loom docs --json`). With no topic, lists the topics in reading order, each with name, title and summary; with a topic, such as syntax or live, returns its Markdown as content. Doesn't need a running app.",
			InputSchema: schema(nil, map[string]any{"topic": str("the topic to read, e.g. syntax (default: list the topics)")}),
			call: func(_ *devClient, args map[string]any) ([]byte, error) {
				return jsonOf(docsJSON(argString(args, "topic")))
			},
		},
		{
			Name:        "build",
			Description: "Check templates and generate the Go code next to each (like `loom build --json`): returns the files written and the diagnostics. Run it after editing .hx files: Go code built from stale templates still compiles. Doesn't need a running app; under `loom dev`, saving rebuilds by itself (see dev_status).",
			InputSchema: schema(nil, map[string]any{"path": str("directory or file to build (default .)")}),
			call: func(_ *devClient, args map[string]any) ([]byte, error) {
				path := argString(args, "path")
				if path == "" {
					path = "."
				}
				res, written, err := compiler.Build(path)
				if err != nil {
					return nil, err
				}
				return json.Marshal(buildOutput{Written: append([]string{}, written...), Diagnostics: diagnostics(res.Diagnostics)})
			},
		},
		{
			Name:        "test",
			Description: "Build the templates, then run go test and summarize it (`loom test --json`): passed, the files written, template diagnostics (with errors no tests run), and per package its result and each failed test's output. Doesn't need a running app.",
			InputSchema: schema(nil, map[string]any{
				"packages": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": `packages to test (default ["./..."])`},
				"run":      str("run only tests matching this regular expression, as go test -run"),
				"race":     map[string]any{"type": "boolean", "description": "run with the race detector, as go test -race (default false)"},
			}),
			call: func(_ *devClient, args map[string]any) ([]byte, error) {
				var pkgs []string
				if list, ok := args["packages"].([]any); ok {
					for _, p := range list {
						if s, ok := p.(string); ok {
							pkgs = append(pkgs, s)
						}
					}
				}
				race, _ := args["race"].(bool)
				return jsonOf(loomTest(context.Background(), argString(args, "run"), race, pkgs))
			},
		},
		{
			Name:        "dev_status",
			Description: "Ask `loom dev` whether the app rebuilt after the last change: state building, ready or error, and for an error its kind (migration, template, go, exit), diagnostics and output (`loom status --json`). With wait, first waits for the rebuild of a change just saved. Use it after every edit while `loom dev` runs.",
			InputSchema: schema(nil, map[string]any{"wait": map[string]any{"type": "boolean", "description": "wait for a rebuild in progress, or about to start, to finish (default false)"}}),
			call: func(c *devClient, args map[string]any) ([]byte, error) {
				if args["wait"] == true {
					return c.doTimeout(3*time.Minute, "GET", devStatusPath+"?wait=true", nil)
				}
				return c.do("GET", devStatusPath, nil)
			},
		},
		{
			Name:        "components",
			Description: "List the starter kit's components: each file, the components it declares, and their parameters with type, default and whether required (`loom components --json`). Add one to the app with `loom components add NAME`. Doesn't need a running app.",
			InputSchema: schema(nil, map[string]any{}),
			call: func(*devClient, map[string]any) ([]byte, error) {
				return jsonOf(manage.Catalog(kit.Default))
			},
		},
		{
			Name:        "components_add",
			Description: "Copy starter kit components into the app's components directory and build them (`loom components add --json NAME...`): returns each result, the files written and diagnostics. Doesn't need a running app.",
			InputSchema: schema([]string{"names"}, map[string]any{
				"names": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": `component names, from the components tool, e.g. ["button", "input"]`},
				"dir":   str("the app's components directory (default components)"),
			}),
			call: func(_ *devClient, args map[string]any) ([]byte, error) {
				cli := []string{"--json"}
				if dir := argString(args, "dir"); dir != "" {
					cli = append(cli, "--dir", dir)
				}
				if list, ok := args["names"].([]any); ok {
					for _, n := range list {
						if s, ok := n.(string); ok {
							cli = append(cli, s)
						}
					}
				}
				var out, errOut bytes.Buffer
				if code := runComponents(append([]string{"add"}, cli...), &out, &errOut); code == 2 {
					return nil, errors.New(strings.TrimSpace(errOut.String()))
				}
				return out.Bytes(), nil
			},
		},
		{
			Name:        "manifest_diff",
			Description: "What a change does to the app's behaviour: the loom.manifest difference between two git revisions, per file and section (routes, events, migrations, jobs, review flags), each item added, removed or changed (`loom diff --json`; docs/manifest.md). Doesn't need a running app.",
			InputSchema: schema(nil, map[string]any{"range": str("BASE..HEAD, comparing from where HEAD branched off BASE, or BASE alone to compare with the working tree (default origin/main, or main)")}),
			call: func(_ *devClient, args map[string]any) ([]byte, error) {
				spec := argString(args, "range")
				if spec == "" {
					spec = defaultBase()
				}
				d, err := manifestDiff(spec)
				if err != nil {
					return nil, err
				}
				return json.Marshal(d)
			},
		},
		{Name: "manifest", Description: "Describe what the app does, for review: routes with their auth and the events each page sends, the tables each page watches, layouts, handlers, mounts, jobs and cron, and flags for a reviewer (`loom manifest --json`; docs/manifest.md).", InputSchema: schema(nil, map[string]any{}), call: get("/_loom/dev/manifest")},
		{Name: "routes", Description: "List the app's live routes: path, page type and component (`loom routes --json`).", InputSchema: schema(nil, map[string]any{}), call: get("/_loom/dev/routes")},
		{Name: "sessions", Description: "List open page sessions: sid, path, version, connected streams, restarts (`loom sessions --json`).", InputSchema: schema(nil, map[string]any{}), call: get("/_loom/dev/sessions")},
		{
			Name:        "inspect_session",
			Description: "Show a page session's current assigns (template field names), HTML and pending c.SendAfter timers (`loom inspect --json SID`).",
			InputSchema: schema([]string{"sid"}, map[string]any{"sid": str("the session id, from sessions")}),
			call: func(c *devClient, args map[string]any) ([]byte, error) {
				return c.do("GET", "/_loom/dev/sessions/"+url.PathEscape(argString(args, "sid")), nil)
			},
		},
		{
			Name:        "render_component",
			Description: "Render any compiled component with assigns given as JSON with template (snake_case) field names (`loom render --json`).",
			InputSchema: schema([]string{"component"}, map[string]any{
				"component": str(`component name, e.g. "TodoApp" or "templates.TodoApp"`),
				"assigns":   map[string]any{"type": "object", "description": "the component's parameters"},
			}),
			call: func(c *devClient, args map[string]any) ([]byte, error) {
				body := []byte("{}")
				if a, ok := args["assigns"]; ok && a != nil {
					body, _ = json.Marshal(a)
				}
				return c.do("POST", "/_loom/dev/render?component="+url.QueryEscape(argString(args, "component")), body)
			},
		},
		{Name: "previews", Description: "List every component's preview variants, from its enum and bool parameters and @preview sample values, or why it has none (`loom preview --json`). The gallery is at /_loom/preview.", InputSchema: schema(nil, map[string]any{}), call: get("/_loom/dev/previews")},
		{
			Name:        "preview_screenshot",
			Description: "Screenshot a component's previews in headless Chrome and return the PNG images: every variant in one image, or one variant; light, dark or both themes (`loom preview screenshot`). Use it to see what a component looks like.",
			InputSchema: schema([]string{"component"}, map[string]any{
				"component": str("the component, e.g. Button or components.Button"),
				"variant":   str("one variant's name, from the previews tool (default: all)"),
				"theme":     str("light, dark or a theme from tokens.toml (default: each)"),
			}),
			images: func(c *devClient, args map[string]any) ([]byte, [][]byte, error) {
				var pngs [][]byte
				shots, err := screenshots(c, argString(args, "component"), argString(args, "variant"), argString(args, "theme"), func(_ Screenshot, png []byte) error {
					pngs = append(pngs, png)
					return nil
				})
				if err != nil {
					return nil, nil, err
				}
				for i := range shots {
					shots[i].File = ""
				}
				data, err := json.Marshal(map[string]any{"screenshots": shots})
				return data, pngs, err
			},
		},
		{
			Name:        "send_event",
			Description: "Send an event to a page session, as its browser would on a click or submit (`loom event --json SID EVENT KEY=VALUE...`): returns the page afterwards (assigns and HTML), the errors the event caused, whether the session ended, and what the event did: its trace id and events (the handler's ms and diff bytes, commits, jobs enqueued, broadcasts). Open browsers update as usual. Get session ids from the sessions tool.",
			InputSchema: schema([]string{"sid", "event"}, map[string]any{
				"sid":     str("the session id, from sessions"),
				"event":   str(`the event name, as in the template's on:click="..."`),
				"payload": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "the payload: val-* attributes without the prefix, form fields, value or checked; all strings"},
			}),
			call: func(c *devClient, args map[string]any) ([]byte, error) {
				var pairs []string
				if p, ok := args["payload"].(map[string]any); ok {
					for k := range p {
						pairs = append(pairs, k+"="+argString(p, k))
					}
				}
				body, err := eventBody(argString(args, "event"), pairs)
				if err != nil {
					return nil, err
				}
				return c.do("POST", "/_loom/dev/sessions/"+url.PathEscape(argString(args, "sid"))+"/event", body)
			},
		},
		{Name: "recent_errors", Description: "Show recent runtime errors, newest last: server errors (panics, c.Fail, failed job attempts) and, in dev, browser errors (uncaught script errors, console.error, behavior failures), each with source, code, message, location and fix (`loom errors --json`).", InputSchema: schema(nil, map[string]any{}), call: get("/_loom/dev/errors")},
		{
			Name:        "events",
			Description: "Show what the app did, oldest first: the event stream (docs/events.md), e.g. page.event (a handler: ms, diff bytes, outcome), tx.commit, job.enqueue, job.finish, pubsub.publish, event.refused, error (`loom events --json`). Pass the result's next as since on the next call to see only what happened after it: call once to get a cursor, act, then call with since.",
			InputSchema: schema(nil, map[string]any{
				"since": map[string]any{"type": "integer", "description": "only events after this sequence number: the next of an earlier call"},
				"name":  str(`only events of this name, or group ("job.*")`),
				"sid":   str("only events of this page session"),
				"trace": str("only events of this trace: one action and what it caused"),
				"level": str("only events at this level or above: debug, info, warn or error"),
			}),
			call: func(c *devClient, args map[string]any) ([]byte, error) {
				var since uint64
				fmt.Sscan(argString(args, "since"), &since)
				return c.do("GET", "/_loom/dev/events"+eventQuery(since, argString(args, "name"), argString(args, "sid"), argString(args, "trace"), argString(args, "level")), nil)
			},
		},
		{Name: "trace", Description: "Show every event of one action, oldest first: a click's handler, its commits, the jobs it enqueued and their attempts, their broadcasts and the re-renders they caused (`loom trace --json ID`). send_event returns the event's trace id; events carry theirs.", InputSchema: schema([]string{"id"}, map[string]any{"id": str("the trace id")}), call: func(c *devClient, args map[string]any) ([]byte, error) {
			return c.do("GET", "/_loom/dev/events"+eventQuery(0, "", "", argString(args, "id"), ""), nil)
		}},
		{Name: "stats", Description: "Count the app's events since it started, by name and by route, job type or table, with errors, the count in the last minute, and mean, p95 and max milliseconds: how often handlers, re-renders, commits and jobs ran and how long they took (`loom stats --json`).", InputSchema: schema(nil, map[string]any{}), call: get("/_loom/dev/stats")},
		{Name: "queries", Description: "Every SQL statement the app's database ran since it started, Loom's own included, the busiest (by total time) first: count, count in the last minute, mean, p95 and max milliseconds, rows and errors. A fast query that runs many times a second shows here, where db.slow never would (`loom queries --json`).", InputSchema: schema(nil, map[string]any{}), call: get("/_loom/dev/queries")},
		{Name: "topics", Description: "Map each PubSub topic the open pages subscribe to to the sessions subscribed (GET /_loom/dev/topics): who hears a broadcast.", InputSchema: schema(nil, map[string]any{}), call: get("/_loom/dev/topics")},
		{
			Name:        "doctor",
			Description: "Check the app in one call, each finding with a fix: templates and generated code, DATABASE_URL and migrations, Chrome, .mcp.json, and the running app's health and startup checks (`loom doctor --json`). Run it first when something is off.",
			InputSchema: schema(nil, map[string]any{"path": str("the app's directory (default .)")}),
			call: func(c *devClient, args map[string]any) ([]byte, error) {
				path := argString(args, "path")
				if path == "" {
					path = "."
				}
				return json.Marshal(doctor(path, c.db, c))
			},
		},
		{Name: "mail", Description: "Show the mail the app sent, oldest first, when it runs under `loom dev` with no mailer: each mail's time, to, subject and text, so you can follow a sign-in or password-reset link (`loom mail --json`).", InputSchema: schema(nil, map[string]any{}), call: get("/_loom/dev/mail")},
		{
			Name:        "storage_list",
			Description: "List the files in the app's storage bucket (loom.Config.Storage), in key order: each object's key, size, ETag and time (`loom storage --json`).",
			InputSchema: schema(nil, map[string]any{
				"prefix": str("only keys starting with this, e.g. avatars/"),
				"after":  str("only keys after this one, for the next page"),
				"limit":  map[string]any{"type": "integer", "description": "at most this many (default 100)"},
			}),
			call: func(c *devClient, args map[string]any) ([]byte, error) {
				q := url.Values{}
				for _, k := range []string{"prefix", "after", "limit"} {
					if v := argString(args, k); v != "" {
						q.Set(k, v)
					}
				}
				return c.do("GET", "/_loom/dev/storage?"+q.Encode(), nil)
			},
		},
		{
			Name:        "jobs_list",
			Description: "List background jobs, newest first, with state, attempts and errors (`loom jobs --json`).",
			InputSchema: schema(nil, map[string]any{
				"queue":  str("only jobs in this queue"),
				"state":  str("only jobs in this state: scheduled, available, executing, completed, retryable, discarded or cancelled"),
				"type":   str("only jobs of this type, e.g. todo.PruneCompleted"),
				"search": str("only jobs whose args contain this text, ignoring case"),
				"before": map[string]any{"type": "integer", "description": "only jobs with ids below this, for the next page"},
				"limit":  map[string]any{"type": "integer", "description": "at most this many (default 100)"},
			}),
			call: func(c *devClient, args map[string]any) ([]byte, error) {
				q := url.Values{}
				for _, k := range []string{"queue", "state", "type", "search", "before", "limit"} {
					if v := argString(args, k); v != "" {
						q.Set(k, v)
					}
				}
				return c.do("GET", "/_loom/dev/jobs?"+q.Encode(), nil)
			},
		},
		{Name: "jobs_inspect", Description: "Show one job: args, attempts, errors with stack traces, and timings (`loom jobs inspect --json ID`).", InputSchema: schema([]string{"id"}, map[string]any{"id": id}), call: func(c *devClient, args map[string]any) ([]byte, error) {
			return c.do("GET", "/_loom/dev/jobs/"+url.PathEscape(argString(args, "id")), nil)
		}},
		{Name: "jobs_retry", Description: "Run a scheduled, retryable, completed, discarded or cancelled job again now (`loom jobs retry --json ID`).", InputSchema: schema([]string{"id"}, map[string]any{"id": id}), call: jobOp("retry")},
		{Name: "jobs_cancel", Description: "Cancel a job that hasn't finished (`loom jobs cancel --json ID`).", InputSchema: schema([]string{"id"}, map[string]any{"id": id}), call: jobOp("cancel")},
		{Name: "jobs_delete", Description: "Delete a job that isn't executing (`loom jobs delete --json ID`).", InputSchema: schema([]string{"id"}, map[string]any{"id": id}), call: jobOp("delete")},
		{Name: "jobs_retry_discarded", Description: "Retry every discarded job, or those of one queue (`loom jobs retry-discarded --json`).", InputSchema: schema(nil, map[string]any{"queue": str("only discarded jobs in this queue")}), call: func(c *devClient, args map[string]any) ([]byte, error) {
			return c.do("POST", "/_loom/dev/jobs/retry-discarded?"+url.Values{"queue": {argString(args, "queue")}}.Encode(), nil)
		}},
		{
			Name:        "jobs_enqueue",
			Description: "Enqueue a job of a registered type with JSON args (`loom jobs enqueue --json`). Returns its id, or the existing job's id for a duplicate unique job.",
			InputSchema: schema([]string{"type"}, map[string]any{
				"type": str(`the job type, e.g. "PruneCompleted" or "todo.PruneCompleted"`),
				"args": map[string]any{"type": "object", "description": "the job's args, by their json tag names"},
				"in":   str(`run after this delay, a Go duration such as "10m"`),
			}),
			call: func(c *devClient, args map[string]any) ([]byte, error) {
				body := []byte("{}")
				if a, ok := args["args"]; ok && a != nil {
					body, _ = json.Marshal(a)
				}
				q := url.Values{"type": {argString(args, "type")}}
				if in := argString(args, "in"); in != "" {
					q.Set("in", in)
				}
				return c.do("POST", "/_loom/dev/jobs?"+q.Encode(), body)
			},
		},
		{Name: "queues", Description: "Show each job queue's concurrency, running jobs, jobs by state, and throughput (`loom queues --json`).", InputSchema: schema(nil, map[string]any{}), call: get("/_loom/dev/queues")},
		{Name: "nodes", Description: "Show the job runners' node registry: each node up or stale, which one leads (runs cron and rescue), its queues with workers busy, and when it started and was last seen (`loom nodes --json`).", InputSchema: schema(nil, map[string]any{}), call: get("/_loom/dev/nodes")},
		{Name: "cron", Description: "Show cron entries with their index, next run and last result (`loom cron --json`).", InputSchema: schema(nil, map[string]any{}), call: get("/_loom/dev/cron")},
		{Name: "cron_run", Description: "Enqueue a cron entry's job to run now (`loom cron run --json INDEX`).", InputSchema: schema([]string{"index"}, map[string]any{"index": map[string]any{"type": "integer", "description": "the entry's index, from the cron tool"}}), call: func(c *devClient, args map[string]any) ([]byte, error) {
			return c.do("POST", "/_loom/dev/cron/"+url.PathEscape(argString(args, "index"))+"/run", nil)
		}},
		{
			Name:        "db_schema",
			Description: "Show the database's tables: columns with types, nullability and defaults, primary keys, and which tables are tracked (their changes re-render pages that loom.Watch them) (`loom db schema --json`). Read it before writing SQL instead of guessing. Talks to the database directly; doesn't need a running app.",
			InputSchema: schema(nil, map[string]any{}),
			call: func(c *devClient, _ map[string]any) ([]byte, error) {
				return jsonOf(dbSchema(context.Background(), c.db))
			},
		},
		{
			Name:        "migrate",
			Description: "Apply pending migrations to the database (`loom migrate --json`): returns the files applied, with timings. Changing an applied migration is an error; add a new one with `loom db new NAME`. Under `loom dev` they apply on save. Doesn't need a running app.",
			InputSchema: schema(nil, map[string]any{}),
			call: func(c *devClient, _ map[string]any) ([]byte, error) {
				ctx := context.Background()
				db, err := c.db.open(ctx)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				applied, err := data.Migrate(ctx, db, c.db.fsys())
				if err != nil {
					return nil, err
				}
				return json.Marshal(loom.MigrateResult{Applied: applied})
			},
		},
		{
			Name:        "migrate_status",
			Description: "List migrations and whether each is applied, pending, changed since it was applied, or missing, with any problems (`loom migrate status --json`). Doesn't need a running app.",
			InputSchema: schema(nil, map[string]any{}),
			call: func(c *devClient, _ map[string]any) ([]byte, error) {
				return jsonOf(migrationStatus(context.Background(), c.db))
			},
		},
		{
			Name:        "db_query",
			Description: "Run a SQL query in a read-only transaction and return its columns and rows (`loom db query --json`). Writes fail; change data through the app or a migration. Doesn't need a running app.",
			InputSchema: schema([]string{"sql"}, map[string]any{
				"sql":   str("the query, with literal values (no $1 parameters)"),
				"limit": map[string]any{"type": "integer", "description": "at most this many rows (default 100)"},
			}),
			call: func(c *devClient, args map[string]any) ([]byte, error) {
				limit := 100
				if n, err := strconv.Atoi(argString(args, "limit")); err == nil && n > 0 {
					limit = n
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				return jsonOf(dbQuery(ctx, c.db, argString(args, "sql"), limit))
			},
		},
	}
}

// jsonOf encodes a tool's result, or passes on its error.
func jsonOf[T any](v T, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func runMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: loom mcp [--url URL] [--db URL] [--migrations DIR]")
		fs.PrintDefaults()
	}
	base := fs.String("url", appURL(), "the app's URL (default $LOOM_URL, or http://localhost:$PORT, or "+defaultURL+")")
	db := &dbOpts{}
	fs.StringVar(&db.url, "db", os.Getenv("DATABASE_URL"), "the database URL, for the database tools (default $DATABASE_URL)")
	fs.StringVar(&db.dir, "migrations", defaultMigrations, "the migrations directory, for migrate_status")
	if err := fs.Parse(interleave(fs, args)); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	c := &devClient{base: strings.TrimRight(*base, "/"), http: &http.Client{Timeout: 30 * time.Second}, db: db}
	if err := serveMCP(c, stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "loom mcp: %v\n", err)
		return 2
	}
	return 0
}

// serveMCP answers requests until stdin ends.
func serveMCP(c *devClient, stdin io.Reader, stdout io.Writer) error {
	tools := mcpTools()
	byName := map[string]mcpTool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			enc.Encode(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error: " + err.Error()}})
			continue
		}
		if len(req.ID) == 0 {
			continue // a notification, such as notifications/initialized
		}
		res := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(req.Params, &p)
			version := mcpProtocolVersion
			if p.ProtocolVersion != "" {
				version = p.ProtocolVersion
			}
			res.Result = map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "loom", "version": "0.1"},
				"instructions":    "Loom dev tools. docs reads Loom's documentation (start with docs, no topic); check, build, test, components and components_add work on the code without an app; db_schema, migrate, migrate_status and db_query talk to the database at $DATABASE_URL (or --db); the other tools ask the app running under `loom dev` at " + c.base + ". Each returns the JSON of the matching `loom ... --json` command (docs/cli.md); errors carry a code, message and fix (docs/errors.md).",
			}
		case "ping":
			res.Result = map[string]any{}
		case "tools/list":
			res.Result = map[string]any{"tools": tools}
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil {
				res.Error = &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
				break
			}
			t, ok := byName[p.Name]
			if !ok {
				res.Error = &rpcError{Code: -32602, Message: "unknown tool " + p.Name}
				break
			}
			res.Result = callTool(c, t, p.Arguments)
		default:
			res.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
		}
		if err := enc.Encode(res); err != nil {
			return err
		}
	}
	return sc.Err()
}

// callTool runs a tool and wraps its JSON, or its error, as MCP content.
func callTool(c *devClient, t mcpTool, args map[string]any) map[string]any {
	if args == nil {
		args = map[string]any{}
	}
	var out []byte
	var images [][]byte
	var err error
	if t.images != nil {
		out, images, err = t.images(c, args)
	} else {
		out, err = t.call(c, args)
	}
	var ae *apiError
	var de *data.Error
	switch {
	case errors.As(err, &ae):
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(out)}}, "isError": true}
	case errors.As(err, &de):
		b, _ := json.Marshal(de)
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}, "isError": true}
	case err != nil:
		msg := strings.TrimPrefix(err.Error(), errNoApp.Error()+": ")
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": msg}}, "isError": true}
	}
	content := []any{map[string]any{"type": "text", "text": string(out)}}
	for _, img := range images {
		content = append(content, map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(img)})
	}
	return map[string]any{"content": content}
}
