package auth

import (
	"net/url"
	"strings"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/form"
	"github.com/moomerman/loom/trace"
)

// SignInPage, at /sign-in, signs in with an email and password, mails
// a sign-in link to the email typed, or sends the browser to a provider
// (oauth.go), which comes back here with ?failed= if it didn't work. loom.SignedIn sends people
// here with ?next=, the page they asked for, and signing in goes there.
type SignInPage struct {
	auth   *App
	signIn form.Form[SignInInput]
	sent   string // the email a link was sent to
}

// SignInInput is the sign-in form.
type SignInInput struct {
	Email    string
	Password string `loom:"secret"`
}

// Validate checks the email only: the password is checked against the
// account, and "email me a link" needs no password.
func (in *SignInInput) Validate(e *form.Errors) {
	if !validEmail(in.Email) {
		e.Add(&in.Email, "Enter your email address.")
	}
}

func (p *SignInPage) Mount(c *loom.Ctx) {
	p.signIn = form.New(SignInInput{})
	c.Title("Sign in")
}

func (p *SignInPage) Events() loom.Events {
	return loom.Events{
		"change":     func(c *loom.Ctx, payload loom.Payload) { p.signIn.Change(payload) },
		"sign_in":    p.submit,
		"email_link": p.emailLink,
	}
}

// submit checks the password. A wrong email and a wrong password get the
// same answer, in the same time.
func (p *SignInPage) submit(c *loom.Ctx, payload loom.Payload) {
	in, ok := p.signIn.Submit(payload)
	if !ok {
		return
	}
	u, err := p.auth.checkPassword(c.Context(), in.Email, in.Password)
	if err == ErrWrongPassword {
		// No user: a wrong email and a wrong password look the same.
		p.auth.app.Emit(c.Context(), trace.AuthSignInFailed, "method", "password", "reason", "wrong email or password")
		p.signIn.AddError(&in.Password, "Wrong email or password.")
		return
	}
	if err != nil {
		panic(err)
	}
	p.auth.signIn(c, u)
}

// emailLink mails a sign-in link to the email in the form, which the
// form's change events keep up to date (Submit(nil) checks the fields
// as they are).
func (p *SignInPage) emailLink(c *loom.Ctx, _ loom.Payload) {
	in, ok := p.signIn.Submit(nil)
	if !ok {
		return
	}
	if err := p.auth.sendLink(c, kindSignIn, in.Email); err != nil {
		panic(err)
	}
	p.sent = in.Email
}

func (p *SignInPage) Assigns(c *loom.Ctx) SignInAssigns {
	a := SignInAssigns{SignIn: p.signIn, Sent: p.sent, Email: !p.auth.cfg.NoEmail, SignUp: !p.auth.cfg.NoSignUp, Next: nextQuery(c)}
	q := c.Request.URL.Query()
	for _, pr := range p.auth.cfg.Providers {
		a.Providers = append(a.Providers, ProviderButton{Name: pr.Name, Label: "Continue with " + pr.Label})
		if q.Get("with") == pr.Name && q.Get("failed") != "" {
			a.Failed = providerMessage(pr.Label, q.Get("failed"))
		}
	}
	return a
}

// ProviderButton is a "Continue with Google" link on the sign-in page.
type ProviderButton struct {
	Name  string
	Label string
}

// validEmail is a light check: the address is proved by mailing it.
func validEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	return at > 0 && at < len(s)-1 && !strings.ContainsAny(s, " \t\r\n")
}

// nextQuery is the page's ?next=, to pass along to the other pages.
func nextQuery(c *loom.Ctx) string {
	if next := c.Request.URL.Query().Get("next"); loom.LocalPath(next) {
		return "?next=" + url.QueryEscape(next)
	}
	return ""
}
