package auth_test

import (
	"slices"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/auth"
	"github.com/moomerman/loom/loomtest"
)

// Password attempts are limited per email address, whatever IP they come
// from: the sixth in a minute is refused without checking the password.
func TestSignInRateLimit(t *testing.T) {
	w := setup(t, auth.Config{})
	w.user(t, "ada@example.com", "analytical engine")
	clock := loomtest.LimitClock(t, w.app)
	p := loomtest.Mount(t, w.app, "/auth/sign-in")
	p.AssertHTML("[data-loom-limited][hidden]", 1) // loom.js shows it on a 429
	wrong := loom.Payload{"email": "ada@example.com", "password": "difference engine"}
	for range 5 {
		p.Submit("form", wrong)
	}
	p.AssertEvent("auth.signin_failed", "method", "password")
	right := loom.Payload{"email": "ada@example.com", "password": "analytical engine"}
	p.AssertLimited("sign_in", right)
	wait := p.AssertLimited("email_link", loom.Payload{"email": "ADA@example.com"}) // the same address; refusals count too
	p.AssertEvent("limit.refused", "route", "/auth/sign-in", "event", "email_link")
	p.Submit("form", loom.Payload{"email": "bob@example.com", "password": "anything"})

	clock.Advance(wait)
	p.Submit("form", right)
	w.grantUser(t, p, "/")

	m := w.app.Manifest()
	for _, want := range []string{"every route: 120 per minute by ip", "/sign-in: 5 per minute by email on sign_in, email_link", "/account: 5 per hour by user on new_codes, turn_off"} {
		if !slices.Contains(m.Mounts[0].RateLimits, want) {
			t.Errorf("manifest rate limits %q, want %q", m.Mounts[0].RateLimits, want)
		}
	}
}

// Ten wrong codes in a row, across sign-ins, lock the account's codes:
// even the right one is refused until the lock ends.
func TestTwoFactorLock(t *testing.T) {
	w := setup(t, auth.Config{})
	ada := w.user(t, "ada@example.com", "analytical engine")
	key, _, _ := w.setUpTwoFactor(t, ada)
	for range 2 { // a challenge takes five wrong codes
		c := w.passwordStep(t, "ada@example.com", "analytical engine", "/")
		for range 5 {
			c.Submit("form", loom.Payload{"code": "000000"})
			c.AssertText(".error", "That code didn't work.")
		}
	}
	c := w.passwordStep(t, "ada@example.com", "analytical engine", "/")
	code := auth.Code(key, now())
	c.Submit("form", loom.Payload{"code": code})
	c.AssertText(".error", "Too many wrong codes for this account. Wait 15 minutes, then try again.")
	c.AssertEvent("auth.two_factor_failed", "user", ada.ID, "locked", true)
	if c.Navigated() != "" {
		t.Fatalf("a locked account signed in, to %q", c.Navigated())
	}

	acct := loomtest.Mount(t, w.app, "/auth/account", w.session(t, ada.ID))
	acct.Submit("form.section", loom.Payload{"code": code})
	acct.AssertText(".error", "Too many wrong codes")

	if _, err := w.db.Exec(ctx, `UPDATE auth_totp SET locked_until = now() - interval '1 second' WHERE user_id = $1`, ada.ID); err != nil {
		t.Fatal(err)
	}
	c.Submit("form", loom.Payload{"code": code})
	w.grantUser(t, c, "/")
}
