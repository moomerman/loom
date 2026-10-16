package diff

import (
	"fmt"
	"strconv"
	"strings"
)

// Apply applies a diff to an encoded tree (as produced by Full) and returns
// the updated tree. It does what client/loom.js does, without a DOM, and
// does not modify tree. The result of applying Diff(old, new) to Full(old)
// is equal to Full(new).
func Apply(tree map[string]any, patch map[string]any) (map[string]any, error) {
	if patch == nil {
		return tree, nil
	}
	v, err := applyDynamic(tree, patch)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("diff: patch replaced the root with %T", v)
	}
	return m, nil
}

// applyDynamic applies the change for one slot to its old value.
func applyDynamic(old any, change any) (any, error) {
	switch c := change.(type) {
	case string:
		return c, nil
	case map[string]any:
		if _, full := c["s"]; full {
			return c, nil
		}
		o, ok := old.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("diff: patch %v applied to %T", c, old)
		}
		switch {
		case c["d"] != nil:
			return applyRendered(o, c["d"].(map[string]any))
		case c["k"] != nil:
			return applyUpdates(o, c["k"].([]any))
		case c["o"] != nil:
			return applyOrder(o, c["o"].([]any))
		}
	}
	return nil, fmt.Errorf("diff: invalid change %#v", change)
}

func applyRendered(old map[string]any, changes map[string]any) (map[string]any, error) {
	dyns, err := applySlots(asSlice(old["d"]), changes)
	if err != nil {
		return nil, err
	}
	return map[string]any{"s": old["s"], "d": dyns}, nil
}

// applySlots applies {index: change} to a copy of dyns.
func applySlots(dyns []any, changes map[string]any) ([]any, error) {
	out := append([]any(nil), dyns...)
	for k, c := range changes {
		i, err := strconv.Atoi(k)
		if err != nil || i < 0 || i >= len(out) {
			return nil, fmt.Errorf("diff: invalid slot %q", k)
		}
		if out[i], err = applyDynamic(out[i], c); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// applyUpdates applies {"k": [[key, patch], ...]}: changed items, same order.
func applyUpdates(old map[string]any, entries []any) (map[string]any, error) {
	items := append([]any(nil), asSlice(old["k"])...)
	index := map[string]int{}
	for i, it := range items {
		index[it.([]any)[0].(string)] = i
	}
	for _, e := range entries {
		entry := e.([]any)
		key := entry[0].(string)
		i, ok := index[key]
		if !ok {
			return nil, fmt.Errorf("diff: update for unknown key %q", key)
		}
		dyns, err := applySlots(asSlice(items[i].([]any)[1]), entry[1].(map[string]any))
		if err != nil {
			return nil, err
		}
		items[i] = []any{key, dyns}
	}
	return map[string]any{"s": old["s"], "k": items}, nil
}

// applyOrder applies {"o": [...]}: the complete new list of items.
func applyOrder(old map[string]any, entries []any) (map[string]any, error) {
	byKey := map[string][]any{}
	for _, it := range asSlice(old["k"]) {
		item := it.([]any)
		byKey[item[0].(string)] = asSlice(item[1])
	}
	items := make([]any, 0, len(entries))
	for _, e := range entries {
		entry := e.([]any)
		key := entry[0].(string)
		var dyns []any
		switch {
		case len(entry) == 1: // unchanged
			d, ok := byKey[key]
			if !ok {
				return nil, fmt.Errorf("diff: unknown key %q", key)
			}
			dyns = d
		default:
			switch v := entry[1].(type) {
			case []any: // new item
				dyns = v
			case map[string]any: // changed item
				d, ok := byKey[key]
				if !ok {
					return nil, fmt.Errorf("diff: patch for unknown key %q", key)
				}
				var err error
				if dyns, err = applySlots(d, v); err != nil {
					return nil, err
				}
			}
		}
		items = append(items, []any{key, dyns})
	}
	return map[string]any{"s": old["s"], "k": items}, nil
}

// asSlice returns v as a []any, accepting the []string that Full uses for
// statics and nil for a block without dynamics.
func asSlice(v any) []any {
	switch v := v.(type) {
	case []any:
		return v
	case nil:
		return nil
	}
	panic(fmt.Sprintf("diff: expected a list, got %T", v))
}

// HTML renders an encoded tree as HTML, as the client would.
func HTML(tree map[string]any) string {
	var b strings.Builder
	writeHTML(&b, tree)
	return b.String()
}

func writeHTML(b *strings.Builder, v any) {
	switch v := v.(type) {
	case string:
		b.WriteString(v)
	case map[string]any:
		statics := toStrings(v["s"])
		if items, ok := v["k"]; ok {
			for _, it := range asSlice(items) {
				writeParts(b, statics, asSlice(it.([]any)[1]))
			}
			return
		}
		writeParts(b, statics, asSlice(v["d"]))
	}
}

func writeParts(b *strings.Builder, statics []string, dyns []any) {
	for i, s := range statics {
		b.WriteString(s)
		if i < len(dyns) {
			writeHTML(b, dyns[i])
		}
	}
}

func toStrings(v any) []string {
	switch v := v.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, len(v))
		for i, s := range v {
			out[i] = s.(string)
		}
		return out
	}
	return nil
}
