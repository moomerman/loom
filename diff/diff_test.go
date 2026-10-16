package diff

import (
	"encoding/json"
	"testing"

	"github.com/moomerman/loom/internal/difftest"
	"github.com/moomerman/loom/render"
)

// roundTrip encodes v as JSON and decodes it, as the client receives it.
func roundTrip(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func mustJSON(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// checkApply checks the core property: applying Diff(old, new) to the
// client's copy of old yields new, in tree and in HTML.
func checkApply(t *testing.T, old, new *render.Rendered) map[string]any {
	t.Helper()
	patch, err := Diff(old, new)
	if err != nil {
		t.Fatal(err)
	}
	client := roundTrip(t, Full(old))
	got, err := Apply(client, roundTrip(t, patch))
	if err != nil {
		t.Fatalf("apply failed: %v\nold:   %s\npatch: %s", err, mustJSON(t, Full(old)), mustJSON(t, patch))
	}
	if g, w := mustJSON(t, got), mustJSON(t, Full(new)); g != w {
		t.Fatalf("applying the diff doesn't yield the new tree\nold:   %s\npatch: %s\ngot:   %s\nwant:  %s", mustJSON(t, Full(old)), mustJSON(t, patch), g, w)
	}
	if g, w := HTML(got), new.HTML(); g != w {
		t.Fatalf("HTML differs:\ngot:  %s\nwant: %s", g, w)
	}
	return patch
}

func TestApplyDiffYieldsNewTree(t *testing.T) {
	for _, p := range difftest.Pairs(1, 3000) {
		old, new := p[0], p[1]
		checkApply(t, old, new)
		checkApply(t, new, old)
		if p, _ := Diff(old, old); p != nil {
			t.Fatalf("diff of a tree with itself is not empty: %s", mustJSON(t, p))
		}
	}
}

// --- Examples of the wire format, which docs/wire.md describes ---

var (
	page   = &render.Statics{ID: "P.0", Parts: []string{"<ul>", "</ul><p>", "</p>"}}
	item   = &render.Statics{ID: "P.1", Parts: []string{"<li>", "</li>"}}
	banner = &render.Statics{ID: "P.2", Parts: []string{"<b>", "</b>"}}
)

func todo(keyTitles ...string) *render.Rendered {
	l := &render.List{Statics: item}
	for i := 0; i < len(keyTitles); i += 2 {
		l.Items = append(l.Items, render.Item{Key: keyTitles[i], Dynamics: []render.Dynamic{render.Text(keyTitles[i+1])}})
	}
	return &render.Rendered{Statics: page, Dynamics: []render.Dynamic{l, render.Empty}}
}

func TestWireExamples(t *testing.T) {
	withBanner := todo("a", "Buy milk")
	withBanner.Dynamics[1] = &render.Rendered{Statics: banner, Dynamics: []render.Dynamic{render.Text("hi")}}

	tests := []struct {
		name     string
		old, new *render.Rendered
		want     string
	}{
		{"unchanged", todo("a", "Buy milk"), todo("a", "Buy milk"), `null`},
		{"update item", todo("a", "Buy milk", "b", "Walk dog"), todo("a", "Buy oat milk", "b", "Walk dog"),
			`{"d":{"0":{"k":[["a",{"0":"Buy oat milk"}]]}}}`},
		{"insert", todo("a", "Buy milk"), todo("a", "Buy milk", "b", "Walk dog"),
			`{"d":{"0":{"o":[["a"],["b",["Walk dog"]]]}}}`},
		{"delete", todo("a", "Buy milk", "b", "Walk dog"), todo("b", "Walk dog"),
			`{"d":{"0":{"o":[["b"]]}}}`},
		{"move", todo("a", "Buy milk", "b", "Walk dog"), todo("b", "Walk dog", "a", "Buy milk"),
			`{"d":{"0":{"o":[["b"],["a"]]}}}`},
		{"empty list", todo("a", "Buy milk"), todo(), `{"d":{"0":{"o":[]}}}`},
		{"if becomes true", todo("a", "Buy milk"), withBanner, `{"d":{"1":{"d":["hi"],"s":["<b>","</b>"]}}}`},
		{"if becomes false", withBanner, todo("a", "Buy milk"), `{"d":{"1":""}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			patch := checkApply(t, tc.old, tc.new)
			if got := mustJSON(t, patch); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}

	if got, want := mustJSON(t, Full(todo("a", "Buy milk", "b", "Walk dog"))), `{"d":[{"k":[["a",["Buy milk"]],["b",["Walk dog"]]],"s":["<li>","</li>"]},""],"s":["<ul>","</ul><p>","</p>"]}`; got != want {
		t.Errorf("Full:\ngot  %s\nwant %s", got, want)
	}
}

func TestDuplicateKeys(t *testing.T) {
	_, err := Diff(todo("a", "x"), todo("a", "x", "a", "y"))
	if _, ok := err.(*DuplicateKeyError); !ok {
		t.Fatalf("got %v, want a DuplicateKeyError", err)
	}
}
