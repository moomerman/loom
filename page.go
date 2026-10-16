package loom

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/moomerman/loom/form"
	"github.com/moomerman/loom/render"
)

// A page's methods besides Mount are checked when its route is
// registered, rather than on the first request: Assigns must return the
// template's assigns type (RT406), and every event the page's markup
// sends must have a handler in Events (RT422).

// checkPage checks a page against its template and the app layout it
// renders in, panicking at the caller of Live.
func checkPage(page Page, tmpl render.Template, layout *layout) {
	if _, err := clonePage(page); err != nil {
		panic(err)
	}
	where := callerOutsideLoom()
	checkParamsHandler(page, where)
	m, ok := reflect.TypeOf(page).MethodByName("Assigns")
	want := tmpl.AssignsType()
	if !ok || m.Type.NumIn() != 2 || m.Type.In(1) != ctxType || m.Type.NumOut() != 1 || m.Type.Out(0) != want {
		got := "has no Assigns method"
		if ok {
			got = fmt.Sprintf("has Assigns of type %s", strings.Replace(m.Type.String(), "func(*"+reflect.TypeOf(page).Elem().String()+", ", "func(", 1))
		}
		panic(&Error{Code: RT406, Where: where,
			Msg: fmt.Sprintf("%T %s, but its template %s needs %s", page, got, tmpl.ComponentName(), want),
			Fix: fmt.Sprintf("add func (p %T) Assigns(c *loom.Ctx) %s, returning the page's state", page, want)})
	}
	if err := checkForms(want, map[reflect.Type]bool{}); err != nil {
		panic(&Error{Code: err.Code, Where: where, Msg: fmt.Sprintf("%s, in the assigns of %T: %s", tmpl.ComponentName(), page, err.Msg), Fix: err.Fix})
	}
	handlers := map[string]bool{}
	for name := range eventsOf(page) {
		handlers[name] = true
	}
	var live Page
	if layout != nil {
		live = layout.live
	}
	if live != nil {
		var both []string
		for name := range eventsOf(live) {
			if handlers[name] {
				both = append(both, name)
			}
			handlers[name] = true
		}
		if len(both) > 0 {
			sort.Strings(both)
			panic(&Error{Code: RT434, Where: where,
				Msg: fmt.Sprintf("%T and its live layout %T both handle %s, so the layout's handler would never run on this page", page, live, strings.Join(quoteAll(both), ", ")),
				Fix: "an event goes to the one that handles it; remove it from one of the two Events, or rename the page's and the markup that sends it"})
		}
	}
	uses := tmpl.ComponentEvents()
	if layout != nil {
		uses = append(append([]render.EventUse(nil), uses...), layout.tmpl.ComponentEvents()...)
	}
	for _, u := range uses {
		if !handlers[u.Name] {
			panic(missingHandler(page, live, layout, u, handlers))
		}
	}
}

// checkForms checks the input type of every form in assigns type t, so
// one a form can't decode fails at startup (FM801, FM802) rather than
// when the page first renders it.
func checkForms(t reflect.Type, seen map[reflect.Type]bool) *form.Error {
	if seen[t] {
		return nil
	}
	seen[t] = true
	if c, ok := reflect.Zero(t).Interface().(interface{ CheckInput() error }); ok {
		var fe *form.Error
		if err := c.CheckInput(); errors.As(err, &fe) {
			return fe
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).IsExported() {
				if err := checkForms(t.Field(i).Type, seen); err != nil {
					return err
				}
			}
		}
	case reflect.Slice, reflect.Pointer:
		return checkForms(t.Elem(), seen)
	}
	return nil
}

// eventsOf returns a page's or live layout's Events, or nil.
func eventsOf(p Page) Events {
	if e, ok := p.(interface{ Events() Events }); ok {
		return e.Events()
	}
	return nil
}

func quoteAll(xs []string) []string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = strconv.Quote(x)
	}
	return q
}

// missingHandler is the RT422 error for an event with no handler.
func missingHandler(page, live Page, layout *layout, u render.EventUse, handlers map[string]bool) *Error {
	names := make([]string, 0, len(handlers))
	for n := range handlers {
		names = append(names, n)
	}
	sort.Strings(names)
	fix := fmt.Sprintf(`handle it: add "%s": p.%s to %T's Events, with func (p %T) %s(c *loom.Ctx, payload loom.Payload)`, u.Name, methodName(u.Name), page, page, methodName(u.Name))
	fromLayout := layout != nil && slices.ContainsFunc(layout.tmpl.ComponentEvents(), func(l render.EventUse) bool { return l.At == u.At })
	switch {
	case live != nil:
		fix = fmt.Sprintf(`handle it in the page or in its live layout: add "%s": l.%s to %T's Events`, u.Name, methodName(u.Name), live)
	case fromLayout:
		fix += fmt.Sprintf("; if the layout's event changes state every page shares, make %s a loom.LiveLayout and handle it there, once", layout.name())
	}
	if n := closestName(u.Name, names); n != "" {
		fix = fmt.Sprintf("if the template means %q, fix the name there; otherwise %s", n, fix)
	}
	has := "it handles none"
	if len(names) > 0 {
		has = "it handles " + strings.Join(names, ", ")
	}
	who := fmt.Sprintf("%T", page)
	if live != nil {
		who += fmt.Sprintf(" and its live layout %T", live)
		has = strings.Replace(has, "it handles", "they handle", 1)
	}
	return &Error{Code: RT422, Where: u.At,
		Msg: fmt.Sprintf("on:%s sends event %q, which %s has no handler for (%s), so the %s would do nothing", u.On, u.Name, who, has, u.On),
		Fix: fix}
}

// methodName turns an event name into a method name: toggle_todo -> toggleTodo.
func methodName(event string) string {
	var b strings.Builder
	upper := false
	for _, r := range event {
		switch {
		case r == '_' || r == '-' || r == ':' || r == '.':
			upper = b.Len() > 0
		case upper:
			b.WriteString(strings.ToUpper(string(r)))
			upper = false
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// closestName returns the candidate within two edits of name, or "".
func closestName(name string, candidates []string) string {
	best, bestD := "", 3
	for _, c := range candidates {
		if d := editDistance(name, c); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// boundPage is a page load's copy of a page, with its methods looked up.
type boundPage struct {
	page    Page
	assigns func(c *Ctx) Assigns
	events  Events
	info    InfoHandler   // nil if the page has no HandleInfo
	params  ParamsHandler // nil if the page has no HandleParams
}

func bindPage(page Page) *boundPage {
	b := &boundPage{page: page}
	b.info, _ = page.(InfoHandler)
	b.params, _ = page.(ParamsHandler)
	m := reflect.ValueOf(page).MethodByName("Assigns")
	b.assigns = func(c *Ctx) Assigns { return m.Call([]reflect.Value{reflect.ValueOf(c)})[0].Interface() }
	if e, ok := page.(interface{ Events() Events }); ok {
		b.events = e.Events()
	}
	return b
}

// handler returns the func for an event, or an RT423 error naming the
// events the page and its live layout (if not nil) handle.
func (b *boundPage) handler(name string, live *boundPage) (func(c *Ctx, payload Payload), error) {
	if f := b.events[name]; f != nil {
		return f, nil
	}
	names := make([]string, 0, len(b.events))
	for n := range b.events {
		names = append(names, n)
	}
	who, has := fmt.Sprintf("%T", b.page), "it handles"
	if live != nil {
		for n := range live.events {
			names = append(names, n)
		}
		who, has = fmt.Sprintf("%T with its live layout %T", b.page, live.page), "they handle"
	}
	slices.Sort(names)
	if len(names) > 0 {
		has += " " + strings.Join(names, ", ")
	} else {
		has += " none"
	}
	return nil, &Error{Code: RT423,
		Msg: fmt.Sprintf("%s got event %q, which it has no handler for (%s); the event was ignored", who, name, has),
		Fix: "send one of the page's events; a template can't send this one (RT422 checks that), so it came from a behavior's emit, another page's markup after a navigation, or a forged request"}
}
