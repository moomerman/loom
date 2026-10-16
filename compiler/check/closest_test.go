package check

import "testing"

// A name that is a word of a candidate beats a nearer spelling: an agent
// writing due means due_on, not done.
func TestClosest(t *testing.T) {
	for _, tc := range []struct {
		name       string
		candidates []string
		want       string
	}{
		{"due", []string{"id", "done", "due_on"}, "due_on"},
		{"titel", []string{"id", "title", "done"}, "title"},
		{"at", []string{"created_at", "updated_at", "cat"}, "cat"}, // two word matches: spelling decides
		{"zzz", []string{"id", "title"}, ""},
	} {
		if got := closest(tc.name, tc.candidates); got != tc.want {
			t.Errorf("closest(%q, %v) = %q, want %q", tc.name, tc.candidates, got, tc.want)
		}
	}
}
