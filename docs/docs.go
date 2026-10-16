// Package docs holds Loom's documentation, embedded so that `loom docs`
// and the `docs` MCP tool can show it outside this repository: an agent
// working on an app has the loom command but not Loom's source.
package docs

import (
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"
)

//go:embed *.md
var files embed.FS

// Topic is one document: its name (the file name without .md), its
// title (the first heading) and a one-line summary.
type Topic struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// index lists the documents in reading order, with their summaries. It
// is the docs part of llms.txt; a test keeps the two the same.
var index = []struct{ name, summary string }{
	{"getting-started", "start a new app with `loom new`: the files it makes, the agent files, the loop"},
	{"syntax", "the complete template language"},
	{"live", "live pages in Go: Mount, Assigns, Events, watching tables, restarts, hints, loomtest"},
	{"forms", "forms: a typed input struct, validation, per-field errors, change and submit, names checked at every seam"},
	{"example-jobsweb", "the reference example, a mountable jobs dashboard, file by file"},
	{"example-issues", "a Linear-style issue tracker: sign-in, a live layout's state, menus, keyboard shortcuts, live updates across users"},
	{"example-tally", "a shared-expenses app built from the docs alone: watched tables, a form the server computes, restart-safe input, cron"},
	{"layouts", "root layouts, app layouts, live layouts with their own state and events (kept across live navigation), route groups, titles, page metadata"},
	{"components", "design tokens, scoped styles, variants, the starter kit"},
	{"data", "the database: Select/Get, transactions, AfterCommit, migrations, loomtest.DB"},
	{"auth", "sign-in: the auth sub-app (passwords, email links, resets, Google and GitHub, two-factor), mail, requiring a user, the typed user, sessions, CSRF, testing"},
	{"subapps", "auth checks, middleware, dev-only groups, mounting sub-apps, their migrations, path and asset"},
	{"jobs", "background jobs: enqueue, retries, cron, testing"},
	{"storage", "file storage: a bucket on S3, R2, MinIO, disk or memory; put, get, list, serve; files and transactions"},
	{"manifest", "loom.manifest, the app's behaviour for review, kept fresh by a test"},
	{"errors", "every compile and runtime error code and its fix"},
	{"events", "the event stream: what a running app did, traces from a click to its jobs, loom events, trace, stats"},
	{"deploy", "production: migrating from the binary, a rollout, several nodes, proxies, Postgres"},
	{"cli", "the loom command and its JSON output"},
	{"wire", "render trees and diffs sent to the browser"},
	{"design", "the choices that shape Loom, in a line or two each"},
}

// Topics lists every document in reading order.
func Topics() []Topic {
	out := make([]Topic, 0, len(index))
	for _, e := range index {
		out = append(out, Topic{Name: e.name, Title: title(Read(e.name)), Summary: e.summary})
	}
	return out
}

// ErrUnknown is returned, wrapped, by Lookup for a name that isn't a
// topic.
var ErrUnknown = errors.New("unknown docs topic")

// Lookup returns a topic and its Markdown. The name may end in .md.
func Lookup(name string) (Topic, string, error) {
	name = strings.TrimSuffix(strings.ToLower(name), ".md")
	for _, t := range Topics() {
		if t.Name == name {
			return t, Read(name), nil
		}
	}
	names := make([]string, 0, len(index))
	for _, e := range index {
		names = append(names, e.name)
	}
	sort.Strings(names)
	return Topic{}, "", fmt.Errorf("%w %q; the topics are %s (run `loom docs` to list them)", ErrUnknown, name, strings.Join(names, ", "))
}

// Read returns a document's Markdown, or "" if there is none.
func Read(name string) string {
	b, err := files.ReadFile(name + ".md")
	if err != nil {
		return ""
	}
	return string(b)
}

// Names returns the names of the embedded files, sorted, for tests.
func Names() []string {
	entries, _ := files.ReadDir(".")
	var out []string
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".md"))
	}
	return out
}

// title returns the text of a document's first "# " heading.
func title(md string) string {
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return ""
}
