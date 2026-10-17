// Command loom is the Loom command-line tool.
//
// Every command that prints something for people also accepts --json and
// prints a stable, documented JSON form instead (see docs/cli.md).
package main

import (
	"fmt"
	"os"
)

const usage = `loom is the tool for building Loom apps.

Usage:
  loom new [--json] [--module PATH] [--loom DIR] DIR
                                   create an app in DIR, with files for agents
  loom docs [--json] [TOPIC]       list Loom's docs, or print one
  loom check [--json] [path ...]   parse and type-check templates (default path: .)
  loom build [--json] [path ...]   check templates and generate Go code next to them
  loom test [--json] [--run RE] [package ...]
                                   build templates, then run go test (default ./...)
  loom dev [--json] [package]      run an app, rebuilding and reloading on changes,
                                   and applying migrations if $DATABASE_URL is set
  loom doctor [--json] [--url URL] [path]
                                   check templates, generated code, migrations, the
                                   environment and the running app, each with a fix

Ask a running app in dev mode (default --url $LOOM_URL, or http://localhost:$PORT, or :4000):
  loom status [--json] [--wait]    loom dev's build state: building, ready or error
  loom routes [--json]             list live routes
  loom manifest [--json]           what the app does, for review (docs/manifest.md)
  loom sessions [--json]           list open page sessions
  loom inspect [--json] SID        show a session's assigns and HTML
  loom event [--json] SID EVENT [KEY=VALUE ...]
                                   send an event to a session, as its browser would
  loom render [--json] [--assigns FILE] COMPONENT
                                   render a component with JSON assigns
  loom errors [--json]             show recent runtime errors, server and browser
  loom mail [--json]               show the mail the app kept in dev, with no mailer
  loom storage [--json] [--after KEY] [--limit N] [PREFIX]
                                   list the files in the app's storage bucket
  loom events [--json] [--since N] [--name NAME] [--sid SID] [--trace ID] [--level L] [--follow]
                                   what the app did: the event stream (docs/events.md)
  loom trace [--json] ID           every event of one action: a click, its commits, jobs
  loom stats [--json]              how often each event happened, and how long it took
  loom preview [--json]            list components' preview variants
                                   (the gallery is at /_loom/preview)
  loom preview screenshot [--variant NAME] [--theme light|dark] [--out DIR] [COMPONENT]
                                   render previews to PNG files in headless Chrome

Background jobs (same flags):
  loom jobs [--queue Q] [--state S] [--type T] [--search TEXT] [--before ID]
                                   list jobs, newest first
  loom jobs inspect ID             show a job's args, attempts, errors and timings
  loom jobs retry ID               run a job again now
  loom jobs cancel ID              cancel a job
  loom jobs delete ID              delete a job that isn't executing
  loom jobs retry-discarded [--queue Q]
                                   retry every discarded job
  loom jobs enqueue [--args FILE] [--in DURATION] TYPE
                                   enqueue a job with JSON args
  loom queues                      depth, running jobs and throughput per queue
  loom nodes                       the nodes running jobs: up or stale, the leader,
                                   workers busy per queue
  loom cron                        cron entries, next run and last result
  loom cron run INDEX              enqueue a cron entry's job now

Database (--db URL, default $DATABASE_URL; --dir DIR, default migrations):
  loom db new NAME                 create migrations/NNNN_NAME.sql
  loom db install PACKAGE          copy a sub-app's migrations into migrations/
  loom migrate                     apply pending migrations
  loom migrate status              list applied, pending and edited migrations
  loom db schema                   tables, columns, types and tracked tables
  loom db query [--limit N] SQL    run a query in a read-only transaction

Components (the starter kit; --dir DIR, default components):
  loom components [--json]         list the kit's components and their parameters
  loom components add NAME ...     copy components into the app, which then owns them

Review (needs git; no app):
  loom diff [--json] [BASE[..HEAD]]
                                   what a change does to the app: the loom.manifest
                                   difference from BASE (default origin/main) to HEAD or
                                   the working tree

Agents:
  loom mcp [--url URL] [--db URL]  serve the commands above as MCP tools on stdio

Run "loom help" to see this message.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "new":
		return runNew(args[1:], os.Stdout, os.Stderr)
	case "docs":
		return runDocs(args[1:], os.Stdout, os.Stderr)
	case "check":
		return runCheck(args[1:], os.Stdout, os.Stderr)
	case "build":
		return runBuild(args[1:], os.Stdout, os.Stderr)
	case "test":
		return runTest(args[1:], os.Stdout, os.Stderr)
	case "dev":
		return runDev(args[1:], os.Stdout, os.Stderr)
	case "status":
		return runStatus(args[1:], os.Stdout, os.Stderr)
	case "event":
		return runEvent(args[1:], os.Stdout, os.Stderr)
	case "routes":
		return runRoutes(args[1:], os.Stdout, os.Stderr)
	case "diff":
		return runDiff(args[1:], os.Stdout, os.Stderr)
	case "manifest":
		return runManifest(args[1:], os.Stdout, os.Stderr)
	case "sessions":
		return runSessions(args[1:], os.Stdout, os.Stderr)
	case "inspect":
		return runInspect(args[1:], os.Stdout, os.Stderr)
	case "render":
		return runRender(args[1:], os.Stdout, os.Stderr)
	case "errors":
		return runErrors(args[1:], os.Stdout, os.Stderr)
	case "mail":
		return runMail(args[1:], os.Stdout, os.Stderr)
	case "storage":
		return runStorage(args[1:], os.Stdout, os.Stderr)
	case "events":
		return runEvents(args[1:], os.Stdout, os.Stderr)
	case "trace":
		return runTrace(args[1:], os.Stdout, os.Stderr)
	case "stats":
		return runStats(args[1:], os.Stdout, os.Stderr)
	case "doctor":
		return runDoctor(args[1:], os.Stdout, os.Stderr)
	case "jobs":
		return runJobs(args[1:], os.Stdout, os.Stderr)
	case "queues":
		return runQueues(args[1:], os.Stdout, os.Stderr)
	case "nodes":
		return runNodes(args[1:], os.Stdout, os.Stderr)
	case "cron":
		return runCron(args[1:], os.Stdout, os.Stderr)
	case "db":
		return runDB(args[1:], os.Stdout, os.Stderr)
	case "migrate":
		return runMigrate(args[1:], os.Stdout, os.Stderr)
	case "components":
		return runComponents(args[1:], os.Stdout, os.Stderr)
	case "preview":
		return runPreview(args[1:], os.Stdout, os.Stderr)
	case "mcp":
		return runMCP(args[1:], os.Stdin, os.Stdout, os.Stderr)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "loom: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
