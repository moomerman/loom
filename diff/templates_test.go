package diff

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/moomerman/loom/internal/difftest"
	"github.com/moomerman/loom/internal/hxtest"
)

func randomTodos(r *rand.Rand) []hxtest.Todo {
	var todos []hxtest.Todo
	for _, k := range r.Perm(8)[:r.Intn(8)] {
		todos = append(todos, hxtest.Todo{ID: fmt.Sprint(k), Title: difftest.Words[r.Intn(len(difftest.Words))], Done: r.Intn(2) == 0})
	}
	return todos
}

// TestGeneratedTemplates checks the diff property on real generated code.
func TestGeneratedTemplates(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	filters := []string{"all", "active"}
	for i := 0; i < 500; i++ {
		old := hxtest.TodoList.Render(nil, hxtest.TodoListAssigns{Todos: randomTodos(r), Filter: filters[r.Intn(2)]})
		new := hxtest.TodoList.Render(nil, hxtest.TodoListAssigns{Todos: randomTodos(r), Filter: filters[r.Intn(2)]})
		checkApply(t, old, new)

		var cols [][]hxtest.Card
		for c := 0; c < r.Intn(3); c++ {
			var col []hxtest.Card
			for _, id := range r.Perm(5)[:r.Intn(5)] {
				col = append(col, hxtest.Card{ID: id, Title: difftest.Words[r.Intn(len(difftest.Words))], Tags: []string{"x"}[:r.Intn(2)]})
			}
			cols = append(cols, col)
		}
		b1 := hxtest.Board.Render(nil, hxtest.BoardAssigns{Columns: cols, Title: "t", Show: r.Intn(2) == 0})
		b2 := hxtest.Board.Render(nil, hxtest.BoardAssigns{Columns: cols[:len(cols)/2], Title: "u", Show: r.Intn(2) == 0})
		checkApply(t, b1, b2)
	}
}

// TestToggleSendsOnlyTheChange is wire.md's example: toggling one todo
// sends only that item's changed slots, and no statics.
func TestToggleSendsOnlyTheChange(t *testing.T) {
	todos := []hxtest.Todo{{ID: "a", Title: "Buy milk"}, {ID: "b", Title: "Walk dog"}}
	old := hxtest.TodoList.Render(nil, hxtest.TodoListAssigns{Todos: todos, Filter: "all"})
	todos2 := append([]hxtest.Todo(nil), todos...)
	todos2[0].Done = true
	new := hxtest.TodoList.Render(nil, hxtest.TodoListAssigns{Todos: todos2, Filter: "all"})

	patch := checkApply(t, old, new)
	got := mustJSON(t, patch)
	want := `{"d":{"0":{"k":[["a",{"1":" class=\"done\"","2":" checked"}]]},"1":{"d":{"2":{"d":{"0":"50%"}}}}}}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if strings.Contains(got, `"s"`) {
		t.Error("diff resends statics")
	}
}

func BenchmarkDiffLargeList(b *testing.B) {
	todos := make([]hxtest.Todo, 1000)
	for i := range todos {
		todos[i] = hxtest.Todo{ID: fmt.Sprint(i), Title: fmt.Sprint("todo ", i)}
	}
	old := hxtest.TodoList.Render(nil, hxtest.TodoListAssigns{Todos: todos, Filter: "all"})
	todos[500].Done = true
	new := hxtest.TodoList.Render(nil, hxtest.TodoListAssigns{Todos: todos, Filter: "all"})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Diff(old, new); err != nil {
			b.Fatal(err)
		}
	}
}
