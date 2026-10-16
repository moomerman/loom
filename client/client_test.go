package client

import (
	"regexp"
	"testing"
)

// Design rule 6 caps loom.js at 8 KB minified. This approximates minification
// by removing comments and collapsing whitespace, which overstates the
// size a real minifier would produce.
func TestSize(t *testing.T) {
	s := regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAll(JS, nil)
	s = regexp.MustCompile(`\s+`).ReplaceAll(s, []byte(" "))
	s = regexp.MustCompile(` ?([{}()\[\];,:=+<>!&|?]) ?`).ReplaceAll(s, []byte("$1"))
	t.Logf("loom.js: %d bytes, about %d stripped", len(JS), len(s))
	if len(s) > 8*1024 {
		t.Errorf("loom.js is about %d bytes stripped; the budget is 8192", len(s))
	}
}
