package ast

import (
	"fmt"
	"strconv"
	"strings"
)

// Dump returns a readable, indented rendering of the tree, used by golden
// tests. Expressions are printed as s-expressions, e.g. (|| (. t done) x).
func Dump(f *File) string {
	d := &dumper{}
	for _, imp := range f.Imports {
		d.line(0, "Import %q @%s", imp.Path, imp.Pos)
	}
	for _, b := range f.Behaviors {
		d.line(0, "Behavior %s @%s", b.Name, b.Pos)
		for _, p := range b.Params {
			d.param(1, p)
		}
		for _, e := range b.Emits {
			d.line(1, "Emits %s @%s", e.Name, e.Pos)
			for _, p := range e.Params {
				d.param(2, p)
			}
		}
		for _, r := range b.Requires {
			d.line(1, "Requires %s @%s", r.Selector(), r.Pos)
		}
	}
	for _, c := range f.Components {
		d.line(0, "Component %s @%s", c.Name, c.Pos)
		for _, p := range c.Params {
			d.param(1, p)
		}
		for _, st := range c.Styles {
			d.node(1, st)
		}
		d.nodes(1, c.Body)
	}
	return d.b.String()
}

type dumper struct{ b strings.Builder }

func (d *dumper) param(depth int, p *Param) {
	def := ""
	if p.Default != nil {
		def = " = " + ExprString(p.Default)
	}
	if len(p.Previews) > 0 {
		var vs []string
		for _, x := range p.Previews {
			vs = append(vs, ExprString(x))
		}
		def += " @preview(" + strings.Join(vs, ", ") + ")"
	}
	d.line(depth, "Param %s %s%s @%s", p.Name, TypeString(p.Type), def, p.Pos)
}

func (d *dumper) line(depth int, format string, args ...any) {
	d.b.WriteString(strings.Repeat("  ", depth))
	fmt.Fprintf(&d.b, format, args...)
	d.b.WriteByte('\n')
}

func (d *dumper) nodes(depth int, nodes []Node) {
	for _, n := range nodes {
		d.node(depth, n)
	}
}

func (d *dumper) node(depth int, n Node) {
	switch n := n.(type) {
	case *Text:
		d.line(depth, "Text %s @%s", strconv.Quote(n.Value), n.Pos)
	case *Interp:
		d.line(depth, "Interp %s @%s", ExprString(n.X), n.Pos)
	case *Element:
		void := ""
		if n.Void {
			void = " void"
		}
		d.line(depth, "Element <%s>%s @%s", n.Tag, void, n.Pos)
		d.directives(depth+1, n.Directives)
		if n.Use != nil {
			var props []string
			for _, p := range n.Use.Props {
				props = append(props, p.Name+": "+ExprString(p.X))
			}
			d.line(depth+1, "use:%s {%s} @%s", n.Use.Name, strings.Join(props, ", "), n.Use.Pos)
		}
		d.attrs(depth+1, n.Attrs)
		d.nodes(depth+1, n.Children)
	case *ComponentCall:
		d.line(depth, "Call <.%s> @%s", n.Name, n.Pos)
		d.directives(depth+1, n.Directives)
		d.attrs(depth+1, n.Attrs)
		for _, s := range n.Slots {
			d.node(depth+1, s)
		}
		d.nodes(depth+1, n.Children)
	case *Slot:
		d.line(depth, "Slot <:%s> @%s", n.Name, n.Pos)
		d.attrs(depth+1, n.Attrs)
		d.nodes(depth+1, n.Children)
	default:
		d.line(depth, "?%T", n)
	}
}

func (d *dumper) directives(depth int, dir Directives) {
	if dir.For != nil {
		d.line(depth, ":for %s <- %s @%s", dir.For.Var, ExprString(dir.For.List), dir.For.Pos)
	}
	if dir.If != nil {
		d.line(depth, ":if %s", ExprString(dir.If))
	}
	if dir.Key != nil {
		d.line(depth, ":key %s", ExprString(dir.Key))
	}
}

func (d *dumper) attrs(depth int, attrs []*Attr) {
	for _, a := range attrs {
		switch a.Kind {
		case AttrBare:
			d.line(depth, "Attr %s @%s", a.Name, a.Pos)
		case AttrString:
			d.line(depth, "Attr %s=%s @%s", a.Name, strconv.Quote(a.Value), a.Pos)
		case AttrExpr:
			d.line(depth, "Attr %s={%s} @%s", a.Name, ExprString(a.X), a.Pos)
		}
	}
}

// TypeString renders a type as written in a signature, e.g. `[]Todo`.
func TypeString(t Type) string {
	switch t := t.(type) {
	case *NamedType:
		if t.Arg != nil {
			return t.Name + "[" + TypeString(t.Arg) + "]"
		}
		return t.Name
	case *ListType:
		return "[]" + TypeString(t.Elem)
	case *EnumType:
		return ":" + strings.Join(t.Values, " | :")
	}
	return "?"
}

// ExprString renders an expression as an s-expression.
func ExprString(e Expr) string {
	switch e := e.(type) {
	case *Ident:
		return e.Name
	case *IntLit:
		return strconv.FormatInt(e.Value, 10)
	case *FloatLit:
		return strconv.FormatFloat(e.Value, 'g', -1, 64)
	case *StringLit:
		return strconv.Quote(e.Value)
	case *BoolLit:
		return strconv.FormatBool(e.Value)
	case *Field:
		return "(. " + ExprString(e.X) + " " + e.Name + ")"
	case *Index:
		return "([] " + ExprString(e.X) + " " + ExprString(e.Index) + ")"
	case *Call:
		parts := []string{e.Func.Name}
		for _, a := range e.Args {
			parts = append(parts, ExprString(a))
		}
		return "(call " + strings.Join(parts, " ") + ")"
	case *Unary:
		return "(" + e.Op + " " + ExprString(e.X) + ")"
	case *Binary:
		return "(" + e.Op + " " + ExprString(e.X) + " " + ExprString(e.Y) + ")"
	case *Cond:
		if e.Else == nil {
			return "(if " + ExprString(e.Cond) + " " + ExprString(e.Then) + ")"
		}
		return "(if " + ExprString(e.Cond) + " " + ExprString(e.Then) + " " + ExprString(e.Else) + ")"
	case *ClassList:
		parts := []string{"class"}
		for _, x := range e.Items {
			parts = append(parts, ExprString(x))
		}
		return "(" + strings.Join(parts, " ") + ")"
	case *Atom:
		return ":" + e.Name
	}
	return fmt.Sprintf("?%T", e)
}
