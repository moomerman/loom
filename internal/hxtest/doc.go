// Package hxtest holds templates that exercise every template feature.
// Its generated *_hx.go files are golden files for the code generator
// (see compiler/gogen_test.go), and its tests check the rendered HTML.
package hxtest

// Todo is an item in a todo list.
type Todo struct {
	ID    string
	Title string
	Done  bool
}

// Card is shown on a Board.
type Card struct {
	ID       int
	Title    string
	Priority float64
	Tags     []string
}

// User shows field naming: Go names map to snake_case template names.
type User struct {
	Name      string
	UserID    int
	HTMLBio   string
	CreatedAt string
	Address   Address
}

// Address is a nested struct.
type Address struct {
	City string
}

// Shout upper-cases s and adds an exclamation mark.
//
//loom:helper
func Shout(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'a' <= c && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b) + "!"
}

// Percent formats a ratio as a whole percentage.
//
//loom:helper
func Percent(done, total int) string {
	if total == 0 {
		return "0%"
	}
	return itoa(done*100/total) + "%"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// DoneCount counts the finished todos.
//
//loom:helper
func DoneCount(todos []Todo) int {
	n := 0
	for _, t := range todos {
		if t.Done {
			n++
		}
	}
	return n
}

// Str formats an int as a string.
//
//loom:helper
func Str(n int) string { return itoa(n) }

// KeysOf lists cards' IDs, as strings, for the sortable behavior.
//
//loom:helper
func KeysOf(cards []Card) []string {
	ids := make([]string, len(cards))
	for i, c := range cards {
		ids[i] = itoa(c.ID)
	}
	return ids
}
