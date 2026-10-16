package auth

import (
	"github.com/moomerman/loom"
	"github.com/moomerman/loom/form"
)

// SetUpPage, at /account/two-factor, sets up two-factor sign-in for the
// signed-in person: they scan a QR code into an authenticator app and
// type back a code from it, which turns it on and shows their recovery
// codes. The secret is stored before it is confirmed, so the page shows
// the same QR code after a reload or a restart.
type SetUpPage struct {
	auth   *App
	userID string
	email  string
	secret string // "" once two-factor is on
	codes  []string
	code   form.Form[CodeInput]
}

func (p *SetUpPage) Mount(c *loom.Ctx) {
	a, ctx := p.auth, c.Context()
	p.userID = c.UserID()
	u, err := a.User(ctx, p.userID)
	if err != nil {
		panic(err)
	}
	p.email = u.Email
	if p.secret, err = a.pendingTOTP(ctx, p.userID); err != nil {
		panic(err)
	}
	p.code = form.New(CodeInput{})
	c.Title("Set up two-factor sign-in")
}

func (p *SetUpPage) Events() loom.Events {
	return loom.Events{
		"change": func(c *loom.Ctx, payload loom.Payload) { p.code.Change(payload) },
		"set_up": p.setUp,
	}
}

func (p *SetUpPage) setUp(c *loom.Ctx, payload loom.Payload) {
	in, ok := p.code.Submit(payload)
	if !ok || p.secret == "" {
		return
	}
	codes, ok, err := p.auth.enableTOTP(c.Context(), p.userID, in.Code)
	if err != nil {
		panic(err)
	}
	if !ok {
		p.code.AddError(&in.Code, wrongCode)
		return
	}
	p.codes, p.secret = codes, ""
}

func (p *SetUpPage) Assigns(c *loom.Ctx) SetUpAssigns {
	as := SetUpAssigns{On: p.secret == "", Code: p.code, Codes: p.codes}
	if p.secret != "" {
		as.Qr, as.Secret = p.auth.authenticator(p.email, p.secret)
	}
	return as
}
