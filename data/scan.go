package data

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Select runs a query and scans every row into a T.
//
//	todos, err := loom.Select[Todo](ctx, db, `select * from todos where user_id = $1 order by id`, userID)
//
// T is a struct, whose fields take the columns of the same name: the
// field's db tag (`db:"user_id"`), or else its name in snake_case
// (UserID takes user_id). Fields tagged `db:"-"` are skipped, embedded
// structs contribute their fields, and fields with no column are left
// alone. A column with no field is an error (DB610). T may also be a
// single value, such as int64, string or time.Time, for a query of one
// column. A NULL needs a pointer or sql.Null* field (DB611).
func Select[T any](ctx context.Context, q Querier, query string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	err = scanRows(rows, func(dest func(v any) []any) error {
		var v T
		if err := scanInto(rows, &v, dest); err != nil {
			return err
		}
		out = append(out, v)
		return nil
	}, reflect.TypeOf((*T)(nil)).Elem())
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []T{}
	}
	return out, rows.Close()
}

// Get runs a query that should return one row and scans it into a T, as
// Select does. It returns an error matching ErrNotFound (DB604) if there
// is no row, and DB605 if there is more than one.
//
//	todo, err := loom.Get[Todo](ctx, db, `select * from todos where id = $1`, id)
//	if errors.Is(err, loom.ErrNotFound) { ... }
func Get[T any](ctx context.Context, q Querier, query string, args ...any) (T, error) {
	var zero, v T
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return zero, err
	}
	defer rows.Close()
	n := 0
	err = scanRows(rows, func(dest func(v any) []any) error {
		n++
		if n > 1 {
			return &Error{Code: DB605, Msg: fmt.Sprintf("loom.Get expects one row, but the query returned more: %s", oneLine(query)),
				Fix: "filter on a unique column (such as the primary key), add LIMIT 1, or use loom.Select for many rows"}
		}
		return scanInto(rows, &v, dest)
	}, reflect.TypeOf((*T)(nil)).Elem())
	if err != nil {
		return zero, err
	}
	if n == 0 {
		return zero, &Error{Code: DB604, Msg: fmt.Sprintf("loom.Get found no row: %s %v", oneLine(query), args),
			Fix: "check the arguments; handle the missing row with errors.Is(err, loom.ErrNotFound), e.g. by showing a not-found page"}
	}
	return v, rows.Close()
}

// scanRows plans how the columns map to t and calls each for every row
// with a function returning the scan destinations in a *t.
func scanRows(rows Rows, each func(dest func(v any) []any) error, t reflect.Type) error {
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	p, err := planFor(t, cols)
	if err != nil {
		return err
	}
	for rows.Next() {
		if err := each(p.dests); err != nil {
			return err
		}
	}
	return rows.Err()
}

// scanInto scans the current row into v (a pointer), and on failure
// finds the column at fault to name it.
func scanInto(rows Rows, v any, dest func(v any) []any) error {
	ds := dest(v)
	err := rows.Scan(ds...)
	if err == nil {
		return nil
	}
	cols, _ := rows.Columns()
	p, _ := planFor(reflect.TypeOf(v).Elem(), cols)
	for i := range ds {
		probe := make([]any, len(ds))
		for j := range probe {
			probe[j] = new(any)
		}
		probe[i] = ds[i]
		if perr := rows.Scan(probe...); perr != nil {
			return p.scanError(i, perr)
		}
	}
	return err
}

// plan maps a query's columns to the fields of a type.
type plan struct {
	t      reflect.Type
	cols   []string
	fields [][]int // per column, the field index path; nil for a single value
	names  []string
}

var plans sync.Map // planKey -> *plan

type planKey struct {
	t    reflect.Type
	cols string
}

var (
	scannerType = reflect.TypeOf((*sql.Scanner)(nil)).Elem()
	timeType    = reflect.TypeOf(time.Time{})
)

// single reports whether t is scanned as one value rather than by field.
func single(t reflect.Type) bool {
	return t.Kind() != reflect.Struct || t == timeType || reflect.PointerTo(t).Implements(scannerType)
}

func planFor(t reflect.Type, cols []string) (*plan, error) {
	key := planKey{t, strings.Join(cols, "\x00")}
	if p, ok := plans.Load(key); ok {
		return p.(*plan), nil
	}
	p := &plan{t: t, cols: cols}
	if single(t) {
		if len(cols) != 1 {
			return nil, &Error{Code: DB612, Msg: fmt.Sprintf("the query returns %d columns (%s), but %s holds one value", len(cols), strings.Join(cols, ", "), t),
				Fix: "select exactly one column, or scan into a struct with a field per column"}
		}
		plans.Store(key, p)
		return p, nil
	}
	fields := map[string][]int{}
	names := map[string]string{}
	collectFields(t, nil, "", fields, names)
	seen := map[string]bool{}
	for _, c := range cols {
		if seen[c] {
			return nil, &Error{Code: DB612, Msg: fmt.Sprintf("the query returns column %q twice, so it can't be scanned into %s", c, t),
				Fix: fmt.Sprintf("alias one of them, e.g. `b.%s AS b_%s`, and give %s a field for it", c, c, t.Name())}
		}
		seen[c] = true
		idx, ok := fields[c]
		if !ok && c == "loom_xid" {
			// A column older Loom versions added to tracked tables, which
			// select * returns; no struct is expected to hold it.
			p.fields = append(p.fields, nil)
			p.names = append(p.names, c)
			continue
		}
		if !ok {
			return nil, &Error{Code: DB610, Msg: fmt.Sprintf("column %q has no matching field in %s", c, t),
				Fix: fmt.Sprintf("add a field to %s named %s or tagged `db:%q`, or leave the column out of the select list (list the columns instead of select *)", t, fieldName(c), c)}
		}
		p.fields = append(p.fields, idx)
		p.names = append(p.names, names[c])
	}
	plans.Store(key, p)
	return p, nil
}

// collectFields records the column name of each field of t, embedded
// structs included; outer fields win over embedded ones.
func collectFields(t reflect.Type, index []int, prefix string, fields map[string][]int, names map[string]string) {
	var embedded []reflect.StructField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("db")
		if tag == "-" {
			continue
		}
		if f.Anonymous && tag == "" && f.Type.Kind() == reflect.Struct && !single(f.Type) {
			embedded = append(embedded, f)
			continue
		}
		if !f.IsExported() {
			continue
		}
		col := tag
		if col == "" {
			col = SnakeCase(f.Name)
		}
		if _, dup := fields[col]; !dup {
			fields[col] = append(append([]int(nil), index...), i)
			names[col] = prefix + f.Name
		}
	}
	for _, f := range embedded {
		collectFields(f.Type, append(append([]int(nil), index...), f.Index...), prefix+f.Name+".", fields, names)
	}
}

// dests returns the scan destinations for the columns in *v.
func (p *plan) dests(v any) []any {
	rv := reflect.ValueOf(v).Elem()
	if p.fields == nil {
		return []any{rv.Addr().Interface()}
	}
	out := make([]any, len(p.fields))
	for i, idx := range p.fields {
		if idx == nil {
			out[i] = new(any) // a column no field takes: loom_xid
			continue
		}
		out[i] = rv.FieldByIndex(idx).Addr().Interface()
	}
	return out
}

// scanError explains why column i didn't fit its field.
func (p *plan) scanError(i int, err error) error {
	target, typ := p.t.String(), p.t
	if p.fields != nil {
		f := p.t.FieldByIndex(p.fields[i])
		target, typ = p.t.Name()+"."+p.names[i], f.Type
	}
	msg := err.Error()
	if j := strings.Index(msg, ": converting"); j >= 0 {
		msg = msg[j+2:]
	}
	fix := fmt.Sprintf("change %s's type to match the column, or cast the column in SQL (e.g. `%s::text`)", target, p.cols[i])
	if strings.Contains(msg, "NULL") {
		fix = fmt.Sprintf("the column is NULL in this row: make %s a pointer (*%s) or sql.Null type, or use coalesce(%s, ...) in the query", target, typ, p.cols[i])
	}
	return &Error{Code: DB611, Msg: fmt.Sprintf("can't scan column %q into %s (%s): %s", p.cols[i], target, typ, msg), Fix: fix, Err: err}
}

// SnakeCase converts a Go field name to the column name it takes by
// default: UserID -> user_id, HTMLBody -> html_body, DoneAt -> done_at.
func SnakeCase(name string) string {
	r := []rune(name)
	var b strings.Builder
	for i, c := range r {
		if unicode.IsUpper(c) {
			if i > 0 && (unicode.IsLower(r[i-1]) || unicode.IsDigit(r[i-1]) || i+1 < len(r) && unicode.IsLower(r[i+1]) && unicode.IsUpper(r[i-1])) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(c))
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// fieldName suggests a Go field name for a column: user_id -> UserID.
func fieldName(col string) string {
	var b strings.Builder
	for _, part := range strings.Split(col, "_") {
		switch part {
		case "":
		case "id", "url", "html", "json", "sql", "api", "uuid":
			b.WriteString(strings.ToUpper(part))
		default:
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
