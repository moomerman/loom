// Package signintest is a tiny app for testing sign-in: a sign-in page
// that checks a password and signs in, and pages that require a
// signed-in user and read it typed.
package signintest

//go:generate go run github.com/moomerman/loom/cmd/loom build .

import (
	"context"

	"github.com/moomerman/loom"
)

// User is the app's user type.
type User struct{ ID, Name, Password string }

// Users are the app's users, by id.
type Users map[string]*User

// Setup adds the pages to app.
func Setup(app *loom.App, users Users) {
	loom.Users(app, func(ctx context.Context, id string) (*User, error) {
		if u := users[id]; u != nil {
			return u, nil
		}
		return nil, loom.ErrNotFound
	})
	app.Live("/login", &LoginPage{users: users}, Login)
	signedIn := app.Group(loom.SignedIn("/login"))
	signedIn.Live("/", &HomePage{}, Home)
	signedIn.Live("/other", &HomePage{}, Home)
	// A mistake to catch: loom.User on a route that doesn't require
	// sign-in (RT430).
	app.Live("/open", &HomePage{}, Home)
}

// LoginPage checks a name and password.
type LoginPage struct {
	users Users
	error string
}

func (p *LoginPage) Mount(c *loom.Ctx) {}

func (p *LoginPage) Assigns(c *loom.Ctx) LoginAssigns { return LoginAssigns{Error: p.error} }

func (p *LoginPage) Events() loom.Events { return loom.Events{"sign_in": p.signIn} }

func (p *LoginPage) signIn(c *loom.Ctx, payload loom.Payload) {
	u := p.users[payload["name"]]
	if u == nil || u.Password != payload["password"] {
		p.error = "Wrong name or password."
		return
	}
	c.SignIn(u.ID, "/")
}

// HomePage shows who is signed in.
type HomePage struct{ me *User }

func (p *HomePage) Mount(c *loom.Ctx) { p.me = loom.User[*User](c) }

func (p *HomePage) Assigns(c *loom.Ctx) HomeAssigns { return HomeAssigns{Name: p.me.Name} }

func (p *HomePage) Events() loom.Events {
	return loom.Events{
		"ping":     func(*loom.Ctx, loom.Payload) {},
		"sign_out": func(c *loom.Ctx, _ loom.Payload) { c.SignOut("/login") },
	}
}
