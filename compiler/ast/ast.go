// Package ast defines the syntax tree for .hx templates.
//
// The tree is deliberately target-neutral: it says nothing about Go. The
// same tree will later be compiled to JavaScript for client-side rendering,
// so expressions are a small, pure language of their own (see Expr), and
// types are written as plain names (see Type) rather than Go type syntax.
package ast

import "github.com/moomerman/loom/compiler/source"

// File is a parsed .hx file. A file holds imports, then components and
// behaviors.
type File struct {
	Source     *source.File
	Imports    []*Import
	Components []*Component
	Behaviors  []*Behavior
}

// Import is an `import "path"` line: the components of the template
// package at that import path can be called from this file, by name.
type Import struct {
	Pos  source.Pos
	Path string
}

// Component is a `component Name(params)` declaration and its body.
type Component struct {
	Pos    source.Pos
	Name   string
	Params []*Param
	Body   []Node
	// Styles are the component's <style> blocks: <style> elements at the
	// top level of its markup, taken out of Body. Their CSS is scoped to
	// the component and served in a stylesheet, not inline.
	Styles []*Element
}

// Behavior is a `behavior name(params) emits (...) requires [...]`
// declaration: client-side code, in an ES module next to the template
// (name.js), that elements attach with use:name={...}. Its params are the
// props the module receives; its emits are the events it may send, which
// the element binds with on:<emit>.
type Behavior struct {
	Pos      source.Pos
	Name     string // snake_case, as in use:name
	Params   []*Param
	Emits    []*Emit
	Requires []*Require // what the element must have, checked statically or in dev
}

// Emit is an event a behavior may send, with its payload: `select(value string)`.
type Emit struct {
	Pos    source.Pos
	Name   string
	Params []*Param
}

// Require is one `[attr]` or `[attr=value]` of a behavior's requires list.
type Require struct {
	Pos      source.Pos
	Attr     string
	Value    string
	HasValue bool
}

// Selector renders the requirement as a CSS attribute selector.
func (r *Require) Selector() string {
	if !r.HasValue {
		return "[" + r.Attr + "]"
	}
	return "[" + r.Attr + `="` + r.Value + `"]`
}

// Use is an element's `use:name={prop: value, ...}`: the behavior it
// attaches and the props it passes.
type Use struct {
	Pos   source.Pos
	Name  string
	Props []*Prop
}

// Prop is one `name: value` of a use:.
type Prop struct {
	Pos  source.Pos
	Name string
	X    Expr
}

// Param is one typed parameter in a component signature: `label string`,
// `busy bool = false`, `size: :sm | :md = :md` or `on:click event`.
type Param struct {
	Pos  source.Pos
	Name string // "on:click" for an event parameter
	Type Type
	// Default is the value used when a call leaves the parameter out: a
	// literal, or an Atom for an enum. Nil if the parameter is required.
	Default Expr
	// Previews are sample values for the preview gallery, from
	// @preview("Save", ...): literals, or text for a slot.
	Previews []Expr
}

// Type is a parameter type: a NamedType, a ListType or an EnumType.
type Type interface {
	Position() source.Pos
	typ()
}

// NamedType is a type referred to by name, e.g. `string`, `Todo`, `slot`,
// or with a type argument, `form[SettleInput]`.
type NamedType struct {
	Pos  source.Pos
	Name string
	Arg  Type // the type in brackets, or nil
}

// ListType is a list of another type, written `[]T`.
type ListType struct {
	Pos  source.Pos
	Elem Type
}

// EnumType is one of a fixed set of values, written `:primary | :secondary`.
type EnumType struct {
	Pos    source.Pos
	Values []string // without the leading colons
}

func (t *NamedType) Position() source.Pos { return t.Pos }
func (t *ListType) Position() source.Pos  { return t.Pos }
func (t *EnumType) Position() source.Pos  { return t.Pos }
func (*NamedType) typ()                   {}
func (*ListType) typ()                    {}
func (*EnumType) typ()                    {}

// Node is a node in a component body.
type Node interface {
	Position() source.Pos
	node()
}

// Text is literal markup, including whitespace and HTML comments.
type Text struct {
	Pos   source.Pos
	Value string
}

// Interp is a `{expr}` dynamic slot in text.
type Interp struct {
	Pos source.Pos
	X   Expr
}

// Directives are the `:for`, `:if` and `key` attributes, which control
// whether and how often a tag renders rather than becoming HTML attributes.
type Directives struct {
	For *For
	If  Expr
	Key Expr
}

// For is a `:for={x <- list}` comprehension.
type For struct {
	Pos    source.Pos
	Var    string
	VarPos source.Pos
	List   Expr
}

// Element is an HTML element, e.g. `<li class="x">...</li>`.
type Element struct {
	Pos      source.Pos
	Tag      string
	Attrs    []*Attr
	Children []Node
	Void     bool // a void element like <input>, which has no children or end tag
	Use      *Use // use:name={...}, or nil
	Directives
}

// ComponentCall is a call to another component, e.g. `<.footer count={n}>`.
// Its children, minus any named slots, form the default slot.
type ComponentCall struct {
	Pos      source.Pos
	Name     string // as written, without the leading dot
	Attrs    []*Attr
	Children []Node
	Slots    []*Slot
	Directives
}

// Slot is a named slot passed to a component call, e.g. `<:left>Items</:left>`.
type Slot struct {
	Pos      source.Pos
	Name     string // without the leading colon
	Attrs    []*Attr
	Children []Node
}

func (n *Text) Position() source.Pos          { return n.Pos }
func (n *Interp) Position() source.Pos        { return n.Pos }
func (n *Element) Position() source.Pos       { return n.Pos }
func (n *ComponentCall) Position() source.Pos { return n.Pos }
func (n *Slot) Position() source.Pos          { return n.Pos }
func (*Text) node()                           {}
func (*Interp) node()                         {}
func (*Element) node()                        {}
func (*ComponentCall) node()                  {}
func (*Slot) node()                           {}

// AttrKind says how an attribute's value was written.
type AttrKind int

const (
	AttrBare   AttrKind = iota // `disabled`
	AttrString                 // `type="checkbox"`; never interpolated
	AttrExpr                   // `checked={t.done}`
)

// Attr is an attribute on an element, component call or slot. Event
// bindings (`on:change="toggle"`) and payload values (`val-id={t.id}`) are
// ordinary attributes at this level.
type Attr struct {
	Pos   source.Pos
	Name  string
	Kind  AttrKind
	Value string // for AttrString
	X     Expr   // for AttrExpr
}

// Expr is an expression in the template expression language: literals,
// identifiers, field access, indexing, unary and binary operators, inline
// if, and calls to registered helpers. Nothing else.
type Expr interface {
	Position() source.Pos
	expr()
}

type (
	Ident struct {
		Pos  source.Pos
		Name string
	}
	IntLit struct {
		Pos   source.Pos
		Value int64
	}
	FloatLit struct {
		Pos   source.Pos
		Value float64
	}
	StringLit struct {
		Pos   source.Pos
		Value string
	}
	BoolLit struct {
		Pos   source.Pos
		Value bool
	}
	// Field is `x.name`.
	Field struct {
		Pos  source.Pos
		X    Expr
		Name string
	}
	// Index is `x[i]`.
	Index struct {
		Pos   source.Pos
		X     Expr
		Index Expr
	}
	// Call is a call to a registered helper, `name(args...)`. The callee
	// is always a plain name; templates cannot call methods or values.
	Call struct {
		Pos  source.Pos
		Func *Ident
		Args []Expr
	}
	// Unary is `!x` or `-x`.
	Unary struct {
		Pos source.Pos
		Op  string
		X   Expr
	}
	// Binary is `x op y` for op in || && == != < <= > >= + - * / %.
	Binary struct {
		Pos source.Pos
		Op  string
		X   Expr
		Y   Expr
	}
	// Cond is the inline `if cond, then, else`. In a class list, the else
	// part may be left out (Else is nil): `class={if busy, "busy"}`.
	Cond struct {
		Pos  source.Pos
		Cond Expr
		Then Expr
		Else Expr
	}
	// ClassList is a class attribute listing several values separated by
	// spaces, `class={"btn" variant size if busy, "busy"}`. Empty values
	// are left out.
	ClassList struct {
		Pos   source.Pos
		Items []Expr
	}
	// Atom is an enum value in a signature, `:primary`.
	Atom struct {
		Pos  source.Pos
		Name string // without the colon
	}
)

func (e *Ident) Position() source.Pos     { return e.Pos }
func (e *IntLit) Position() source.Pos    { return e.Pos }
func (e *FloatLit) Position() source.Pos  { return e.Pos }
func (e *StringLit) Position() source.Pos { return e.Pos }
func (e *BoolLit) Position() source.Pos   { return e.Pos }
func (e *Field) Position() source.Pos     { return e.Pos }
func (e *Index) Position() source.Pos     { return e.Pos }
func (e *Call) Position() source.Pos      { return e.Pos }
func (e *Unary) Position() source.Pos     { return e.Pos }
func (e *Binary) Position() source.Pos    { return e.Pos }
func (e *Cond) Position() source.Pos      { return e.Pos }
func (e *ClassList) Position() source.Pos { return e.Pos }
func (e *Atom) Position() source.Pos      { return e.Pos }
func (*Ident) expr()                      {}
func (*IntLit) expr()                     {}
func (*FloatLit) expr()                   {}
func (*StringLit) expr()                  {}
func (*BoolLit) expr()                    {}
func (*Field) expr()                      {}
func (*Index) expr()                      {}
func (*Call) expr()                       {}
func (*Unary) expr()                      {}
func (*Binary) expr()                     {}
func (*Cond) expr()                       {}
func (*ClassList) expr()                  {}
func (*Atom) expr()                       {}
