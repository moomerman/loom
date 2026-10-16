package loomtest_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/loomtest"
)

// sortPage reorders cards when its sortable behavior emits moved.
type sortPage struct{ cards []hxtest.Card }

func (p *sortPage) Mount(c *loom.Ctx) {
	p.cards = []hxtest.Card{{ID: 1, Title: "One"}, {ID: 2, Title: "Two"}, {ID: 3, Title: "Three"}}
}

func (p *sortPage) Assigns(c *loom.Ctx) hxtest.SortableAssigns {
	return hxtest.SortableAssigns{Cards: p.cards, Label: "Plan"}
}

func (p *sortPage) Events() loom.Events {
	nothing := func(*loom.Ctx, loom.Payload) {}
	return loom.Events{"move": p.move, "cancel": nothing, "pick": nothing}
}

func (p *sortPage) move(c *loom.Ctx, payload loom.Payload) {
	if payload["board"] == "plan" {
		id, _ := strconv.Atoi(payload["id"])
		to, _ := strconv.Atoi(payload["to"])
		i := slices.IndexFunc(p.cards, func(c hxtest.Card) bool { return c.ID == id })
		card := p.cards[i]
		p.cards = slices.Insert(slices.Delete(p.cards, i, i+1), to, card)
	}
}

func TestEmit(t *testing.T) {
	app := loom.New()
	app.Live("/", &sortPage{}, hxtest.Sortable)
	p := loomtest.Mount(t, app, "/", loomtest.NoLayout())
	p.Emit("ul[role=list]", "moved", map[string]any{"id": "1", "to": 2})
	if got := p.HTML(); !strings.Contains(got, ">Two</li><li") || strings.Index(got, ">One<") < strings.Index(got, ">Three<") {
		t.Errorf("after moving One to the end:\n%s", got)
	}
}
