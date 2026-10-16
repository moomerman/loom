package check

import (
	"strings"

	"github.com/moomerman/loom/render"
)

// Kind is the kind of a template type.
type Kind int

const (
	Invalid Kind = iota // the type of an expression that already failed to check
	String
	Int
	Float
	Bool
	HTML   // markup that is inserted as is: from raw() or render_slot()
	Slot   // markup passed to a component
	List   // []Elem
	Struct // a Go struct type from the template's package
	Enum   // one of a fixed set of strings: `size: :sm | :md`
	Event  // an event parameter, `on:click event`: the caller's handler
	// Form is a form of a Go input struct, `form[SettleInput]`: its
	// fields are FormFields named after the struct's (Struct), and map
	// fields are FormEntries.
	Form
	FormField   // one field of a form, `field`: its name, value and error
	FormEntries // a map field of a form, indexed by key to get a FormField
)

// Type is the type of a template expression or parameter.
//
// Template types are a small, target-neutral set. The Go generator maps
// them to Go types (GoString); a future JavaScript generator will map them
// to JavaScript values.
type Type struct {
	Kind   Kind
	Elem   *Type       // for List
	Struct *StructType // for Struct, and a Form's input
	Enum   *EnumType   // for Enum
}

// EnumType is the type of an enum parameter. Its values are strings at
// run time; in Go it is a named string type with a constant per value.
type EnumType struct {
	GoName string   // e.g. ButtonVariant
	Values []string // in declaration order
}

// Has reports whether v is one of the enum's values.
func (e *EnumType) Has(v string) bool {
	for _, x := range e.Values {
		if x == v {
			return true
		}
	}
	return false
}

// List renders the values as written in a signature: ":sm | :md".
func (e *EnumType) List() string { return ":" + strings.Join(e.Values, " | :") }

// GoConst is the Go constant for value v, e.g. ButtonVariantPrimary.
func (e *EnumType) GoConst(v string) string { return e.GoName + PascalCase(v) }

// StructType is a Go struct as seen from templates.
type StructType struct {
	Name   string
	Fields []*Field
}

// Field is a struct field as seen from templates. Its template name is the
// snake_case form of its Go name: Go `CreatedAt` is `created_at`.
type Field struct {
	Name   string // template name
	GoName string
	Type   *Type  // nil if the Go type has no template equivalent
	GoType string // the Go type as written, for error messages
}

// Basic types, shared by all expressions.
var (
	TInvalid = &Type{Kind: Invalid}
	TString  = &Type{Kind: String}
	TInt     = &Type{Kind: Int}
	TFloat   = &Type{Kind: Float}
	TBool    = &Type{Kind: Bool}
	THTML    = &Type{Kind: HTML}
	TSlot    = &Type{Kind: Slot}
	TEvent   = &Type{Kind: Event}
	TField   = &Type{Kind: FormField}
	TEntries = &Type{Kind: FormEntries}
)

// FormImport is the import path of the Go package of form types.
const FormImport = "github.com/moomerman/loom/form"

// fieldStruct is a form field as templates see it: form.Field.
var fieldStruct = &StructType{Name: "field", Fields: []*Field{
	{Name: "name", GoName: "Name", Type: TString, GoType: "string"},
	{Name: "value", GoName: "Value", Type: TString, GoType: "string"},
	{Name: "error", GoName: "Error", Type: TString, GoType: "string"},
}}

// IsForm reports whether t is or contains a form type, so code using it
// imports the form package.
func IsForm(t *Type) bool {
	switch t.Kind {
	case Form, FormField, FormEntries:
		return true
	case List:
		return IsForm(t.Elem)
	}
	return false
}

// basicTypes maps type names in signatures to types.
var basicTypes = map[string]*Type{
	"string": TString,
	"int":    TInt,
	"float":  TFloat,
	"bool":   TBool,
	"slot":   TSlot,
	"event":  TEvent,
	"field":  TField,
}

// ListOf returns the list type with the given element type.
func ListOf(elem *Type) *Type { return &Type{Kind: List, Elem: elem} }

// String renders the type as written in templates, e.g. "[]Todo".
func (t *Type) String() string {
	switch t.Kind {
	case String:
		return "string"
	case Int:
		return "int"
	case Float:
		return "float"
	case Bool:
		return "bool"
	case HTML:
		return "html"
	case Slot:
		return "slot"
	case List:
		return "[]" + t.Elem.String()
	case Struct:
		return t.Struct.Name
	case Enum:
		return t.Enum.List()
	case Event:
		return "event"
	case Form:
		return "form[" + t.Struct.Name + "]"
	case FormField:
		return "field"
	case FormEntries:
		return "map field"
	}
	return "invalid"
}

// GoString renders the Go type used for t in generated code.
func (t *Type) GoString() string {
	switch t.Kind {
	case Float:
		return "float64"
	case Slot:
		return "render.Slot"
	case HTML:
		return "render.Text"
	case List:
		return "[]" + t.Elem.GoString()
	case Enum:
		return t.Enum.GoName
	case Event:
		return "render.Event"
	case Form:
		return "form.Form[" + t.Struct.Name + "]"
	case FormField:
		return "form.Field"
	case FormEntries:
		return "form.Entries"
	}
	return t.String()
}

// Identical reports whether a and b are the same type. An invalid type is
// identical to everything, so one error doesn't cause a cascade of others.
func Identical(a, b *Type) bool {
	if a.Kind == Invalid || b.Kind == Invalid {
		return true
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case List:
		return Identical(a.Elem, b.Elem)
	case Struct, Form:
		return a.Struct == b.Struct
	case Enum:
		return a.Enum == b.Enum
	}
	return true
}

// IsText reports whether values of t render as text: strings and enums.
func IsText(t *Type) bool { return t.Kind == String || t.Kind == Enum || t.Kind == Invalid }

// Field returns the field with the given template name, or nil.
func (s *StructType) Field(name string) *Field {
	for _, f := range s.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// FieldNames lists the template names of the fields, for error messages.
func (s *StructType) FieldNames() []string {
	var names []string
	for _, f := range s.Fields {
		names = append(names, f.Name)
	}
	return names
}

// SnakeCase converts a Go name to its template name: Title -> title,
// CreatedAt -> created_at, UserID -> user_id, HTMLBody -> html_body.
func SnakeCase(s string) string { return render.SnakeCase(s) }

// PascalCase converts a template name to its Go name: todo_list -> TodoList.
// Common initialisms are kept upper case: user_id -> UserID.
func PascalCase(s string) string {
	var b strings.Builder
	for _, word := range strings.Split(s, "_") {
		if word == "" {
			continue
		}
		if initialisms[word] {
			b.WriteString(strings.ToUpper(word))
			continue
		}
		b.WriteString(strings.ToUpper(word[:1]) + word[1:])
	}
	return b.String()
}

// initialisms are written in upper case in Go names, following Go style.
var initialisms = map[string]bool{"id": true, "url": true, "html": true, "http": true, "json": true, "api": true, "uri": true, "uuid": true, "ip": true, "css": true}
