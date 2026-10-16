package auth

import (
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/form"
)

// ForgotPage, at /forgot, mails a link to choose a new password. It says
// the same whether or not the email has an account, so it can't be used
// to find out who has one.
type ForgotPage struct {
	auth   *App
	forgot form.Form[ForgotInput]
	sent   string
}

// ForgotInput is the forgot-password form.
type ForgotInput struct {
	Email string
}

func (in *ForgotInput) Validate(e *form.Errors) {
	if !validEmail(in.Email) {
		e.Add(&in.Email, "Enter your email address.")
	}
}

func (p *ForgotPage) Mount(c *loom.Ctx) {
	p.forgot = form.New(ForgotInput{})
	c.Title("Reset your password")
}

func (p *ForgotPage) Events() loom.Events {
	return loom.Events{
		"change": func(c *loom.Ctx, payload loom.Payload) { p.forgot.Change(payload) },
		"send":   p.send,
	}
}

func (p *ForgotPage) send(c *loom.Ctx, payload loom.Payload) {
	in, ok := p.forgot.Submit(payload)
	if !ok {
		return
	}
	if err := p.auth.sendLink(c, kindReset, in.Email); err != nil {
		panic(err)
	}
	p.sent = in.Email
}

func (p *ForgotPage) Assigns(c *loom.Ctx) ForgotAssigns {
	return ForgotAssigns{Forgot: p.forgot, Sent: p.sent}
}
