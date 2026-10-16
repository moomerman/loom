// Package diff encodes render trees for the wire and computes sparse diffs
// between two renders of the same template. The format is documented in
// docs/wire.md; Apply implements the client's side of it, so tests can
// check that applying a diff to the old tree always yields the new one.
//
// Encoded values are plain JSON values (string, []any, map[string]any), so
// they can be passed to encoding/json directly.
package diff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/moomerman/loom/render"
)

// Full encodes a whole tree: the initial render, or a resync after the
// client reconnects.
//
//	{"s": ["<p>", "</p>"], "d": ["hello"]}
func Full(r *render.Rendered) map[string]any {
	m := map[string]any{"s": r.Statics.Parts}
	if len(r.Dynamics) > 0 {
		m["d"] = fullDynamics(r.Dynamics)
	}
	return m
}

func fullDynamics(dyns []render.Dynamic) []any {
	out := make([]any, len(dyns))
	for i, d := range dyns {
		out[i] = fullDynamic(d)
	}
	return out
}

func fullDynamic(d render.Dynamic) any {
	switch d := d.(type) {
	case render.Text:
		return string(d)
	case *render.Rendered:
		return Full(d)
	case *render.List:
		items := make([]any, len(d.Items))
		for i, it := range d.Items {
			items[i] = []any{it.Key, fullDynamics(it.Dynamics)}
		}
		return map[string]any{"s": d.Statics.Parts, "k": items}
	}
	panic(fmt.Sprintf("diff: unexpected dynamic %T", d))
}

// Diff returns the changes from old to new, two renders of the same
// template, or nil if nothing changed. It fails only if a list has
// duplicate keys.
//
//	{"d": {"0": "new text", "2": {"k": [["a", {"0": "Buy oat milk"}]]}}}
func Diff(old, new *render.Rendered) (map[string]any, error) {
	if old == nil || old.Statics != new.Statics {
		return Full(new), nil
	}
	return diffDynamics(old.Dynamics, new.Dynamics)
}

// diffDynamics returns {"d": {index: change}} for the changed slots, or nil.
func diffDynamics(old, new []render.Dynamic) (map[string]any, error) {
	changes, err := diffSlots(old, new)
	if err != nil || changes == nil {
		return nil, err
	}
	return map[string]any{"d": changes}, nil
}

// diffSlots returns {index: change} for the changed slots, or nil.
func diffSlots(old, new []render.Dynamic) (map[string]any, error) {
	var changes map[string]any
	for i, d := range new {
		var o render.Dynamic
		if i < len(old) {
			o = old[i]
		}
		c, err := diffDynamic(o, d)
		if err != nil {
			return nil, err
		}
		if c != nil {
			if changes == nil {
				changes = map[string]any{}
			}
			changes[strconv.Itoa(i)] = c
		}
	}
	return changes, nil
}

// diffDynamic returns the change for one slot, or nil if it is unchanged.
func diffDynamic(old, new render.Dynamic) (any, error) {
	switch n := new.(type) {
	case render.Text:
		if o, ok := old.(render.Text); ok && o == n {
			return nil, nil
		}
		return string(n), nil
	case *render.Rendered:
		if o, ok := old.(*render.Rendered); ok && o.Statics == n.Statics {
			d, err := diffDynamics(o.Dynamics, n.Dynamics)
			if d == nil {
				return nil, err // avoid a typed nil in the interface
			}
			return d, err
		}
		return Full(n), nil
	case *render.List:
		if o, ok := old.(*render.List); ok && o.Statics == n.Statics {
			d, err := diffList(o, n)
			if d == nil {
				return nil, err
			}
			return d, err
		}
		if err := checkKeys(n); err != nil {
			return nil, err
		}
		return fullDynamic(n), nil
	}
	return nil, fmt.Errorf("diff: unexpected dynamic %T", new)
}

// diffList diffs two renders of the same :for. When the keys are the same
// and in the same order, only changed items are sent:
//
//	{"k": [["a", {"0": "new title"}]]}
//
// Otherwise the whole new order is sent; each entry is [key] for an
// unchanged item, [key, {patch}] for a changed one, and [key, [dynamics]]
// for a new one. Missing keys are deleted:
//
//	{"o": [["b"], ["a", {"0": "x"}], ["c", ["new", "item"]]]}
func diffList(old, new *render.List) (map[string]any, error) {
	if err := checkKeys(new); err != nil {
		return nil, err
	}
	oldItems := make(map[string]render.Item, len(old.Items))
	for _, it := range old.Items {
		oldItems[it.Key] = it
	}

	sameOrder := len(old.Items) == len(new.Items)
	for i := 0; sameOrder && i < len(new.Items); i++ {
		sameOrder = old.Items[i].Key == new.Items[i].Key
	}

	var entries []any
	for _, it := range new.Items {
		o, existed := oldItems[it.Key]
		if !existed {
			entries = append(entries, []any{it.Key, fullDynamics(it.Dynamics)})
			continue
		}
		patch, err := diffSlots(o.Dynamics, it.Dynamics)
		if err != nil {
			return nil, err
		}
		switch {
		case patch != nil:
			entries = append(entries, []any{it.Key, patch})
		case !sameOrder:
			entries = append(entries, []any{it.Key})
		}
	}

	if sameOrder {
		if entries == nil {
			return nil, nil
		}
		return map[string]any{"k": entries}, nil
	}
	if entries == nil {
		entries = []any{} // the new list is empty
	}
	return map[string]any{"o": entries}, nil
}

func checkKeys(l *render.List) error {
	seen := make(map[string]bool, len(l.Items))
	for _, it := range l.Items {
		if seen[it.Key] {
			return &DuplicateKeyError{Statics: l.Statics.ID, Key: it.Key}
		}
		seen[it.Key] = true
	}
	return nil
}

// DuplicateKeyError reports two items of one list with the same key.
type DuplicateKeyError struct {
	Statics string // ID of the list's statics, e.g. "TodoList.1"
	Key     string
}

func (e *DuplicateKeyError) Error() string {
	return fmt.Sprintf("list %s has two items with key %q; :key values must be unique within a list", e.Statics, e.Key)
}

// Marshal encodes an encoded tree or diff as JSON for the wire, without
// escaping <, > and &, which keeps HTML-heavy payloads small. Don't embed
// its output in an HTML <script> element; use encoding/json there.
func Marshal(v map[string]any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// CheckKeys reports the first list in the tree with duplicate keys.
func CheckKeys(r *render.Rendered) error {
	for _, d := range r.Dynamics {
		if err := checkDynamicKeys(d); err != nil {
			return err
		}
	}
	return nil
}

func checkDynamicKeys(d render.Dynamic) error {
	switch d := d.(type) {
	case *render.Rendered:
		return CheckKeys(d)
	case *render.List:
		if err := checkKeys(d); err != nil {
			return err
		}
		for _, it := range d.Items {
			for _, x := range it.Dynamics {
				if err := checkDynamicKeys(x); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
