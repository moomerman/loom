package tally

import "github.com/moomerman/loom"

// LoginPage lists everyone to sign in as. There are no passwords: each
// person is a form that posts to /login, a plain handler that signs in
// with app.SignIn.
type LoginPage struct {
	app    *App
	people []Person
}

func (p *LoginPage) Mount(c *loom.Ctx) {
	c.Title("Sign in")
	people, err := p.app.store.People(c.Context())
	must(err)
	p.people = people
}

func (p *LoginPage) Assigns(c *loom.Ctx) LoginAssigns {
	return LoginAssigns{People: p.people}
}

func (p *LoginPage) Events() loom.Events { return nil }
