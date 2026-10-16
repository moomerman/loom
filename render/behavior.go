package render

import (
	"encoding/json"
	"strings"
	"sync"
)

// Behavior is a behavior's client code, declared by generated code: an ES
// module exporting mount(el, props, emit), attached to elements with
// use:name (docs/components.md).
type Behavior struct {
	ID    string   // unique across packages, e.g. "dropdown-3fa2c1"
	Name  string   // as in use:name
	Emits []string // the events it may send
	JS    string   // the module's source
}

var (
	behaviorsMu sync.Mutex
	behaviors   = map[string]*Behavior{}
)

// RegisterBehavior records a behavior, so tools such as loomtest can find
// it by ID. Generated code calls it for every behavior declared.
func RegisterBehavior(b *Behavior) *Behavior {
	behaviorsMu.Lock()
	defer behaviorsMu.Unlock()
	behaviors[b.ID] = b
	return b
}

// LookupBehavior returns the registered behavior with the given ID.
func LookupBehavior(id string) (*Behavior, bool) {
	behaviorsMu.Lock()
	defer behaviorsMu.Unlock()
	b, ok := behaviors[id]
	return b, ok
}

// Use renders the attributes that attach a behavior to an element: its ID,
// its props as JSON, and the requirements the compiler couldn't check
// (attributes with dynamic values) as a CSS selector, which the browser
// checks in dev.
func Use(id string, props map[string]any, requires string) Text {
	var b strings.Builder
	b.WriteString(` data-loom-use="` + id + `"`)
	if props != nil {
		data, err := json.Marshal(props)
		if err != nil {
			panic("render.Use: " + err.Error()) // props are typed scalars and lists; this can't fail
		}
		b.WriteString(` data-loom-props="` + string(Escape(string(data))) + `"`)
	}
	if requires != "" {
		b.WriteString(` data-loom-requires="` + string(Escape(requires)) + `"`)
	}
	return Text(b.String())
}

// Behaviors returns the behaviors pages that render the given components
// can attach, each once, in the order they are reached.
func Behaviors(tmpls ...Template) []*Behavior {
	seen := map[*Assets]bool{}
	got := map[*Behavior]bool{}
	var out []*Behavior
	var visit func(t Template)
	visit = func(t Template) {
		a := t.ComponentAssets()
		if a == nil || seen[a] {
			return
		}
		seen[a] = true
		for _, b := range a.Behaviors {
			if !got[b] {
				got[b] = true
				out = append(out, b)
			}
		}
		for _, u := range a.Uses {
			visit(u)
		}
	}
	for _, t := range tmpls {
		if t != nil {
			visit(t)
		}
	}
	return out
}
