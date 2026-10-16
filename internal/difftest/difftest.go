// Package difftest makes random render trees for property tests of the
// diff engine (package diff).
package difftest

import (
	"fmt"
	"math/rand"

	"github.com/moomerman/loom/render"
)

// A schema is a random template: statics plus a kind for each dynamic
// slot. Trees generated from one schema share statics, like two renders of
// one compiled template.

type slotKind int

const (
	kText  slotKind = iota
	kIf             // Empty, or a nested block
	kList           // a keyed :for
	kBlock          // a nested block that is always present (a component call)
)

type schema struct {
	statics *render.Statics
	slots   []slotKind
	nested  []*schema // for kIf, kList and kBlock slots
}

var schemaID int

func newSchema(r *rand.Rand, depth int) *schema {
	n := r.Intn(4)
	s := &schema{slots: make([]slotKind, n), nested: make([]*schema, n)}
	parts := make([]string, n+1)
	for i := range parts {
		parts[i] = fmt.Sprintf("<s%d>", i)
	}
	schemaID++
	s.statics = &render.Statics{ID: fmt.Sprint("T.", schemaID), Parts: parts}
	for i := range s.slots {
		k := kText
		if depth < 3 {
			k = slotKind(r.Intn(4))
		}
		s.slots[i] = k
		if k != kText {
			s.nested[i] = newSchema(r, depth+1)
		}
	}
	return s
}

// Words are the texts trees are made of.
var Words = []string{"a", "b", "milk", "<b>", "", "dog"}

func (s *schema) render(r *rand.Rand) *render.Rendered {
	return &render.Rendered{Statics: s.statics, Dynamics: s.dynamics(r)}
}

func (s *schema) dynamics(r *rand.Rand) []render.Dynamic {
	dyns := make([]render.Dynamic, len(s.slots))
	for i, k := range s.slots {
		switch k {
		case kText:
			dyns[i] = render.Text(Words[r.Intn(len(Words))])
		case kIf:
			if r.Intn(2) == 0 {
				dyns[i] = render.Empty
			} else {
				dyns[i] = s.nested[i].render(r)
			}
		case kBlock:
			dyns[i] = s.nested[i].render(r)
		case kList:
			l := &render.List{Statics: s.nested[i].statics}
			for _, key := range r.Perm(6)[:r.Intn(6)] {
				l.Items = append(l.Items, render.Item{Key: fmt.Sprint("k", key), Dynamics: s.nested[i].dynamics(r)})
			}
			dyns[i] = l
		}
	}
	return dyns
}

// mutate returns a copy of t with a few random changes, so diffs mix
// unchanged, changed, moved, inserted and deleted parts.
func (s *schema) mutate(r *rand.Rand, t *render.Rendered) *render.Rendered {
	return &render.Rendered{Statics: t.Statics, Dynamics: s.mutateDynamics(r, t.Dynamics)}
}

func (s *schema) mutateDynamics(r *rand.Rand, dyns []render.Dynamic) []render.Dynamic {
	out := append([]render.Dynamic(nil), dyns...)
	for i, d := range dyns {
		if r.Intn(4) == 0 {
			out[i] = s.dynamics(r)[i] // replace with a fresh value
			continue
		}
		switch d := d.(type) {
		case *render.Rendered:
			out[i] = s.nested[i].mutate(r, d)
		case *render.List:
			items := append([]render.Item(nil), d.Items...)
			r.Shuffle(len(items), func(a, b int) {
				if r.Intn(2) == 0 {
					items[a], items[b] = items[b], items[a]
				}
			})
			for j := range items {
				if r.Intn(3) == 0 {
					items[j] = render.Item{Key: items[j].Key, Dynamics: s.nested[i].mutateDynamics(r, items[j].Dynamics)}
				}
			}
			if len(items) > 0 && r.Intn(3) == 0 {
				items = items[1:]
			}
			out[i] = &render.List{Statics: d.Statics, Items: items}
		}
	}
	return out
}

// Pairs returns n pairs of trees from one template each, from seed: half
// unrelated renders, half a render and a few changes to it.
func Pairs(seed int64, n int) [][2]*render.Rendered {
	r := rand.New(rand.NewSource(seed))
	out := make([][2]*render.Rendered, n)
	for i := range out {
		s := newSchema(r, 0)
		old := s.render(r)
		if i%2 == 0 {
			out[i] = [2]*render.Rendered{old, s.render(r)}
		} else {
			out[i] = [2]*render.Rendered{old, s.mutate(r, old)}
		}
	}
	return out
}
