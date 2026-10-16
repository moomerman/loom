// Package render defines the output of a compiled template: a tree of
// static strings and dynamic values.
//
// Every template compiles to a Rendered: a fixed list of static strings
// (Statics) with dynamic values (Dynamics) between them. Statics never
// change between renders of the same template, so a live page sends them
// once; afterwards only changed dynamics travel (see the diff package).
//
// Generated code builds these trees; most apps never touch them directly.
package render

import (
	"fmt"
	"html"
	"reflect"
	"strconv"
	"strings"
)

// Statics are the fixed parts of a template, between its dynamic values.
// Generated code declares each Statics once as a package-level variable,
// so two Rendered values come from the same template exactly when their
// Statics pointers are equal.
type Statics struct {
	ID    string   // stable identifier, e.g. "TodoList.0"; used on the wire
	Parts []string // len(Parts) == number of dynamics + 1
}

// Dynamic is a dynamic value: Text, *Rendered or *List.
type Dynamic interface{ dynamic() }

// Text is HTML that is safe to insert as is: escaped text, a whole
// attribute like ` class="done"`, or raw HTML from the raw helper.
type Text string

// Rendered is a template, or a block of one (an :if, a component call, a
// slot), rendered: its statics interleaved with dynamic values.
type Rendered struct {
	Statics  *Statics
	Dynamics []Dynamic
}

// List is a rendered :for comprehension. Every item shares the same
// statics; each has its own dynamics and a key that identifies it across
// renders.
type List struct {
	Statics *Statics
	Items   []Item
}

// Item is one entry of a List.
type Item struct {
	Key      string
	Dynamics []Dynamic
}

func (Text) dynamic()      {}
func (*Rendered) dynamic() {}
func (*List) dynamic()     {}

// HTML returns the rendered HTML.
func (r *Rendered) HTML() string {
	var b strings.Builder
	r.WriteHTML(&b)
	return b.String()
}

// WriteHTML writes the rendered HTML to b.
func (r *Rendered) WriteHTML(b *strings.Builder) {
	writeParts(b, r.Statics, r.Dynamics)
}

func writeParts(b *strings.Builder, s *Statics, dyns []Dynamic) {
	for i, part := range s.Parts {
		b.WriteString(part)
		if i < len(dyns) {
			writeDynamic(b, dyns[i])
		}
	}
}

func writeDynamic(b *strings.Builder, d Dynamic) {
	switch d := d.(type) {
	case Text:
		b.WriteString(string(d))
	case *Rendered:
		if d != nil {
			d.WriteHTML(b)
		}
	case *List:
		for _, it := range d.Items {
			writeParts(b, d.Statics, it.Dynamics)
		}
	}
}

// Component is a compiled template component with assigns of type A.
// Generated code declares one per `component` in a .hx file:
//
//	var TodoList = render.Register(render.Component[TodoListAssigns]{Name: "TodoList", Package: "templates", Render: renderTodoList})
type Component[A any] struct {
	Name    string
	Package string // the Go package name
	// RootLayout is set for a root layout: a component with a whole
	// HTML document, which pages are served in (see docs/layouts.md).
	RootLayout bool
	// Render renders the component in a scope, which may be nil.
	Render func(*Scope, A) *Rendered
	// Assets are what the component needs on the page besides its
	// HTML, or nil if it needs nothing.
	Assets *Assets
	// Previews are the samples the preview gallery renders, from the
	// parameters' enum and bool values and @preview sample values (see
	// docs/components.md). NoPreview says why there are none.
	Previews  []Preview[A]
	NoPreview string
	// Events are the server events the component's markup sends by name,
	// its own and those of the components it calls, so tools can list
	// what a page sends (see App.Manifest). An event a caller chooses,
	// through an event parameter, belongs to the caller.
	Events []EventUse
}

// EventUse is one place a template sends a server event by name:
// `<button on:click="delete" val-id={t.id}>` is {Name: "delete", On:
// "click", Vals: ["id"]}.
type EventUse struct {
	Name string   // the event, e.g. "delete"
	On   string   // the DOM or behaviour event that sends it, e.g. "click"
	Vals []string // the val-* names sent with it, e.g. "id"
	At   string   // where, e.g. "todo_app.hx:26:9"
}

// Preview is a sample of a component for the preview gallery.
type Preview[A any] struct {
	// Name lists the values that differ from the first sample, e.g.
	// "variant=danger size=sm busy", or is "default" for the first.
	Name    string
	Assigns A
}

// Sample is a Preview with its assigns type erased.
type Sample struct {
	Name    string
	Assigns any // the component's assigns struct
	Render  func(*Scope) *Rendered
}

// RenderHTML renders the component to an HTML string, as if mounted at
// the root (a nil Scope).
func (c Component[A]) RenderHTML(assigns A) string {
	return c.Render(nil, assigns).HTML()
}

// Slot is markup passed to a component, written <:name>...</:name> for a
// named slot or as the call's content for the default slot (inner_block).
// A nil Slot renders nothing.
type Slot func() *Rendered

// Empty is the dynamic for something that renders nothing, such as an
// :if whose condition is false.
const Empty = Text("")

// Escape escapes s for use in HTML text or a quoted attribute value.
func Escape(s string) Text { return Text(html.EscapeString(s)) }

// Int renders an integer as text.
func Int(n int) Text { return Text(strconv.Itoa(n)) }

// Float renders a float as text, in the shortest form that round-trips.
func Float(f float64) Text { return Text(strconv.FormatFloat(f, 'g', -1, 64)) }

// Bool renders a bool as text: "true" or "false".
func Bool(v bool) Text { return Text(strconv.FormatBool(v)) }

// Attr renders a dynamic attribute with a text value: ` name="value"`.
func Attr(name string, value Text) Text {
	return Text(" " + name + `="` + string(value) + `"`)
}

// BoolAttr renders a boolean attribute: ` name` when on, nothing when off.
func BoolAttr(name string, on bool) Text {
	if on {
		return Text(" " + name)
	}
	return Empty
}

// RenderSlot renders a slot, or nothing for a nil slot.
func RenderSlot(s Slot) Dynamic {
	if s == nil {
		return Empty
	}
	if r := s(); r != nil {
		return r
	}
	return Empty
}

// LiveTitle renders a root layout's <.live_title>: a <title> holding
// prefix, then content (or def when content is empty), then suffix. The
// parts are kept in data attributes, so the client can rebuild the title
// when the page sets a new one (see docs/layouts.md).
func LiveTitle(prefix, def, suffix string, content Slot) Text {
	var b strings.Builder
	b.WriteString("<title")
	for _, a := range [][2]string{{"data-prefix", prefix}, {"data-default", def}, {"data-suffix", suffix}} {
		if a[1] != "" {
			b.WriteString(string(Attr(a[0], Escape(a[1]))))
		}
	}
	b.WriteString(">")
	b.WriteString(string(Escape(prefix)))
	title := ""
	if content != nil {
		if r := content(); r != nil {
			title = strings.TrimSpace(r.HTML())
		}
	}
	if title == "" {
		title = string(Escape(def))
	}
	b.WriteString(title)
	b.WriteString(string(Escape(suffix)))
	b.WriteString("</title>")
	return Text(b.String())
}

// IntKey converts an integer :key, or a list index, to an item key.
func IntKey(n int) string { return strconv.Itoa(n) }

// Template is a component with its assigns type erased, so the runtime can
// hold components of different types. Every Component is a Template.
type Template interface {
	ComponentName() string
	PackageName() string
	// RenderJSON renders the component with assigns decoded from JSON
	// with template field names (see DecodeAssigns).
	RenderJSON(data []byte) (*Rendered, error)
	// RenderAssigns renders the component in a scope (nil for the root).
	// It fails if assigns is not the component's assigns type.
	RenderAssigns(s *Scope, assigns any) (*Rendered, error)
	// AssignsType returns the component's assigns struct type.
	AssignsType() reflect.Type
	// IsRootLayout reports whether the component is a root layout.
	IsRootLayout() bool
	// ComponentAssets returns the component's assets, or nil.
	ComponentAssets() *Assets
	// ComponentPreviews returns the component's preview samples, or
	// none and the reason why.
	ComponentPreviews() ([]Sample, string)
	// ComponentEvents returns the events the component sends by name.
	ComponentEvents() []EventUse
}

// ComponentName returns the component's name.
func (c Component[A]) ComponentName() string { return c.Name }

// PackageName returns the name of the Go package declaring the component.
func (c Component[A]) PackageName() string { return c.Package }

// AssignsType returns A, the component's assigns struct type.
func (c Component[A]) AssignsType() reflect.Type { return reflect.TypeOf((*A)(nil)).Elem() }

// IsRootLayout reports whether the component is a root layout.
func (c Component[A]) IsRootLayout() bool { return c.RootLayout }

// ComponentAssets returns the component's assets, or nil.
func (c Component[A]) ComponentAssets() *Assets { return c.Assets }

// ComponentEvents returns the events the component sends by name.
func (c Component[A]) ComponentEvents() []EventUse { return c.Events }

// ComponentPreviews returns the component's preview samples, or none and
// the reason why.
func (c Component[A]) ComponentPreviews() ([]Sample, string) {
	var out []Sample
	for _, p := range c.Previews {
		a := p.Assigns
		out = append(out, Sample{Name: p.Name, Assigns: a, Render: func(s *Scope) *Rendered { return c.Render(s, a) }})
	}
	return out, c.NoPreview
}

// SampleSlot is a slot holding markup written in a template's @preview,
// for the preview gallery. The markup is the template author's, never
// data, so it isn't escaped.
func SampleSlot(markup string) Slot {
	return func() *Rendered { return &Rendered{Statics: &Statics{Parts: []string{markup}}} }
}

// RenderJSON decodes assigns from JSON and renders the component.
func (c Component[A]) RenderJSON(data []byte) (*Rendered, error) {
	var a A
	if err := DecodeAssigns(data, &a); err != nil {
		return nil, err
	}
	return c.Render(nil, a), nil
}

// RenderAssigns renders the component with assigns, which must be of type A.
func (c Component[A]) RenderAssigns(s *Scope, assigns any) (*Rendered, error) {
	a, ok := assigns.(A)
	if !ok {
		var want A
		return nil, &AssignsError{Component: c.Name, Got: fmt.Sprintf("%T", assigns), Want: fmt.Sprintf("%T", want)}
	}
	return c.Render(s, a), nil
}

// AssignsError reports assigns of the wrong type for a component.
type AssignsError struct {
	Component string
	Got, Want string // Go type names
}

func (e *AssignsError) Error() string {
	return fmt.Sprintf("component %s needs assigns of type %s, but got %s", e.Component, e.Want, e.Got)
}
