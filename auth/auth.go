// Package auth is a sign-in sub-app: email and password, sign-in links
// by email, password resets, Google and GitHub, two-factor sign-in with
// an authenticator app, and an account page, mounted with one line.
// It keeps accounts in the app's database (auth_users and auth_links,
// installed with `loom db install`), signs people in with Loom's
// sessions (docs/auth.md), and mails through the app's loom.Mailer from
// a background job, so a failed send is retried and shows in the jobs
// dashboard.
//
//	signIn := auth.New(auth.Config{AppName: "Tally"})
//	app.Mount("/auth", signIn)
//	app.UseMailer(myMailer)
//	signedIn := app.Group(loom.SignedIn("/auth/sign-in"))
//
// The pages use the host app's root layout and take its design tokens,
// so they look like the app. See docs/auth.md.
//
//loom:package
package auth

//go:generate go run github.com/moomerman/loom/cmd/loom build . ./ui

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/jobs"
)

// Config configures sign-in. The zero value has every page, and needs a
// mailer and, outside dev, the app's loom.Config.BaseURL, where mailed
// links and provider callbacks point. It is never taken from a request
// in production: the Host header is the sender's to choose.
type Config struct {
	// AppName names the app on the pages and in mail: "Sign in to
	// Tally". Default: the app's Title.
	AppName string
	// AfterSignIn returns where a person goes after signing in when the
	// sign-in page has no ?next= (loom.SignedIn sets one). Default "/".
	AfterSignIn func(u User) string
	// AfterSignOut is where a person goes after signing out. Default "/".
	AfterSignOut string
	// OnSignUp runs when an account is created, in the same transaction,
	// so the app makes its own rows for the user (a profile, a team)
	// exactly when the account exists. An error cancels the sign-up.
	OnSignUp func(ctx context.Context, tx loom.Tx, u User) error
	// NoSignUp turns off sign-up: only existing accounts (seeded, or made
	// with CreateUser) sign in. A sign-in link to an unknown email is
	// then not sent.
	NoSignUp bool
	// NoEmail turns off everything that sends mail, sign-in links and
	// password resets, so the app needs no mailer.
	NoEmail bool
	// LinkFor is how long a mailed link works. Default 30 minutes.
	LinkFor time.Duration
	// Providers are other sites people can sign in with, as buttons on
	// the sign-in page: auth.Google(id, secret), auth.GitHub(id, secret).
	// Each needs the app's BaseURL outside dev, for its callback
	// (AU902), and a client ID and secret (AU906).
	Providers []Provider
	// TwoFactor is whether people also type a code from an authenticator
	// app when they sign in, however they sign in: TwoFactorOptional (the
	// default: each person turns it on from the account page),
	// TwoFactorRequired (everyone sets it up at their next sign-in) or
	// TwoFactorOff.
	TwoFactor TwoFactor
}

// TwoFactor is the app's policy for two-factor sign-in (Config.TwoFactor).
type TwoFactor int

const (
	// TwoFactorOptional offers two-factor sign-in on the account page;
	// those who turn it on are asked for a code at every sign-in.
	TwoFactorOptional TwoFactor = iota
	// TwoFactorRequired asks everyone for a code at every sign-in, and
	// has anyone without an authenticator set one up before they are
	// signed in. It can't be turned off on the account page.
	TwoFactorRequired
	// TwoFactorOff never asks for a code, nor offers it. Authenticators
	// people set up before are kept, for when it is turned back on.
	TwoFactorOff
)

// App is the mountable sign-in sub-app. Create it with New and mount it
// with Group.Mount.
type App struct {
	cfg    Config
	app    *loom.App // set by Routes
	prefix string    // the mount prefix, e.g. "/auth"
}

// New returns the sign-in sub-app. An invalid Config is AU903.
func New(cfg Config) *App {
	if cfg.AfterSignOut == "" {
		cfg.AfterSignOut = "/"
	}
	if cfg.LinkFor == 0 {
		cfg.LinkFor = 30 * time.Minute
	}
	if !loom.LocalPath(cfg.AfterSignOut) || cfg.LinkFor < 0 {
		panic(&loom.Error{Code: AU903, Msg: fmt.Sprintf("auth.Config has AfterSignOut %q and LinkFor %s", cfg.AfterSignOut, cfg.LinkFor),
			Fix: `AfterSignOut is a path in the app, like "/"; LinkFor is positive, or zero for 30 minutes`})
	}
	if cfg.TwoFactor < TwoFactorOptional || cfg.TwoFactor > TwoFactorOff {
		panic(&loom.Error{Code: AU903, Msg: fmt.Sprintf("auth.Config has TwoFactor %d", cfg.TwoFactor),
			Fix: "use auth.TwoFactorOptional (the default), auth.TwoFactorRequired or auth.TwoFactorOff"})
	}
	seen := map[string]bool{}
	for _, p := range cfg.Providers {
		if p.identity == nil || seen[p.Name] {
			panic(&loom.Error{Code: AU903, Msg: fmt.Sprintf("auth.Config.Providers has a provider %q that isn't one auth.Google or auth.GitHub made, or is there twice", p.Name),
				Fix: `make each with auth.Google(id, secret) or auth.GitHub(id, secret), once`})
		}
		seen[p.Name] = true
	}
	return &App{cfg: cfg}
}

//go:embed migrations
var migrations embed.FS

// Migrations returns the tables auth keeps in the app's database, for
// `loom db install github.com/moomerman/loom/auth`.
func (a *App) Migrations() fs.FS {
	m, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic(err)
	}
	return m
}

// Routes registers the pages, relative to the mount prefix:
//
//	/sign-in        email and password, or "email me a link"
//	/sign-up        a new account
//	/forgot         "email me a link to reset my password"
//	/reset/{token}  the reset link: a new password
//	/link/{token}   the sign-in link: a button that signs in
//	/two-factor     the code after signing in, or setting up an
//	                authenticator when Config.TwoFactor requires one
//	/account        change password, two-factor, sign out (signed in only)
//	/account/two-factor  set up an authenticator (signed in only)
//	POST /sign-out  a plain handler, for a sign-out button in the app
//	/oauth/{name}   plain handlers, for each of Config.Providers: off to
//	                the provider, and /oauth/{name}/callback back
//
// The pages have their own app layout, a card, inside the host's root
// layout.
func (a *App) Routes(g *loom.Group) {
	a.app, a.prefix = g.App(), g.Prefix()
	g = g.Group(loom.RateLimit(perIP(120, time.Minute))) // every page load, event and handler; also caps the hashing work
	g.Handle("POST /sign-out", http.HandlerFunc(a.serveSignOut))
	if a.cfg.TwoFactor != TwoFactorOff {
		g.Handle("GET /two-factor/begin/{token}", http.HandlerFunc(a.serveBeginChallenge))
		a.app.StartupCheck(a.checkSecret)
	}
	for i := range a.cfg.Providers {
		p := &a.cfg.Providers[i]
		g.Handle("GET /oauth/"+p.Name, a.serveStart(p))
		g.Handle("GET /oauth/"+p.Name+"/callback", a.serveCallback(p))
	}
	if len(a.cfg.Providers) > 0 {
		a.app.StartupCheck(a.checkProviders)
	}
	g = g.Group(loom.Layout(Card, func(c *loom.Ctx) CardAssigns { return CardAssigns{AppName: a.appName()} }))
	g.Live("/sign-in", &SignInPage{auth: a}, SignIn, attempts("sign_in", "email_link"))
	if !a.cfg.NoSignUp {
		g.Live("/sign-up", &SignUpPage{auth: a}, SignUp, attempts("sign_up"))
	}
	if !a.cfg.NoEmail {
		g.Live("/forgot", &ForgotPage{auth: a}, Forgot, attempts("send"))
		g.Live("/reset/{token}", &ResetPage{auth: a}, Reset, loom.RateLimit(perIP(20, time.Minute, "save")))
		g.Live("/link/{token}", &LinkPage{auth: a}, Link, loom.RateLimit(perIP(20, time.Minute, "sign_in")))
		a.app.Jobs(SendLink{auth: a})
		a.app.StartupCheck(a.checkMail)
	}
	if a.cfg.TwoFactor != TwoFactorOff {
		g.Live("/two-factor", &ChallengePage{auth: a}, Challenge, loom.RateLimit(perIP(20, time.Minute, "verify", "set_up")))
	}
	signedIn := g.Group(loom.SignedIn(a.prefix + "/sign-in"))
	signedIn.Live("/account", &AccountPage{auth: a}, Account, loom.RateLimit(
		loom.Limit{Key: loom.ByUser, Max: 10, Per: time.Hour, On: []string{"save_password"}},
		loom.Limit{Key: loom.ByUser, Max: 5, Per: time.Hour, On: []string{"new_codes", "turn_off"}},
	))
	if a.cfg.TwoFactor != TwoFactorOff {
		signedIn.Live("/account/two-factor", &SetUpPage{auth: a}, SetUp, loom.RateLimit(loom.Limit{Key: loom.ByUser, Max: 10, Per: time.Minute, On: []string{"set_up"}}))
	}
}

// Auth's rate limits (docs/auth.md). They are built in, so mounting auth
// can't forget them; an app can add stricter ones in front of the mount.
// Two-factor codes also lock per account after ten wrong ones in a row
// (useCode), which no IP limit can give.

// perIP limits the events named (everything, if none) per client IP.
func perIP(max int, per time.Duration, on ...string) loom.Limit {
	return loom.Limit{Key: loom.ByIP, Max: max, Per: per, On: on}
}

// attempts limits the events that check a password or send mail: 20 a
// minute per IP (an office signing in at nine shares one) and 5 a minute
// per email address, whichever address the attempts come from.
func attempts(events ...string) loom.RouteOption {
	return loom.RateLimit(perIP(20, time.Minute, events...), loom.Limit{Key: loom.ByPayload("email"), Max: 5, Per: time.Minute, On: events})
}

// checkMail is the startup check that mail can be sent: a mailer (AU901)
// and, outside dev, the address links point to (AU902).
func (a *App) checkMail(ctx context.Context) error {
	if a.app.Mailer() == nil {
		return &loom.Error{Code: AU901, Msg: "auth sends sign-in and password-reset links, but the app has no mailer",
			Fix: "give the app one, app.UseMailer(m), with m a loom.Mailer around your mail service; or set auth.Config{NoEmail: true} for passwords only (under `loom dev` mail is kept for `loom mail` instead)"}
	}
	return a.checkBaseURL("mails links")
}

// checkProviders is the startup check that each provider can be used:
// a client ID and secret (AU906) and the address the provider sends the
// browser back to (AU902).
func (a *App) checkProviders(ctx context.Context) error {
	for _, p := range a.cfg.Providers {
		if p.ClientID == "" || p.ClientSecret == "" {
			env := strings.ToUpper(p.Name)
			return &loom.Error{Code: AU906, Msg: fmt.Sprintf("auth signs in with %s, but its client ID or secret is empty", p.Label),
				Fix: fmt.Sprintf(`register the app with %s (docs/auth.md says where) and pass both, e.g. auth.%s(os.Getenv("%s_CLIENT_ID"), os.Getenv("%s_CLIENT_SECRET")); or leave the provider out when they aren't set`, p.Label, providerFunc(p.Name), env, env)}
		}
	}
	return a.checkBaseURL("signs in with " + a.cfg.Providers[0].Label)
}

func providerFunc(name string) string {
	if name == "github" {
		return "GitHub"
	}
	return "Google"
}

// checkBaseURL is AU902: links and provider callbacks need the app's
// public address, except in dev, where they use the address the browser
// used.
func (a *App) checkBaseURL(what string) error {
	if a.app.BaseURL() == "" && !a.app.Dev() {
		return &loom.Error{Code: AU902, Msg: "auth " + what + ", but loom.Config.BaseURL is empty",
			Fix: `set the app's public address, e.g. loom.New(loom.Config{BaseURL: os.Getenv("BASE_URL")}) with BASE_URL=https://app.example.com`}
	}
	return nil
}

func (a *App) appName() string {
	if a.cfg.AppName != "" {
		return a.cfg.AppName
	}
	return a.app.Title()
}

// afterSignIn is where the browser goes once signed in, unless the page
// has a ?next=.
func (a *App) afterSignIn(u User) string {
	if a.cfg.AfterSignIn != nil {
		return a.cfg.AfterSignIn(u)
	}
	return "/"
}

// signIn ends the first step of signing in, a password, a link or a
// new account, from a live page: it signs u in, or, when two-factor
// applies to them, sends the browser on to the code page.
func (a *App) signIn(c *loom.Ctx, u User) {
	secret, err := a.challenge(c.Context(), u.ID)
	if err != nil {
		panic(err)
	}
	if secret == "" {
		c.SignIn(u.ID, a.afterSignIn(u))
		return
	}
	c.Navigate(c.Path("/two-factor/begin/"+secret) + nextQuery(c))
}

// serveSignOut signs out from a plain form, such as a "Sign out" button
// in the app's layout: <form method="post" action="/auth/sign-out">.
func (a *App) serveSignOut(w http.ResponseWriter, r *http.Request) {
	if err := a.app.SignOut(w, r); err != nil {
		http.Error(w, "500 signing out failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, a.cfg.AfterSignOut, http.StatusSeeOther)
}

// mailEvery is how often one email is sent a link of each kind: asking
// again sooner sends nothing more (the first mail is on its way), so
// the pages can't be used to flood someone's inbox.
const mailEvery = 2 * time.Minute

// sendLink enqueues the mail job, from a page's event.
func (a *App) sendLink(c *loom.Ctx, kind, email string) error {
	job := SendLink{Email: email, Kind: kind}
	if next := c.Request.URL.Query().Get("next"); loom.LocalPath(next) {
		job.Next = next
	}
	if a.app.BaseURL() == "" && a.app.Dev() {
		job.Base = origin(c.Request)
	}
	return a.app.DB().Tx(c.Context(), func(tx loom.Tx) error {
		_, err := jobs.Enqueue(tx, job, jobs.Unique(mailEvery))
		return err
	})
}

// SendLink mails a sign-in or password-reset link. It makes the link
// when it runs, so the link's secret is only ever in the mail, never in
// the job's args, which the jobs dashboard shows. A reset for an email
// with no account sends nothing, as does a sign-in link when sign-up is
// off; the page says the same either way, so it doesn't tell who has an
// account.
type SendLink struct {
	Email string `json:"email"`
	Kind  string `json:"kind"`           // "sign_in" or "reset"
	Next  string `json:"next,omitempty"` // a sign-in link's page to go to after
	Base  string `json:"base,omitempty"` // in dev without BaseURL, the page's origin

	auth *App `json:"-"`
}

// Options puts the mail in the default queue, with a few retries: mail
// services fail now and then, and a link that comes an hour late is no
// use.
func (SendLink) Options() jobs.Options {
	return jobs.Options{MaxAttempts: 5, Timeout: 30 * time.Second}
}

// Perform makes the link and sends the mail.
func (j SendLink) Perform(ctx context.Context, c *jobs.Ctx) error {
	a := j.auth
	if j.Kind != kindSignIn && j.Kind != kindReset {
		return jobs.Cancel(fmt.Errorf("unknown kind %q", j.Kind))
	}
	_, err := a.UserByEmail(ctx, a.app.DB(), j.Email)
	exists := err == nil
	if err != nil && !isNotFound(err) {
		return err
	}
	if !exists && (j.Kind == kindReset || a.cfg.NoSignUp) {
		return nil
	}
	mailer := a.app.Mailer()
	if mailer == nil {
		return jobs.Cancel(&loom.Error{Code: AU901, Msg: "the app has no mailer", Fix: "app.UseMailer(m)"})
	}
	secret, err := a.newLink(ctx, a.app.DB(), j.Kind, j.Email)
	if err != nil {
		return err
	}
	base := a.app.BaseURL()
	if base == "" {
		base = j.Base
	}
	name, mins := a.appName(), int(a.cfg.LinkFor/time.Minute)
	var m loom.Mail
	switch j.Kind {
	case kindSignIn:
		u := strings.TrimRight(base, "/") + a.prefix + "/link/" + secret
		if j.Next != "" {
			u += "?next=" + url.QueryEscape(j.Next)
		}
		m = loom.Mail{To: j.Email, Subject: "Sign in to " + name,
			Text: fmt.Sprintf("Sign in to %s by opening this link:\n\n%s\n\nIt works once, within %d minutes. If you didn't ask for it, ignore this email.\n", name, u, mins)}
		if !exists {
			m.Text = fmt.Sprintf("Open this link to make your %s account and sign in:\n\n%s\n\nIt works once, within %d minutes. If you didn't ask for it, ignore this email.\n", name, u, mins)
		}
	case kindReset:
		u := strings.TrimRight(base, "/") + a.prefix + "/reset/" + secret
		m = loom.Mail{To: j.Email, Subject: "Reset your " + name + " password",
			Text: fmt.Sprintf("Choose a new password for %s by opening this link:\n\n%s\n\nIt works once, within %d minutes. If you didn't ask for it, ignore this email: your password hasn't changed.\n", name, u, mins)}
	}
	return mailer.Send(ctx, m)
}

// origin is the scheme and host the browser used, for dev links.
func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// RequiresAuth is false: auth's pages are where people sign in, so they
// are public by design (the account page requires sign-in itself). It
// tells loom.manifest not to flag the mount as having no auth check.
func (a *App) RequiresAuth() bool { return false }
