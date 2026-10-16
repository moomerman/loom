package loom

import (
	"os"
	"regexp"
	"testing"
)

// TestCodesDocumented keeps docs/errors.md in sync with the RT codes.
func TestCodesDocumented(t *testing.T) {
	data, err := os.ReadFile("docs/errors.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^### (RT\d{3})$`).FindAllStringSubmatch(string(data), -1) {
		documented[m[1]] = true
	}
	src, err := os.ReadFile("errors.go")
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\t(RT\d{3}) = "RT\d{3}"`).FindAllStringSubmatch(string(src), -1) {
		known[m[1]] = true
		if !documented[m[1]] {
			t.Errorf("%s is not documented in docs/errors.md", m[1])
		}
	}
	if len(known) == 0 {
		t.Fatal("found no RT codes in errors.go")
	}
	for c := range documented {
		if !known[c] {
			t.Errorf("docs/errors.md documents %s, which errors.go doesn't define", c)
		}
	}
}

// A panic with a Loom error keeps that error's code and fix rather than
// wrapping it in RT402, whose fix would mislead.
func TestPanicErrorKeepsLoomErrors(t *testing.T) {
	rt := &Error{Code: RT401, Msg: "wrong assigns", Fix: "return the right type"}
	if e := panicError("Mount", rt); e != rt {
		t.Errorf("got %+v, want the RT401 error as is", e)
	}
	if e := panicError("Mount", "boom"); e.Code != RT402 {
		t.Errorf("got %+v, want RT402", e)
	}
}
