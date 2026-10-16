package gogen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/moomerman/loom/compiler/ast"
	"github.com/moomerman/loom/compiler/check"
)

// maxPreviews caps a component's previews. Up to the cap the gallery
// shows every combination of the varying parameters; beyond it, the
// first sample and then each other value of each parameter on its own.
const maxPreviews = 24

// A previewAxis is one parameter's values in the previews: every value
// of an enum, both bools, or the @preview samples. The first is used
// whenever another parameter varies.
type previewAxis struct {
	field  string
	values []previewValue
}

type previewValue struct {
	expr  string // Go expression
	label string // as in the preview's name
}

// previews returns the Go composite literals of a component's previews,
// or the reason it has none.
func (g *gen) previews() (samples []string, reason string) {
	c := g.comp
	var axes []previewAxis
	for _, prm := range c.Params {
		var vs []previewValue
		switch {
		case prm.Type.Kind == check.Event, prm.Type.Kind == check.FormField:
			continue // no handler; no field
		case prm.Type.Kind == check.Enum:
			first := prm.Type.Enum.Values[0]
			if a, ok := prm.Default.(*ast.Atom); ok {
				first = a.Name
			}
			vs = append(vs, previewValue{prm.Type.Enum.GoConst(first), prm.Name + "=" + first})
			for _, v := range prm.Type.Enum.Values {
				if v != first {
					vs = append(vs, previewValue{prm.Type.Enum.GoConst(v), prm.Name + "=" + v})
				}
			}
		case prm.Type.Kind == check.Bool:
			first := false
			if b, ok := prm.Default.(*ast.BoolLit); ok {
				first = b.Value
			}
			for _, b := range []bool{first, !first} {
				label := prm.Name
				if !b {
					label = "not " + prm.Name
				}
				vs = append(vs, previewValue{strconv.FormatBool(b), label})
			}
		case len(prm.Previews) > 0:
			if prm.Default != nil {
				// An optional parameter's samples come after its default,
				// so the previews show it both left out and given.
				vs = append(vs, previewValue{g.defaultValue(prm, ""), prm.Name + "=" + ast.ExprString(prm.Default)})
			}
			for _, x := range prm.Previews {
				vs = append(vs, previewValue{g.previewExpr(prm, x), prm.Name + "=" + ast.ExprString(x)})
			}
		case prm.Default != nil:
			vs = append(vs, previewValue{g.defaultValue(prm, ""), ""})
		case prm.Type.Kind == check.Slot:
			continue // empty
		case prm.Type.Kind == check.String || prm.Type.Kind == check.Int || prm.Type.Kind == check.Float:
			return nil, fmt.Sprintf("give %s sample values with @preview, e.g. %s %s %s", prm.Name, prm.Name, prm.Type, previewHint(prm.Type))
		default:
			return nil, fmt.Sprintf("%s is %s, which @preview can't give a value; preview a component that builds one", prm.Name, prm.Type)
		}
		axes = append(axes, previewAxis{prm.GoName, vs})
	}

	total := 1
	for _, a := range axes {
		total *= len(a.values)
	}
	var combos [][]int // for each sample, the index of each axis's value
	if total <= maxPreviews {
		combos = [][]int{make([]int, len(axes))}
		for i := len(axes) - 1; i >= 0; i-- {
			var next [][]int
			for v := range axes[i].values {
				for _, c := range combos {
					c2 := append([]int(nil), c...)
					c2[i] = v
					next = append(next, c2)
				}
			}
			combos = next
		}
	} else {
		combos = [][]int{make([]int, len(axes))}
		for i, a := range axes {
			for v := 1; v < len(a.values); v++ {
				c := make([]int, len(axes))
				c[i] = v
				combos = append(combos, c)
			}
		}
	}
	for _, combo := range combos {
		var fields, labels []string
		for i, a := range axes {
			v := a.values[combo[i]]
			fields = append(fields, a.field+": "+v.expr)
			if combo[i] != 0 {
				labels = append(labels, v.label)
			}
		}
		name := strings.Join(labels, " ")
		if name == "" {
			name = "default"
		}
		samples = append(samples, fmt.Sprintf("{Name: %q, Assigns: %sAssigns{%s}}", name, c.Name, strings.Join(fields, ", ")))
	}
	return samples, ""
}

// previewExpr is the Go value of a @preview sample.
func (g *gen) previewExpr(prm *check.Param, x ast.Expr) string {
	if prm.Type.Kind == check.Slot {
		return "render.SampleSlot(" + strconv.Quote(x.(*ast.StringLit).Value) + ")"
	}
	if u, ok := x.(*ast.Unary); ok {
		return "-" + g.expr(u.X)
	}
	return g.expr(x)
}

func previewHint(t *check.Type) string {
	switch t.Kind {
	case check.Int:
		return "@preview(1, 20)"
	case check.Float:
		return "@preview(0.5, 2.0)"
	}
	return `@preview("Save")`
}
