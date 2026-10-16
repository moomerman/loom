package form

import "fmt"

// Form error codes, documented in docs/errors.md.
const (
	FM801 = "FM801" // a form's input type can't be decoded: not a struct, or a field of an unsupported type
	FM802 = "FM802" // an input's Validate method has the wrong signature or a value receiver
	FM803 = "FM803" // Errors.Add or AddError named something that isn't one of the input's fields
)

// Error is a form error: what is wrong and how to fix it.
type Error struct {
	Code string
	Msg  string
	Fix  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("error %s: %s\n  fix: %s", e.Code, e.Msg, e.Fix)
}
