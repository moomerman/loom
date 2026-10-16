package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/form"
	"github.com/moomerman/loom/trace"
)

// Two-factor sign-in. Every way in (a password, a mailed link, a new
// account, a reset, Google or GitHub) ends in App.signIn, or its twin in
// the OAuth callback. When two-factor applies to the person (they turned
// it on, or Config.TwoFactor requires it), that stores a challenge, a
// sign-in waiting for a code, instead of signing in, and the browser
// goes to /two-factor/begin/{secret}, which keeps the secret in a cookie
// scoped to the code page and goes on to it. So the secret leaves the
// address bar at once, and the code page works only in the browser that
// passed the first step.

// challengeCookie holds a challenge's secret, for the code page only.
const challengeCookie = "loom_auth_challenge"

// challenge returns the secret of a new challenge when two-factor
// applies to the user, or "" when they sign in without a code.
func (a *App) challenge(ctx context.Context, userID string) (string, error) {
	if a.cfg.TwoFactor == TwoFactorOff {
		return "", nil
	}
	on, err := a.twoFactorOn(ctx, a.app.DB(), userID)
	if err != nil {
		return "", err
	}
	if !on && a.cfg.TwoFactor != TwoFactorRequired {
		return "", nil
	}
	return a.newChallenge(ctx, userID)
}

// serveBeginChallenge moves a challenge's secret from the URL into a
// cookie and goes on to the code page.
func (a *App) serveBeginChallenge(w http.ResponseWriter, r *http.Request) {
	a.beginChallenge(w, r, r.PathValue("token"), r.URL.Query().Get("next"))
}

func (a *App) beginChallenge(w http.ResponseWriter, r *http.Request, secret, next string) {
	http.SetCookie(w, &http.Cookie{Name: challengeCookie, Value: secret, Path: a.prefix + "/two-factor", MaxAge: int(challengeFor / time.Second),
		HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode})
	u := a.prefix + "/two-factor"
	if loom.LocalPath(next) {
		u += "?next=" + url.QueryEscape(next)
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

// ChallengePage, at /two-factor, is the second step of signing in: the
// code from the person's authenticator app, or a recovery code. When
// Config.TwoFactor requires two-factor and they have no authenticator
// yet, they set one up here first, and see their recovery codes, before
// they are signed in.
type ChallengePage struct {
	auth   *App
	token  string
	email  string
	mode   string // "code", "set_up", "codes" (set up, showing the recovery codes) or "expired"
	secret string // set_up: the new authenticator's secret
	codes  []string
	code   form.Form[CodeInput]
}

// CodeInput is a form with one code: six digits from an authenticator
// app, or a recovery code.
type CodeInput struct {
	Code string `loom:"secret"`
}

func (in *CodeInput) Validate(e *form.Errors) {
	if normalizeCode(in.Code) == "" {
		e.Add(&in.Code, "Enter the code from your authenticator app.")
	}
}

// wrongCode is what a code that doesn't work gets.
const wrongCode = "That code didn't work. Type the newest code your app shows for this account."

// lockedCode is the error while the account's codes are locked
// (codeLockAfter wrong codes in a row).
const lockedCode = "Too many wrong codes for this account. Wait 15 minutes, then try again."

// codeError is the form error for a code useCode refused.
func codeError(err error) string {
	if errors.Is(err, errCodesLocked) {
		return lockedCode
	}
	return wrongCode
}

func (p *ChallengePage) Mount(c *loom.Ctx) {
	a, ctx := p.auth, c.Context()
	p.code = form.New(CodeInput{})
	c.Title("Two-factor sign-in")
	p.mode = "expired"
	if ck, err := c.Request.Cookie(challengeCookie); err == nil {
		p.token = ck.Value
	}
	if p.token == "" {
		return
	}
	id, ok, err := a.peekChallenge(ctx, p.token)
	if err != nil {
		panic(err)
	}
	if !ok {
		return
	}
	u, err := a.User(ctx, id)
	if isNotFound(err) {
		return
	}
	if err != nil {
		panic(err)
	}
	p.email = u.Email
	on, err := a.twoFactorOn(ctx, a.app.DB(), id)
	if err != nil {
		panic(err)
	}
	switch {
	case on:
		p.mode = "code"
	case a.cfg.TwoFactor == TwoFactorRequired:
		if p.secret, err = a.pendingTOTP(ctx, id); err != nil {
			panic(err)
		}
		p.mode = "set_up"
	default: // turned off since the first step: nothing to ask
		p.finish(c)
	}
}

func (p *ChallengePage) Events() loom.Events {
	return loom.Events{
		"change":   func(c *loom.Ctx, payload loom.Payload) { p.code.Change(payload) },
		"verify":   p.verify,
		"set_up":   p.setUp,
		"continue": p.continueAfterCodes,
	}
}

// continueAfterCodes signs in once the person has set up an
// authenticator and seen their recovery codes. Only setUp reaches the
// "codes" mode, so in any other mode the event is refused: the button
// isn't on the page, and sending the event directly must not skip the
// code.
func (p *ChallengePage) continueAfterCodes(c *loom.Ctx, _ loom.Payload) {
	if p.mode != "codes" {
		return
	}
	p.finish(c)
}

// verify checks the code and signs in. Each try counts against the
// challenge; after five, the person starts again from the password.
func (p *ChallengePage) verify(c *loom.Ctx, payload loom.Payload) {
	in, ok := p.code.Submit(payload)
	if !ok || p.mode != "code" {
		return
	}
	a, ctx := p.auth, c.Context()
	id, ok, err := a.attemptChallenge(ctx, p.token)
	if err != nil {
		panic(err)
	}
	if !ok {
		p.mode = "expired"
		return
	}
	used, err := a.useCode(ctx, a.app.DB(), id, in.Code)
	if err != nil && !errors.Is(err, errCodesLocked) {
		panic(err)
	}
	if !used {
		a.app.Emit(ctx, trace.AuthTwoFactorFailed, "user", id, "locked", errors.Is(err, errCodesLocked))
		p.code.AddError(&in.Code, codeError(err))
		return
	}
	p.finish(c)
}

// setUp turns on the authenticator the person just scanned, once they
// type a code from it, and shows their recovery codes.
func (p *ChallengePage) setUp(c *loom.Ctx, payload loom.Payload) {
	in, ok := p.code.Submit(payload)
	if !ok || p.mode != "set_up" {
		return
	}
	a, ctx := p.auth, c.Context()
	id, ok, err := a.attemptChallenge(ctx, p.token)
	if err != nil {
		panic(err)
	}
	if !ok {
		p.mode = "expired"
		return
	}
	codes, ok, err := a.enableTOTP(ctx, id, in.Code)
	if err != nil {
		panic(err)
	}
	if !ok {
		p.code.AddError(&in.Code, wrongCode)
		return
	}
	p.codes, p.mode, p.secret = codes, "codes", ""
}

// finish uses the challenge and signs in.
func (p *ChallengePage) finish(c *loom.Ctx) {
	a, ctx := p.auth, c.Context()
	id, ok, err := a.takeChallenge(ctx, a.app.DB(), p.token)
	if err != nil {
		panic(err)
	}
	if !ok {
		p.mode = "expired"
		return
	}
	u, err := a.User(ctx, id)
	if err != nil {
		panic(err)
	}
	c.SignIn(u.ID, a.afterSignIn(u))
}

func (p *ChallengePage) Assigns(c *loom.Ctx) ChallengeAssigns {
	as := ChallengeAssigns{Mode: p.mode, Email: p.email, Code: p.code, Codes: p.codes, Next: nextQuery(c)}
	if p.secret != "" {
		as.Qr, as.Secret = p.auth.authenticator(p.email, p.secret)
	}
	return as
}

// authenticator is what the set-up pages show of a new secret: a QR
// code of its otpauth:// address, and the secret spaced for typing.
func (a *App) authenticator(email, secret string) (qr, key string) {
	code, err := encodeQR(totpURI(a.appName(), email, secret))
	if err == nil { // an email too long for a QR code leaves the key to type
		qr = code.svgDataURL()
	}
	return qr, groupSecret(secret)
}
