package docs

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The index in docs.go and the docs list in llms.txt must agree: the same
// documents, in the same order, with the same summaries.
func TestIndexMatchesLLMsTxt(t *testing.T) {
	b, err := os.ReadFile("../llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`^- \[docs/([a-z-]+)\.md\]\(docs/[a-z-]+\.md\): (.*)$`)
	var want []string
	for _, l := range strings.Split(string(b), "\n") {
		if m := line.FindStringSubmatch(l); m != nil {
			want = append(want, m[1]+": "+m[2])
		}
	}
	var got []string
	for _, e := range index {
		got = append(got, e.name+": "+e.summary)
	}
	if !slices.Equal(got, want) {
		t.Errorf("docs.go's index and llms.txt differ\n index:\n  %s\n llms.txt:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// Every embedded document is in the index, and every topic has a title.
func TestEveryFileIsATopic(t *testing.T) {
	var names []string
	for _, tp := range Topics() {
		if tp.Title == "" {
			t.Errorf("%s.md has no # heading (or doesn't exist)", tp.Name)
		}
		names = append(names, tp.Name)
	}
	slices.Sort(names)
	if files := Names(); !slices.Equal(names, files) {
		t.Errorf("topics %v, files %v", names, files)
	}
}

func TestLookup(t *testing.T) {
	tp, md, err := Lookup("Syntax.md")
	if err != nil || tp.Name != "syntax" || tp.Title != "Template syntax" || !strings.HasPrefix(md, "# Template syntax") {
		t.Fatalf("Lookup(syntax) = %+v, %.20q, %v", tp, md, err)
	}
	if _, _, err := Lookup("nope"); !errors.Is(err, ErrUnknown) || !strings.Contains(err.Error(), "syntax") {
		t.Fatalf("Lookup(nope) error = %v", err)
	}
}
