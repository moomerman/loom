package render

import "strings"

// Event is the value of a component's event parameter (`on:click event`):
// the server event its caller named, and the val-* values the caller
// passed with it. The zero Event means the caller gave no handler.
type Event struct {
	Name  string // e.g. "delete"; "" for none
	Vals  []Val  // sent as the event's payload
	Hints []Val  // optimistic:* hints: "class" with class names, or "disabled" or "hidden" with ""
}

// Val is one val-* value sent with an event: name "id" for val-id.
type Val struct {
	Name  string
	Value Text // escaped
}

// With returns the event with more values, for a component that forwards
// its own event parameter and adds val-* attributes.
func (e Event) With(vals ...Val) Event {
	if len(vals) == 0 {
		return e
	}
	e.Vals = append(append([]Val(nil), e.Vals...), vals...)
	return e
}

// Hint returns the event with more optimistic:* hints, for a component
// that forwards its own event parameter and adds hints.
func (e Event) Hint(hints ...Val) Event {
	if len(hints) == 0 {
		return e
	}
	e.Hints = append(append([]Val(nil), e.Hints...), hints...)
	return e
}

// EventAttrs renders a forwarded event, `<button on:click>`, as the
// attributes the client reads: ` on:click="delete" val-id="3"
// optimistic:disabled`, or nothing if the caller gave no handler.
func EventAttrs(name string, e Event) Text {
	if e.Name == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(" " + name + `="` + string(Escape(e.Name)) + `"`)
	for _, v := range e.Vals {
		b.WriteString(" val-" + v.Name + `="` + string(v.Value) + `"`)
	}
	for _, h := range e.Hints {
		b.WriteString(" optimistic:" + h.Name)
		if h.Value != "" {
			b.WriteString(`="` + string(h.Value) + `"`)
		}
	}
	return Text(b.String())
}

// Classes joins class names with spaces, leaving out empty ones:
// Classes("btn", "", "sm") is "btn sm".
func Classes(names ...string) string {
	var parts []string
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, " ")
}
