package auth

import (
	"errors"
	"fmt"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/form"
)

// SignUpPage, at /sign-up, makes an account with an email and password
// and signs it in. Config.OnSignUp runs in the same transaction, so the
// app's own rows for the user exist exactly when the account does.
type SignUpPage struct {
	auth   *App
	signUp form.Form[SignUpInput]
}

// SignUpInput is the sign-up form.
type SignUpInput struct {
	Email    string
	Password string `loom:"secret"`
}

func (in *SignUpInput) Validate(e *form.Errors) {
	if !validEmail(in.Email) {
		e.Add(&in.Email, "Enter your email address.")
	}
	checkNewPassword(e, &in.Password)
}

// checkNewPassword checks a new password's length, the one rule
// HashPassword has; long passphrases beat rules about symbols.
func checkNewPassword(e *form.Errors, password *string) {
	if len(*password) < MinPassword {
		e.Add(password, fmt.Sprintf("Use at least %d characters.", MinPassword))
	} else if len(*password) > MaxPassword {
		e.Add(password, fmt.Sprintf("Use at most %d characters.", MaxPassword))
	}
}

func (p *SignUpPage) Mount(c *loom.Ctx) {
	p.signUp = form.New(SignUpInput{})
	c.Title("Create an account")
}

func (p *SignUpPage) Events() loom.Events {
	return loom.Events{
		"change":  func(c *loom.Ctx, payload loom.Payload) { p.signUp.Change(payload) },
		"sign_up": p.submit,
	}
}

func (p *SignUpPage) submit(c *loom.Ctx, payload loom.Payload) {
	in, ok := p.signUp.Submit(payload)
	if !ok {
		return
	}
	var u User
	err := p.auth.app.DB().Tx(c.Context(), func(tx loom.Tx) error {
		var err error
		u, err = p.auth.signUp(c.Context(), tx, in.Email, in.Password, false)
		return err
	})
	if errors.Is(err, loom.ErrUniqueViolation) {
		p.signUp.AddError(&in.Email, "There's already an account for this email. Sign in instead.")
		return
	}
	if err != nil {
		panic(err)
	}
	p.auth.signIn(c, u)
}

func (p *SignUpPage) Assigns(c *loom.Ctx) SignUpAssigns {
	return SignUpAssigns{SignUp: p.signUp, Next: nextQuery(c)}
}
