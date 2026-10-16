// Package templates holds the todo example's templates and the types they
// render. Run `go generate` in examples/todo after editing a .hx file.
package templates

// Todo is one item on the list, a row of the todos table.
type Todo struct {
	ID         string // a UUID
	Title      string
	Done       bool
	InsertedAt string // RFC 3339, in UTC: 2026-01-31T09:30:00Z
}

// User is the person using the app, shown in the app layout.
type User struct {
	Name string
}

// TodoPath is the URL of a todo's page.
//
//loom:helper
func TodoPath(id string) string {
	return "/todos/" + id
}
