// Package form is the state of an HTML form on a live page, between the
// template that renders it and the code that saves it: what the person
// typed, field by field, the typed struct it decodes to, and each field's
// error.
//
// The struct is the form's single description. Its fields are the form's
// fields, named in templates and in the payload by their snake_case
// names, as everywhere in Loom: Go PaidBy is paid_by. A template takes
// the form as a form[ExpenseInput] parameter and binds an input to a
// field with <.input field={expense.paid_by} />, which `loom check`
// checks against the struct; the page's Assigns passes the
// form.Form[ExpenseInput] it holds, which `go build` checks. Validation
// names fields by pointer, &in.PaidBy, which `go build` checks too. So a
// field name is checked at every seam it crosses.
//
// The flow: the form's on:change calls Change, which
// decodes and validates what was typed and shows the errors of the fields
// the person has changed; on:submit calls Submit, which shows every error
// and returns the input when there are none, to save. See docs/forms.md.
package form

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/moomerman/loom/render"
)

// Form is a form's state. Make one with New, in Mount or in the handler
// that opens the form, and keep it in the page; give it to the template
// as is from Assigns. The zero Form is an empty form of T.
//
// A Form never changes the maps it was made with, so the copy of a page
// each page load gets doesn't share a form's state with another.
type Form[T any] struct {
	data      T
	raw       map[string]string // what was typed, by field name: "amount", "exact[sam]"
	errs      map[string]string // each field's first error
	used      map[string]bool   // fields the person has changed, whose errors show
	submitted bool              // after a submit, every error shows
}

// Validator is implemented by an input struct that checks itself. The
// form calls Validate after each Change and Submit, on the decoded input:
//
//	func (in *SettleInput) Validate(e *form.Errors) {
//		if in.From == in.To {
//			e.Add(&in.To, "Pick two different people.")
//		}
//	}
//
// The receiver must be a pointer, so &in.To is the form's own field.
type Validator interface {
	Validate(e *Errors)
}

// Field is one field of a form as a template sees it: its name for the
// input's name attribute, the text to show in it, and its error, which
// is empty until the person has changed the field or submitted the form.
// A bool field's value is "true" or "false".
type Field struct {
	Name  string
	Value string
	Error string
}

// Entries are the fields of a map field, one per key: in a template,
// expense.exact[person.id] is the field named "exact[sam]".
type Entries struct {
	name string
	get  func(name string) Field
}

// Key returns the entry with the given key.
func (e Entries) Key(key string) Field {
	name := e.name + "[" + key + "]"
	if e.get == nil {
		return Field{Name: name}
	}
	return e.get(name)
}

// New returns a form holding data, as yet unchanged, so no errors show.
// It panics (FM801, FM802) if T isn't a struct a form can decode; the
// app's startup checks every form in a page's assigns the same way.
func New[T any](data T) Form[T] {
	s := mustSpecs[T]()
	f := Form[T]{data: data, raw: map[string]string{}}
	for _, fs := range s.fields {
		fs.format(reflect.ValueOf(&f.data).Elem().Field(fs.index), f.raw)
	}
	f.errs = f.validate(nil)
	return f
}

// Change takes the form's fields from an on:change payload (a form's
// event sends all its named fields), decodes and validates them, and
// shows the errors of each field whose value changed.
func (f *Form[T]) Change(payload map[string]string) {
	f.decode(payload)
}

// Submit takes the form's fields from an on:submit payload, decodes and
// validates them, and shows every error. It returns the input and true
// when there are none, ready to save; the input is the form's own, so
// AddError can name its fields after a failed save.
func (f *Form[T]) Submit(payload map[string]string) (*T, bool) {
	f.decode(payload)
	f.submitted = true
	return &f.data, len(f.errs) == 0
}

// AddError adds an error the form couldn't know, such as a name already
// taken when the insert ran, to a field of the input Submit returned:
// f.AddError(&in.Email, "That email is taken."). It shows at once. It
// panics (FM803) if field isn't a pointer to one of that input's fields.
func (f *Form[T]) AddError(field any, msg string) {
	f.addError(field, "", false, msg)
}

// AddKeyError is AddError for one entry of a map field.
func (f *Form[T]) AddKeyError(field any, key, msg string) {
	f.addError(field, key, true, msg)
}

func (f *Form[T]) addError(field any, key string, keyed bool, msg string) {
	e := &Errors{base: reflect.ValueOf(&f.data), specs: mustSpecs[T](), errs: map[string]string{}}
	if keyed {
		e.AddKey(field, key, msg)
	} else {
		e.Add(field, msg)
	}
	errs := make(map[string]string, len(f.errs)+1)
	for k, v := range f.errs {
		errs[k] = v
	}
	for k, v := range e.errs {
		if _, ok := errs[k]; !ok {
			errs[k] = v
		}
	}
	f.errs = errs
	f.submitted = true
}

// Value returns the input as last decoded. A field whose text didn't
// parse (an int field holding "abc") is its zero value, with an error.
// Treat its maps as read-only.
func (f *Form[T]) Value() T { return f.data }

// Valid reports whether the input as last decoded has no errors, shown
// or not.
func (f *Form[T]) Valid() bool { return len(f.errs) == 0 }

// Field returns the field with the given template name. Generated code
// calls it for expense.amount; the name is checked at compile time.
func (f Form[T]) Field(name string) Field {
	if f.raw == nil {
		f = New(f.data) // the zero Form: an unchanged form of the zero input
	}
	fd := Field{Name: name, Value: f.raw[name]}
	if f.submitted || f.used[name] {
		fd.Error = f.errs[name]
	}
	return fd
}

// Entries returns the entries of a map field. Generated code calls it
// for expense.exact[key].
func (f Form[T]) Entries(name string) Entries {
	fs := mustSpecs[T]().byName[name]
	isBool := fs != nil && fs.kind == kindBoolMap
	return Entries{name: name, get: func(entry string) Field {
		fd := f.Field(entry)
		if isBool && fd.Value == "" {
			fd.Value = "false" // a key the map doesn't have yet
		}
		return fd
	}}
}

// CheckInput reports whether T is a struct a form can decode, with a
// valid Validate method if it has one. loom.App checks each form in a
// page's assigns with it at startup.
func (Form[T]) CheckInput() error {
	_, err := specsOf(reflect.TypeOf((*T)(nil)).Elem())
	return err
}

// AssignsJSON is the form as dev tools show it: each field's value and
// shown error, by name. A field tagged `loom:"secret"` (a password)
// shows render.Hidden instead of its value.
func (f Form[T]) AssignsJSON() any {
	s := mustSpecs[T]()
	out := map[string]any{}
	for name := range f.raw {
		fd := f.Field(name)
		var value any = fd.Value
		base, _, _ := strings.Cut(name, "[") // a map field's entry: name[key]
		if fs := s.byName[base]; fs != nil && fs.secret {
			value = render.HideSecret(reflect.ValueOf(fd.Value))
		}
		out[name] = map[string]any{"value": value, "error": fd.Error}
	}
	return out
}

// DecodeAssigns fills the form from JSON shaped like AssignsJSON's, so
// `loom render` can render a component that takes a form.
func (f *Form[T]) DecodeAssigns(raw any) error {
	obj, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("want an object of fields, like {\"amount\": {\"value\": \"12\", \"error\": \"\"}}")
	}
	payload := map[string]string{}
	errs := map[string]string{}
	for name, v := range obj {
		fd, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want an object with value and error", name)
		}
		if s, ok := fd["value"].(string); ok {
			payload[name] = s
		}
		if s, ok := fd["error"].(string); ok && s != "" {
			errs[name] = s
		}
	}
	*f = New(f.data)
	f.decode(payload)
	f.errs, f.submitted = errs, true
	return nil
}

// decode takes the fields in payload, parses them into a new input and
// validates it. It replaces the form's maps rather than changing them.
func (f *Form[T]) decode(payload map[string]string) {
	s := mustSpecs[T]()
	if f.raw == nil {
		*f = New(f.data)
	}
	raw := make(map[string]string, len(f.raw))
	for k, v := range f.raw {
		raw[k] = v
	}
	for _, fs := range s.fields {
		fs.take(payload, raw)
	}
	used := make(map[string]bool, len(f.used))
	for k, v := range f.used {
		used[k] = v
	}
	for k, v := range raw {
		if old, ok := f.raw[k]; !ok || old != v {
			used[k] = true
		}
	}
	var data T
	v := reflect.ValueOf(&data).Elem()
	parseErrs := map[string]string{}
	for _, fs := range s.fields {
		fs.parse(raw, v.Field(fs.index), parseErrs)
	}
	f.data, f.raw, f.used = data, raw, used
	f.errs = f.validate(parseErrs)
}

// validate runs the input's Validate, after the errors from parsing,
// which come first.
func (f *Form[T]) validate(errs map[string]string) map[string]string {
	if errs == nil {
		errs = map[string]string{}
	}
	if v, ok := any(&f.data).(Validator); ok {
		e := &Errors{base: reflect.ValueOf(&f.data), specs: mustSpecs[T](), errs: errs}
		v.Validate(e)
	}
	return errs
}

// Errors collects a Validate's errors.
type Errors struct {
	base  reflect.Value // pointer to the input being validated
	specs *specs
	errs  map[string]string
}

// Add adds an error to a field of the input being validated, named by
// its address: e.Add(&in.Amount, "Enter an amount, like 12.50."). A
// field keeps its first error. It panics (FM803) if field isn't a
// pointer to one of the input's fields.
func (e *Errors) Add(field any, msg string) {
	fs := e.field(field)
	if fs.kind == kindStringMap || fs.kind == kindBoolMap {
		panic(e.misuse(fmt.Sprintf("%s is a map field, so its errors belong to one entry", fs.goName),
			fmt.Sprintf("write e.AddKey(&in.%s, key, msg)", fs.goName)))
	}
	e.add(fs.name, msg)
}

// AddKey adds an error to one entry of a map field:
// e.AddKey(&in.Exact, personID, "Enter an amount.").
func (e *Errors) AddKey(field any, key, msg string) {
	fs := e.field(field)
	if fs.kind != kindStringMap && fs.kind != kindBoolMap {
		panic(e.misuse(fmt.Sprintf("%s isn't a map field", fs.goName), fmt.Sprintf("write e.Add(&in.%s, msg)", fs.goName)))
	}
	e.add(fs.name+"["+key+"]", msg)
}

// Has reports whether the field already has an error, so Validate can
// skip checks that would only repeat it.
func (e *Errors) Has(field any) bool {
	_, ok := e.errs[e.field(field).name]
	return ok
}

func (e *Errors) add(name, msg string) {
	if _, ok := e.errs[name]; !ok {
		e.errs[name] = msg
	}
}

// field finds the field ptr points to, by its offset in the input.
func (e *Errors) field(ptr any) *fieldSpec {
	p := reflect.ValueOf(ptr)
	if p.Kind() == reflect.Pointer && !p.IsNil() {
		off := p.Pointer() - e.base.Pointer()
		for _, fs := range e.specs.fields {
			if fs.offset == off && fs.typ == p.Type().Elem() {
				return fs
			}
		}
	}
	panic(e.misuse(fmt.Sprintf("%T isn't a pointer to a field of the %s being validated", ptr, e.specs.typ),
		fmt.Sprintf("name the field by its address in the input Validate was given (or Submit returned), like &in.%s", e.specs.fields[0].goName)))
}

func (e *Errors) misuse(msg, fix string) error {
	return &Error{Code: FM803, Msg: msg, Fix: fix}
}

// --- Field specs ---

type kind int

const (
	kindString kind = iota
	kindInt
	kindFloat
	kindBool
	kindStringMap
	kindBoolMap
)

// specs describe an input struct's fields, worked out once per type.
type specs struct {
	typ    reflect.Type
	fields []*fieldSpec
	byName map[string]*fieldSpec
}

type fieldSpec struct {
	index  int
	name   string // template name
	goName string
	kind   kind
	typ    reflect.Type
	offset uintptr
	secret bool // tagged loom:"secret": dev tools hide its value
}

var cache sync.Map // reflect.Type -> *specs or error

func mustSpecs[T any]() *specs {
	s, err := specsOf(reflect.TypeOf((*T)(nil)).Elem())
	if err != nil {
		panic(err)
	}
	return s
}

func specsOf(t reflect.Type) (*specs, error) {
	if v, ok := cache.Load(t); ok {
		if err, ok := v.(error); ok {
			return nil, err
		}
		return v.(*specs), nil
	}
	s, err := buildSpecs(t)
	if err != nil {
		cache.Store(t, err)
		return nil, err
	}
	cache.Store(t, s)
	return s, nil
}

var errorsType = reflect.TypeOf((*Errors)(nil))

func buildSpecs(t reflect.Type) (*specs, error) {
	if t.Kind() != reflect.Struct {
		return nil, &Error{Code: FM801, Msg: fmt.Sprintf("a form's input must be a struct, not %s", t),
			Fix: "declare a struct with a field per form field, e.g. type SettleInput struct { Amount string }"}
	}
	s := &specs{typ: t, byName: map[string]*fieldSpec{}}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		fs := &fieldSpec{index: i, name: render.SnakeCase(sf.Name), goName: sf.Name, typ: sf.Type, offset: sf.Offset, secret: render.Secret(sf)}
		switch {
		case sf.Type.Kind() == reflect.String:
			fs.kind = kindString
		case sf.Type.Kind() == reflect.Int || sf.Type.Kind() == reflect.Int64:
			fs.kind = kindInt
		case sf.Type.Kind() == reflect.Float64:
			fs.kind = kindFloat
		case sf.Type.Kind() == reflect.Bool:
			fs.kind = kindBool
		case sf.Type.Kind() == reflect.Map && sf.Type.Key().Kind() == reflect.String && sf.Type.Elem().Kind() == reflect.String:
			fs.kind = kindStringMap
		case sf.Type.Kind() == reflect.Map && sf.Type.Key().Kind() == reflect.String && sf.Type.Elem().Kind() == reflect.Bool:
			fs.kind = kindBoolMap
		default:
			return nil, &Error{Code: FM801, Msg: fmt.Sprintf("field %s.%s has type %s, which a form can't decode", t.Name(), sf.Name, sf.Type),
				Fix: "form fields are string, int, int64, float64, bool, map[string]string or map[string]bool; use a string and parse it in Validate"}
		}
		s.fields = append(s.fields, fs)
		s.byName[fs.name] = fs
	}
	if len(s.fields) == 0 {
		return nil, &Error{Code: FM801, Msg: fmt.Sprintf("%s has no exported fields, so its form has none", t),
			Fix: "export the struct's fields: Amount, not amount"}
	}
	// Validate, if declared, must be func (in *T) Validate(e *form.Errors).
	if _, ok := t.MethodByName("Validate"); ok {
		return nil, &Error{Code: FM802, Msg: fmt.Sprintf("%s.Validate has a value receiver, so the errors it adds can't name the form's fields", t.Name()),
			Fix: fmt.Sprintf("give it a pointer receiver: func (in *%s) Validate(e *form.Errors)", t.Name())}
	}
	if m, ok := reflect.PointerTo(t).MethodByName("Validate"); ok {
		mt := m.Type // includes the receiver
		if mt.NumIn() != 2 || mt.In(1) != errorsType || mt.NumOut() != 0 {
			return nil, &Error{Code: FM802, Msg: fmt.Sprintf("%s.Validate has type %s, so the form can't call it", t.Name(), strings.Replace(mt.String(), "func(*", "func (*", 1)),
				Fix: fmt.Sprintf("declare it as func (in *%s) Validate(e *form.Errors), adding errors with e.Add", t.Name())}
		}
	}
	return s, nil
}

// format writes a field's value as text into raw.
func (fs *fieldSpec) format(v reflect.Value, raw map[string]string) {
	switch fs.kind {
	case kindString:
		raw[fs.name] = v.String()
	case kindInt:
		raw[fs.name] = strconv.FormatInt(v.Int(), 10)
	case kindFloat:
		raw[fs.name] = strconv.FormatFloat(v.Float(), 'f', -1, 64)
	case kindBool:
		raw[fs.name] = strconv.FormatBool(v.Bool())
	case kindStringMap, kindBoolMap:
		iter := v.MapRange()
		for iter.Next() {
			k := fs.name + "[" + iter.Key().String() + "]"
			if fs.kind == kindBoolMap {
				raw[k] = strconv.FormatBool(iter.Value().Bool())
			} else {
				raw[k] = iter.Value().String()
			}
		}
	}
}

// take copies a field's text from payload into raw. A form sends a
// checked checkbox and leaves an unchecked one out, so a bool missing
// from the payload is false.
func (fs *fieldSpec) take(payload, raw map[string]string) {
	switch fs.kind {
	case kindBool:
		raw[fs.name] = strconv.FormatBool(checked(payload, fs.name))
	case kindString, kindInt, kindFloat:
		if v, ok := payload[fs.name]; ok {
			raw[fs.name] = v
		}
	case kindStringMap:
		for k, v := range payload {
			if _, ok := entryKey(fs.name, k); ok {
				raw[k] = v
			}
		}
	case kindBoolMap:
		for k := range raw {
			if _, ok := entryKey(fs.name, k); ok {
				raw[k] = strconv.FormatBool(checked(payload, k))
			}
		}
		for k := range payload {
			if _, ok := entryKey(fs.name, k); ok {
				raw[k] = strconv.FormatBool(checked(payload, k))
			}
		}
	}
}

func checked(payload map[string]string, name string) bool {
	v, ok := payload[name]
	return ok && v != "false"
}

// entryKey returns the key of a map field's entry name: "exact[sam]" is
// the entry "sam" of field "exact".
func entryKey(field, name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, field+"[")
	if !ok || !strings.HasSuffix(rest, "]") {
		return "", false
	}
	return strings.TrimSuffix(rest, "]"), true
}

// parse sets a field from its text in raw, recording an error if the
// text isn't a value of its type.
func (fs *fieldSpec) parse(raw map[string]string, v reflect.Value, errs map[string]string) {
	text := strings.TrimSpace(raw[fs.name])
	switch fs.kind {
	case kindString:
		v.SetString(raw[fs.name])
	case kindInt:
		if text == "" {
			return
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || v.OverflowInt(n) {
			errs[fs.name] = "Enter a whole number."
			return
		}
		v.SetInt(n)
	case kindFloat:
		if text == "" {
			return
		}
		n, err := strconv.ParseFloat(text, 64)
		if err != nil {
			errs[fs.name] = "Enter a number."
			return
		}
		v.SetFloat(n)
	case kindBool:
		v.SetBool(raw[fs.name] == "true")
	case kindStringMap, kindBoolMap:
		m := reflect.MakeMap(fs.typ)
		var keys []string
		for k := range raw {
			if key, ok := entryKey(fs.name, k); ok {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			text := raw[fs.name+"["+key+"]"]
			if fs.kind == kindBoolMap {
				m.SetMapIndex(reflect.ValueOf(key).Convert(fs.typ.Key()), reflect.ValueOf(text == "true").Convert(fs.typ.Elem()))
			} else {
				m.SetMapIndex(reflect.ValueOf(key).Convert(fs.typ.Key()), reflect.ValueOf(text).Convert(fs.typ.Elem()))
			}
		}
		v.Set(m)
	}
}
