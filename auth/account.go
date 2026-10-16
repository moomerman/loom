package auth

import (
	"errors"
	"strings"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/form"
	"github.com/moomerman/loom/trace"
)

// AccountPage, at /account, is the signed-in person's account: change
// (or set) their password, set up or manage two-factor sign-in, sign out
// here, or sign out everywhere. Changing the password signs out every
// other session.
type AccountPage struct {
	auth     *App
	user     User
	password bool // the account has a password, so changing it needs it
	change   form.Form[PasswordInput]
	changed  bool
	via      string // the providers the account signs in with: "Google and GitHub"

	twoFactor bool // two-factor sign-in is on
	codesLeft int  // unused recovery codes
	manage    form.Form[CodeInput]
	newCodes  []string // just made, shown once
	turnedOff bool
}

// PasswordInput is the change-password form. Current is left out when
// the account has no password yet (it signs in by link).
type PasswordInput struct {
	Current string `loom:"secret"`
	New     string `loom:"secret"`
}

func (in *PasswordInput) Validate(e *form.Errors) { checkNewPassword(e, &in.New) }

func (p *AccountPage) Mount(c *loom.Ctx) {
	var err error
	if p.user, err = p.auth.User(c.Context(), c.UserID()); err != nil {
		panic(err)
	}
	if p.password, err = p.auth.hasPassword(c.Context(), p.user.ID); err != nil {
		panic(err)
	}
	if len(p.auth.cfg.Providers) > 0 {
		names, err := p.auth.identities(c.Context(), p.user.ID)
		if err != nil {
			panic(err)
		}
		var labels []string
		for _, n := range names {
			for _, pr := range p.auth.cfg.Providers {
				if pr.Name == n {
					labels = append(labels, pr.Label)
				}
			}
		}
		p.via = strings.Join(labels, " and ")
	}
	if p.auth.cfg.TwoFactor != TwoFactorOff {
		if err := p.loadTwoFactor(c); err != nil {
			panic(err)
		}
	}
	p.change = form.New(PasswordInput{})
	p.manage = form.New(CodeInput{})
	p.changed = c.Request.URL.Query().Get("changed") == "1"
	c.Title("Your account")
}

func (p *AccountPage) Events() loom.Events {
	return loom.Events{
		"change":              func(c *loom.Ctx, payload loom.Payload) { p.change.Change(payload) },
		"save_password":       p.savePassword,
		"sign_out":            func(c *loom.Ctx, _ loom.Payload) { c.SignOut(p.auth.cfg.AfterSignOut) },
		"sign_out_everywhere": p.signOutEverywhere,
		"set_up_two_factor":   func(c *loom.Ctx, _ loom.Payload) { c.Navigate(c.Path("/account/two-factor")) },
		"code_change":         func(c *loom.Ctx, payload loom.Payload) { p.manage.Change(payload) },
		"new_codes":           p.newRecoveryCodes,
		"turn_off":            p.turnOff,
	}
}

func (p *AccountPage) loadTwoFactor(c *loom.Ctx) error {
	var err error
	if p.twoFactor, err = p.auth.twoFactorOn(c.Context(), p.auth.app.DB(), p.user.ID); err != nil {
		return err
	}
	p.codesLeft, err = p.auth.recoveryCodesLeft(c.Context(), p.user.ID)
	return err
}

// checkCode uses the code in the two-factor form, a current code or a
// recovery code: changing two-factor takes the second factor too, not
// only a signed-in browser.
func (p *AccountPage) checkCode(c *loom.Ctx, payload loom.Payload) bool {
	in, ok := p.manage.Submit(payload)
	if !ok || !p.twoFactor {
		return false
	}
	used, err := p.auth.useCode(c.Context(), p.auth.app.DB(), p.user.ID, in.Code)
	if err != nil && !errors.Is(err, errCodesLocked) {
		panic(err)
	}
	if !used {
		p.auth.app.Emit(c.Context(), trace.AuthTwoFactorFailed, "user", p.user.ID, "locked", errors.Is(err, errCodesLocked))
		p.manage.AddError(&in.Code, codeError(err))
	}
	return used
}

// newRecoveryCodes replaces the recovery codes, for when they are used
// up or seen by someone else.
func (p *AccountPage) newRecoveryCodes(c *loom.Ctx, payload loom.Payload) {
	if !p.checkCode(c, payload) {
		return
	}
	codes, err := p.auth.newRecoveryCodes(c.Context(), p.auth.app.DB(), p.user.ID)
	if err != nil {
		panic(err)
	}
	p.newCodes, p.codesLeft = codes, len(codes)
	p.manage = form.New(CodeInput{})
}

// turnOff turns two-factor sign-in off, unless the app requires it. The
// button isn't the form's submit, so the code is the one change events
// kept.
func (p *AccountPage) turnOff(c *loom.Ctx, _ loom.Payload) {
	if p.auth.cfg.TwoFactor == TwoFactorRequired || !p.checkCode(c, nil) {
		return
	}
	if err := p.auth.disableTOTP(c.Context(), p.user.ID); err != nil {
		panic(err)
	}
	p.twoFactor, p.codesLeft, p.newCodes, p.turnedOff = false, 0, nil, true
	p.manage = form.New(CodeInput{})
}

func (p *AccountPage) savePassword(c *loom.Ctx, payload loom.Payload) {
	in, ok := p.change.Submit(payload)
	if !ok {
		return
	}
	a, ctx := p.auth, c.Context()
	if p.password {
		if _, err := a.checkPassword(ctx, p.user.Email, in.Current); err == ErrWrongPassword {
			p.change.AddError(&in.Current, "That isn't your current password.")
			return
		} else if err != nil {
			panic(err)
		}
	}
	err := a.app.DB().Tx(ctx, func(tx loom.Tx) error {
		if err := a.setPassword(ctx, tx, p.user.ID, in.New); err != nil {
			return err
		}
		return a.deleteLinks(ctx, tx, p.user.Email)
	})
	if err != nil {
		panic(err)
	}
	// End every session, this one too, then sign this browser in afresh.
	if err := a.app.SignOutEverywhere(ctx, p.user.ID); err != nil {
		panic(err)
	}
	c.SignIn(p.user.ID, c.Path("/account")+"?changed=1")
}

func (p *AccountPage) signOutEverywhere(c *loom.Ctx, _ loom.Payload) {
	if err := p.auth.deleteLinks(c.Context(), p.auth.app.DB(), p.user.Email); err != nil {
		panic(err)
	}
	if err := p.auth.app.SignOutEverywhere(c.Context(), p.user.ID); err != nil {
		panic(err)
	}
	c.SignOut(p.auth.cfg.AfterSignOut)
}

func (p *AccountPage) Assigns(c *loom.Ctx) AccountAssigns {
	return AccountAssigns{Email: p.user.Email, Password: p.password, Change: p.change, Changed: p.changed, Via: p.via,
		TwoFactor: twoFactorPolicy[p.auth.cfg.TwoFactor], TwoFactorOn: p.twoFactor, CodesLeft: p.codesLeft, Manage: p.manage,
		NewCodes: p.newCodes, TurnedOff: p.turnedOff}
}

// twoFactorPolicy names Config.TwoFactor for the template.
var twoFactorPolicy = map[TwoFactor]string{TwoFactorOptional: "optional", TwoFactorRequired: "required", TwoFactorOff: "off"}
