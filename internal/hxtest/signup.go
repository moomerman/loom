package hxtest

import "github.com/moomerman/loom/form"

// SignupInput is what the signup form decodes to; its fields are the
// form's fields.
type SignupInput struct {
	Name   string
	Age    int
	Agree  bool
	Shares map[string]string
}

// Validate checks the input; the form shows the errors.
func (in *SignupInput) Validate(e *form.Errors) {
	if in.Name == "" {
		e.Add(&in.Name, "Enter a name.")
	}
}
