package render

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Assigns as JSON. Dev tools show a page's assigns and render components
// from JSON, using template names for every field: Go field CreatedAt is
// "created_at", as in templates. Slots are skipped.

// SnakeCase converts a Go name to its template name: Title -> title,
// CreatedAt -> created_at, UserID -> user_id, HTMLParser -> html_parser.
func SnakeCase(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUpper(c) {
			// Start a new word at an upper-case letter that follows a
			// lower-case letter or digit, or that starts a word after an
			// acronym (the P in HTMLParser).
			prevLower := i > 0 && (isLower(s[i-1]) || isDigit(s[i-1]))
			acronymEnd := i > 0 && isUpper(s[i-1]) && i+1 < len(s) && isLower(s[i+1])
			if prevLower || acronymEnd {
				b.WriteByte('_')
			}
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

func isUpper(c byte) bool { return 'A' <= c && c <= 'Z' }
func isLower(c byte) bool { return 'a' <= c && c <= 'z' }
func isDigit(c byte) bool { return '0' <= c && c <= '9' }

var (
	slotType = reflect.TypeOf(Slot(nil))
)

// AssignsJSON converts assigns (or any value usable in templates) to a
// JSON-ready value with template field names. A field tagged
// `loom:"secret"` shows as Hidden (or "" when empty), so dev tools such
// as `loom inspect`, whose output agents paste into logs and pull
// requests, never show a typed password.
func AssignsJSON(v any) any { return toJSON(reflect.ValueOf(v)) }

// Hidden is what dev tools show for a secret field's value.
const Hidden = "[secret]"

// Secret reports whether a struct field is tagged `loom:"secret"`: a
// password, a code, a token. Its value is never shown by dev tools.
func Secret(f reflect.StructField) bool { return f.Tag.Get("loom") == "secret" }

// HideSecret returns what dev tools show of a secret value: "" if it is
// empty, so they still show whether something was typed, else Hidden.
func HideSecret(v reflect.Value) any {
	if v.IsZero() {
		return ""
	}
	return Hidden
}

// jsonValue is implemented by assigns values with their own JSON form,
// such as a form.Form, whose state is unexported.
type jsonValue interface{ AssignsJSON() any }

// jsonDecoder is implemented by a pointer to such a value.
type jsonDecoder interface{ DecodeAssigns(raw any) error }

func toJSON(v reflect.Value) any {
	if v.IsValid() && v.CanInterface() {
		if j, ok := v.Interface().(jsonValue); ok {
			return j.AssignsJSON()
		}
	}
	switch v.Kind() {
	case reflect.Invalid:
		return nil
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return toJSON(v.Elem())
	case reflect.Struct:
		m := map[string]any{}
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			switch {
			case !f.IsExported() || f.Type == slotType:
			case Secret(f):
				m[SnakeCase(f.Name)] = HideSecret(v.Field(i))
			default:
				m[SnakeCase(f.Name)] = toJSON(v.Field(i))
			}
		}
		return m
	case reflect.Slice, reflect.Array:
		out := make([]any, v.Len())
		for i := range out {
			out[i] = toJSON(v.Index(i))
		}
		return out
	case reflect.Func, reflect.Chan:
		return nil
	default:
		return v.Interface()
	}
}

// DecodeAssigns fills ptr, a pointer to an assigns struct, from JSON that
// uses template field names. Unknown fields and wrong types are errors
// that name the path, e.g. `todos[1].title: want a string, got 3`.
func DecodeAssigns(data []byte, ptr any) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("assigns are not valid JSON: %v", err)
	}
	return fromJSON(raw, reflect.ValueOf(ptr).Elem(), "")
}

func fromJSON(raw any, v reflect.Value, path string) error {
	where := path
	if where == "" {
		where = "assigns"
	}
	if raw == nil {
		return nil // leave the zero value
	}
	if v.CanAddr() {
		if d, ok := v.Addr().Interface().(jsonDecoder); ok {
			if err := d.DecodeAssigns(raw); err != nil {
				return fmt.Errorf("%s: %v", where, err)
			}
			return nil
		}
	}
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		return fromJSON(raw, v.Elem(), path)
	case reflect.Struct:
		obj, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want an object, got %s", where, jsonKind(raw))
		}
		fields := map[string]int{}
		var names []string
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.IsExported() && f.Type != slotType {
				fields[SnakeCase(f.Name)] = i
				names = append(names, SnakeCase(f.Name))
			}
		}
		sort.Strings(names)
		for k, val := range obj {
			i, ok := fields[k]
			if !ok {
				return fmt.Errorf("%s: unknown field %q; fields are %s", where, k, strings.Join(names, ", "))
			}
			if err := fromJSON(val, v.Field(i), join(path, k)); err != nil {
				return err
			}
		}
		return nil
	case reflect.Slice:
		arr, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%s: want an array, got %s", where, jsonKind(raw))
		}
		s := reflect.MakeSlice(v.Type(), len(arr), len(arr))
		for i, e := range arr {
			if err := fromJSON(e, s.Index(i), fmt.Sprintf("%s[%d]", where, i)); err != nil {
				return err
			}
		}
		v.Set(s)
		return nil
	case reflect.String:
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("%s: want a string, got %s", where, jsonKind(raw))
		}
		v.SetString(s)
	case reflect.Bool:
		b, ok := raw.(bool)
		if !ok {
			return fmt.Errorf("%s: want true or false, got %s", where, jsonKind(raw))
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := raw.(float64)
		if !ok || n != float64(int64(n)) {
			return fmt.Errorf("%s: want an integer, got %s", where, jsonKind(raw))
		}
		v.SetInt(int64(n))
	case reflect.Float32, reflect.Float64:
		n, ok := raw.(float64)
		if !ok {
			return fmt.Errorf("%s: want a number, got %s", where, jsonKind(raw))
		}
		v.SetFloat(n)
	default:
		return fmt.Errorf("%s: a %s can't be set from JSON", where, v.Type())
	}
	return nil
}

func join(path, k string) string {
	if path == "" {
		return k
	}
	return path + "." + k
}

func jsonKind(v any) string {
	switch v := v.(type) {
	case string:
		return fmt.Sprintf("the string %q", v)
	case float64:
		return fmt.Sprintf("the number %v", v)
	case bool:
		return fmt.Sprint(v)
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	}
	return "null"
}

// The registry of compiled components, so dev tools can render any
// component by name. Generated code registers each component.
var (
	registryMu sync.Mutex
	registry   []Template
	sourceDirs = map[string]bool{}
)

// Register records a component and returns it. Generated code calls it
// for every component.
func Register[A any](c Component[A]) Component[A] {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, c)
	if _, file, _, ok := runtime.Caller(1); ok && filepath.IsAbs(file) {
		sourceDirs[filepath.Dir(file)] = true
	}
	return c
}

// SourceDirs returns the directories of the generated files that
// registered components, as the binary was built: where their .hx
// templates were, so loomtest can check the generated code is current.
// Binaries built with -trimpath record none.
func SourceDirs() []string {
	registryMu.Lock()
	defer registryMu.Unlock()
	dirs := make([]string, 0, len(sourceDirs))
	for d := range sourceDirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

// Components returns every registered component.
func Components() []Template {
	registryMu.Lock()
	defer registryMu.Unlock()
	return append([]Template(nil), registry...)
}

// Lookup finds a registered component by name, either "TodoApp" or
// qualified by its package name, "templates.TodoApp".
func Lookup(name string) (Template, error) {
	var found []Template
	var all []string
	for _, t := range Components() {
		q := t.PackageName() + "." + t.ComponentName()
		all = append(all, q)
		if name == t.ComponentName() || name == q {
			found = append(found, t)
		}
	}
	sort.Strings(all)
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return nil, fmt.Errorf("no component %q; components are %s", name, strings.Join(all, ", "))
	}
	var qs []string
	for _, t := range found {
		qs = append(qs, t.PackageName()+"."+t.ComponentName())
	}
	return nil, fmt.Errorf("component name %q is ambiguous: use one of %s", name, strings.Join(qs, ", "))
}
