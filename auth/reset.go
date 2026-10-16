package auth

import (
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/form"
	"github.com/moomerman/loom/trace"
)

// ResetPage, at /reset/{token}, is where a password-reset link lands:
// the person chooses a new password. Saving it uses the link, signs out
// every other session of the account, proves the email (the link came
// by mail) and signs in.
type ResetPage struct {
	auth    *App
	token   string
	email   string
	invalid bool // unknown, used or expired
	reset   form.Form[ResetInput]
}

// ResetInput is the new-password form.
type ResetInput struct {
	Password string `loom:"secret"`
}

func (in *ResetInput) Validate(e *form.Errors) { checkNewPassword(e, &in.Password) }

func (p *ResetPage) Mount(c *loom.Ctx) {
	p.token = c.Request.PathValue("token")
	l, ok, err := p.auth.peekLink(c.Context(), kindReset, p.token)
	if err != nil {
		panic(err)
	}
	p.invalid, p.email = !ok, l.Email
	p.reset = form.New(ResetInput{})
	c.Title("Choose a new password")
}

func (p *ResetPage) Events() loom.Events {
	return loom.Events{
		"change": func(c *loom.Ctx, payload loom.Payload) { p.reset.Change(payload) },
		"save":   p.save,
	}
}

func (p *ResetPage) save(c *loom.Ctx, payload loom.Payload) {
	in, ok := p.reset.Submit(payload)
	if !ok || p.invalid {
		return
	}
	a, ctx := p.auth, c.Context()
	var u User
	err := a.app.DB().Tx(ctx, func(tx loom.Tx) error {
		l, ok, err := a.takeLink(ctx, tx, kindReset, p.token)
		if err != nil || !ok {
			return err
		}
		if u, err = a.UserByEmail(ctx, tx, l.Email); err != nil {
			return err
		}
		// Verify first: on an account whose email was never proved it
		// clears the old password, and the new one is set after.
		if _, _, err := a.verify(ctx, tx, u.ID); err != nil {
			return err
		}
		if err := a.setPassword(ctx, tx, u.ID, in.Password); err != nil {
			return err
		}
		return a.deleteLinks(ctx, tx, l.Email)
	})
	if isNotFound(err) || (err == nil && u.ID == "") {
		p.invalid = true // used meanwhile, or the account is gone
		return
	}
	if err != nil {
		panic(err)
	}
	if err := a.app.SignOutEverywhere(ctx, u.ID); err != nil {
		panic(err)
	}
	a.app.Emit(ctx, trace.AuthReset, "user", u.ID)
	a.signIn(c, u)
}

func (p *ResetPage) Assigns(c *loom.Ctx) ResetAssigns {
	return ResetAssigns{Reset: p.reset, Email: p.email, Invalid: p.invalid}
}
