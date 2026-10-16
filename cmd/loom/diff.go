package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// loom diff: what a change does to the app's behaviour, from the
// loom.manifest files at two revisions (docs/manifest.md). Reviewers
// and agents get "this adds a route, two events and a daily job"
// instead of a code diff.

// ManifestDiff is `loom diff --json`.
type ManifestDiff struct {
	Base  string     `json:"base"` // the commit compared from
	Head  string     `json:"head"` // the commit compared to, or "" for the working tree
	Files []FileDiff `json:"files"`
}

// FileDiff is the change to one loom.manifest.
type FileDiff struct {
	Path     string        `json:"path"`
	Status   string        `json:"status"` // added, removed or modified
	Sections []SectionDiff `json:"sections"`
}

// SectionDiff is the change to one section, such as routes.
type SectionDiff struct {
	Section string       `json:"section"`
	Added   []ItemChange `json:"added"`
	Removed []ItemChange `json:"removed"`
	Changed []ItemChange `json:"changed"`
}

// ItemChange is an item (a route, a job, a migration) added, removed or
// changed. Added and removed items carry all their lines in Lines; a
// changed one carries the lines removed and added.
type ItemChange struct {
	Item    string   `json:"item"`          // its first line, as now (or as it was, if removed)
	Was     string   `json:"was,omitempty"` // its first line before, if that changed
	Lines   []string `json:"lines,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Added   []string `json:"added,omitempty"`
}

const manifestName = "loom.manifest"

func runDiff(args []string, stdout, stderr io.Writer) int {
	fs := newCommand("diff", "diff [--json] [BASE[..HEAD]]", "print JSON", stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: loom diff [--json] [BASE[..HEAD]]\n\nCompare the loom.manifest files at BASE (default origin/main, or main) with HEAD, or with the\nworking tree when HEAD is left out. BASE..HEAD compares from where HEAD\nbranched off BASE, as a pull request does.\n")
	}
	if !fs.parse(args, 0, 1) {
		return 2
	}
	asJSON := fs.JSON
	spec := defaultBase()
	if fs.NArg() == 1 {
		spec = fs.Arg(0)
	}
	d, err := manifestDiff(spec)
	if err != nil {
		fmt.Fprintf(stderr, "loom diff: %v\n", err)
		return 2
	}
	if *asJSON {
		writeJSON(stdout, d)
		return 0
	}
	printDiff(stdout, d)
	return 0
}

// manifestDiff compares the manifests named by spec: "BASE..HEAD" or
// "BASE" (against the working tree).
func manifestDiff(spec string) (*ManifestDiff, error) {
	top, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not in a git repository: %w", err)
	}
	top = strings.TrimSpace(top)
	baseRev, headRev, ranged := strings.Cut(spec, "..")
	d := &ManifestDiff{}
	if ranged {
		if headRev == "" {
			headRev = "HEAD"
		}
		if d.Head, err = revParse(headRev); err != nil {
			return nil, err
		}
		mb, err := git("merge-base", baseRev, headRev)
		if err != nil {
			return nil, fmt.Errorf("no common ancestor of %s and %s: %w", baseRev, headRev, err)
		}
		d.Base = strings.TrimSpace(mb)
	} else if d.Base, err = revParse(baseRev); err != nil {
		return nil, err
	}
	before, err := manifestsAt(top, d.Base)
	if err != nil {
		return nil, err
	}
	var after map[string]string
	if d.Head != "" {
		after, err = manifestsAt(top, d.Head)
	} else {
		after, err = workingManifests(top)
	}
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for p := range before {
		paths[p] = true
	}
	for p := range after {
		paths[p] = true
	}
	d.Files = []FileDiff{}
	for _, p := range sortedKeys(paths) {
		a, inBefore := before[p]
		b, inAfter := after[p]
		if a == b {
			continue
		}
		f := FileDiff{Path: p, Status: "modified", Sections: diffManifests(a, b)}
		switch {
		case !inBefore:
			f.Status = "added"
		case !inAfter:
			f.Status = "removed"
		}
		d.Files = append(d.Files, f)
	}
	return d, nil
}

// defaultBase is origin/main when the repository has it, as it is
// fresher than a local main that hasn't been pulled, and main otherwise.
func defaultBase() string {
	if _, err := revParse("origin/main"); err == nil {
		return "origin/main"
	}
	return "main"
}

func git(args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}
	return out.String(), nil
}

func revParse(rev string) (string, error) {
	out, err := git("rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("unknown revision %q", rev)
	}
	return strings.TrimSpace(out), nil
}

func isManifest(path string) bool {
	return path == manifestName || strings.HasSuffix(path, "/"+manifestName)
}

// manifestsAt reads every loom.manifest in a commit, by path from the
// repository root.
func manifestsAt(top, rev string) (map[string]string, error) {
	list, err := git("-C", top, "ls-tree", "-r", "--name-only", rev)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, p := range strings.Split(list, "\n") {
		if !isManifest(p) {
			continue
		}
		text, err := git("-C", top, "show", rev+":"+p)
		if err != nil {
			return nil, err
		}
		out[p] = text
	}
	return out, nil
}

// workingManifests reads the loom.manifest files in the working tree
// that git tracks or would add.
func workingManifests(top string) (map[string]string, error) {
	list, err := git("-C", top, "ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, p := range strings.Split(list, "\n") {
		if !isManifest(p) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(top, p))
		if errors.Is(err, os.ErrNotExist) {
			continue // deleted, not yet staged
		}
		if err != nil {
			return nil, err
		}
		out[p] = string(b)
	}
	return out, nil
}

// A manifest is sections (lines at the left margin), holding items
// (indented two spaces), each with detail lines indented further.
type section struct {
	name  string
	items []item
}

type item struct {
	head  string
	lines []string // details, with their indentation relative to the item
}

func parseManifest(text string) []section {
	var out []section
	for _, l := range strings.Split(text, "\n") {
		switch {
		case strings.TrimSpace(l) == "", strings.HasPrefix(l, "#"):
		case !strings.HasPrefix(l, " "):
			out = append(out, section{name: l})
		case len(out) == 0:
		case strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   "):
			s := &out[len(out)-1]
			s.items = append(s.items, item{head: strings.TrimSpace(l)})
		default:
			s := &out[len(out)-1]
			if len(s.items) > 0 {
				it := &s.items[len(s.items)-1]
				it.lines = append(it.lines, strings.TrimPrefix(l, "    "))
			}
		}
	}
	return out
}

// key identifies an item across revisions: a route's path, a
// migration's file, a job's type. A flag is its
// whole line, as several flags name the same subject.
func key(sec, head string) string {
	if sec == "for review" {
		return head
	}
	end := len(head)
	for _, sep := range []string{"  ", ": "} {
		if i := strings.Index(head, sep); i >= 0 && i < end {
			end = i
		}
	}
	return head[:end]
}

func diffManifests(a, b string) []SectionDiff {
	before, after := parseManifest(a), parseManifest(b)
	var names []string
	seen := map[string]bool{}
	for _, s := range append(append([]section(nil), after...), before...) {
		if !seen[s.name] {
			seen[s.name] = true
			names = append(names, s.name)
		}
	}
	find := func(list []section, name string) []item {
		for _, s := range list {
			if s.name == name {
				return s.items
			}
		}
		return nil
	}
	out := []SectionDiff{}
	for _, name := range names {
		old, cur := find(before, name), find(after, name)
		sd := SectionDiff{Section: name, Added: []ItemChange{}, Removed: []ItemChange{}, Changed: []ItemChange{}}
		oldBy := map[string]item{}
		for _, it := range old {
			oldBy[key(name, it.head)] = it
		}
		curKeys := map[string]bool{}
		for _, it := range cur {
			k := key(name, it.head)
			curKeys[k] = true
			was, ok := oldBy[k]
			if !ok {
				sd.Added = append(sd.Added, ItemChange{Item: it.head, Lines: it.lines})
				continue
			}
			removed, added := lineChanges(was.lines, it.lines)
			if was.head == it.head && len(removed) == 0 && len(added) == 0 {
				continue
			}
			c := ItemChange{Item: it.head, Removed: removed, Added: added}
			if was.head != it.head {
				c.Was = was.head
			}
			sd.Changed = append(sd.Changed, c)
		}
		for _, it := range old {
			if !curKeys[key(name, it.head)] {
				sd.Removed = append(sd.Removed, ItemChange{Item: it.head, Lines: it.lines})
			}
		}
		if len(sd.Added)+len(sd.Removed)+len(sd.Changed) > 0 {
			out = append(out, sd)
		}
	}
	return out
}

// lineChanges returns the lines only in a and only in b, in order, by a
// longest common subsequence.
func lineChanges(a, b []string) (removed, added []string) {
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			i, j = i+1, j+1
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			removed = append(removed, a[i])
			i++
		default:
			added = append(added, b[j])
			j++
		}
	}
	return removed, added
}

func printDiff(w io.Writer, d *ManifestDiff) {
	head := "the working tree"
	if d.Head != "" {
		head = short(d.Head)
	}
	if len(d.Files) == 0 {
		fmt.Fprintf(w, "no change to the app's behaviour from %s to %s\n", short(d.Base), head)
		return
	}
	fmt.Fprintf(w, "changes from %s to %s\n", short(d.Base), head)
	for _, f := range d.Files {
		fmt.Fprintf(w, "\n%s (%s)\n", f.Path, f.Status)
		for _, s := range f.Sections {
			fmt.Fprintf(w, "\n  %s\n", s.Section)
			for _, c := range s.Added {
				fmt.Fprintf(w, "    + %s\n", c.Item)
				for _, l := range c.Lines {
					fmt.Fprintf(w, "        %s\n", l)
				}
			}
			for _, c := range s.Removed {
				fmt.Fprintf(w, "    - %s\n", c.Item)
			}
			for _, c := range s.Changed {
				fmt.Fprintf(w, "    ~ %s\n", c.Item)
				if c.Was != "" {
					fmt.Fprintf(w, "        was: %s\n", c.Was)
				}
				for _, l := range c.Removed {
					fmt.Fprintf(w, "        - %s\n", strings.TrimSpace(l))
				}
				for _, l := range c.Added {
					fmt.Fprintf(w, "        + %s\n", strings.TrimSpace(l))
				}
			}
		}
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
