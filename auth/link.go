package auth

import (
	"github.com/moomerman/loom"
)

// LinkPage, at /link/{token}, is where a sign-in link lands. It signs in
// only when the person presses the button, not on opening the link:
// mail scanners open links to check them, and would use them up. Using
// the link proves the email; for an email with no account (and sign-up
// on) it makes one, running Config.OnSignUp.
type LinkPage struct {
	auth    *App
	token   string
	email   string
	invalid bool // unknown, used or expired
}

func (p *LinkPage) Mount(c *loom.Ctx) {
	p.token = c.Request.PathValue("token")
	l, ok, err := p.auth.peekLink(c.Context(), kindSignIn, p.token)
	if err != nil {
		panic(err)
	}
	p.invalid, p.email = !ok, l.Email
	c.Title("Sign in")
}

func (p *LinkPage) Events() loom.Events {
	return loom.Events{"sign_in": p.signIn}
}

func (p *LinkPage) signIn(c *loom.Ctx, _ loom.Payload) {
	if p.invalid {
		return
	}
	a, ctx := p.auth, c.Context()
	var u User
	var claimed bool
	err := a.app.DB().Tx(ctx, func(tx loom.Tx) error {
		l, ok, err := a.takeLink(ctx, tx, kindSignIn, p.token)
		if err != nil || !ok {
			return err
		}
		u, err = a.UserByEmail(ctx, tx, l.Email)
		switch {
		case isNotFound(err) && !a.cfg.NoSignUp:
			u, err = a.signUp(ctx, tx, l.Email, "", true)
		case err == nil:
			u, claimed, err = a.verify(ctx, tx, u.ID)
		}
		return err
	})
	if err == nil && claimed {
		err = a.app.SignOutEverywhere(ctx, u.ID)
	}
	if isNotFound(err) || (err == nil && u.ID == "") {
		p.invalid = true
		return
	}
	if err != nil {
		panic(err)
	}
	a.signIn(c, u)
}

func (p *LinkPage) Assigns(c *loom.Ctx) LinkAssigns {
	return LinkAssigns{Email: p.email, Invalid: p.invalid}
}
