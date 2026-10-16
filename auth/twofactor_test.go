package auth_test

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/auth"
	"github.com/moomerman/loom/loomtest"
)

// Codes are taken from two steps, so the second is newer than the
// first: set up with the previous step's code, then sign in with the
// current one (the pages accept one step either side of now).
var (
	earlier = func() time.Time { return time.Now().Add(-30 * time.Second) }
	now     = time.Now
)

// session signs id in and returns the cookie, for signed-in pages.
func (w *world) session(t *testing.T, id string) loomtest.Option {
	t.Helper()
	ck, err := w.app.SessionCookie(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return loomtest.Cookie(ck.Name, ck.Value)
}

// setUpTwoFactor turns two-factor on for u from the account pages and
// returns the authenticator's key, the code it was set up with and the
// recovery codes.
func (w *world) setUpTwoFactor(t *testing.T, u auth.User) (key, used string, codes []string) {
	t.Helper()
	acct := loomtest.Mount(t, w.app, "/auth/account", w.session(t, u.ID))
	acct.Click("button[on:click=set_up_two_factor]")
	if acct.Navigated() != "/auth/account/two-factor" {
		t.Fatalf("set up went to %q", acct.Navigated())
	}
	p := loomtest.Mount(t, w.app, "/auth/account/two-factor", w.session(t, u.ID))
	p.AssertAccessible()
	key = setUpKey(t, p)
	if again := setUpKey(t, loomtest.Mount(t, w.app, "/auth/account/two-factor", w.session(t, u.ID))); again != key {
		t.Errorf("reloading shows key %q, not the one scanned, %q", again, key)
	}
	p.Submit("form", loom.Payload{"code": "000000"})
	p.AssertText(".error", "That code didn't work. Type the newest code your app shows for this account.")
	used = auth.Code(key, earlier())
	p.Submit("form", loom.Payload{"code": used})
	return key, used, recoveryCodes(t, p)
}

// setUpKey reads the key a set-up page shows, under its QR code.
func setUpKey(t *testing.T, p *loomtest.Page) string {
	t.Helper()
	if !strings.Contains(p.HTML(), `src="data:image/svg+xml;base64,`) {
		t.Fatalf("no QR code:\n%s", p.HTML())
	}
	m := regexp.MustCompile(`<code[^>]*>([A-Z2-7 ]+)</code>`).FindStringSubmatch(p.HTML())
	if m == nil {
		t.Fatalf("no key:\n%s", p.HTML())
	}
	return m[1]
}

// recoveryCodes reads the recovery codes a page shows.
func recoveryCodes(t *testing.T, p *loomtest.Page) []string {
	t.Helper()
	var codes []string
	for _, m := range regexp.MustCompile(`<li[^>]*>([a-z2-7]{5}-[a-z2-7]{5})</li>`).FindAllStringSubmatch(p.HTML(), -1) {
		codes = append(codes, m[1])
	}
	if len(codes) != 10 {
		t.Fatalf("%d recovery codes, want 10:\n%s", len(codes), p.HTML())
	}
	return codes
}

// challenge follows a page's move to the code page: the begin handler
// moves the secret into a cookie for the code page alone. It returns
// the code page, mounted with that cookie.
func (w *world) challenge(t *testing.T, nav, next string) *loomtest.Page {
	t.Helper()
	if !strings.HasPrefix(nav, "/auth/two-factor/begin/") {
		t.Fatalf("went to %q, want the code page", nav)
	}
	srv := httptest.NewServer(w.app)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(srv.URL + nav)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return w.codePage(t, res, next)
}

// codePage checks res went to the code page with the challenge cookie,
// and mounts it.
func (w *world) codePage(t *testing.T, res *http.Response, next string) *loomtest.Page {
	t.Helper()
	want := "/auth/two-factor"
	if next != "" {
		want += "?next=" + url.QueryEscape(next)
	}
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != want {
		t.Fatalf("%d to %q, want %s", res.StatusCode, res.Header.Get("Location"), want)
	}
	for _, c := range res.Cookies() {
		if c.Name == loom.SessionCookie {
			t.Fatal("the first step set a session cookie")
		}
		if c.Name == "loom_auth_challenge" {
			if c.Path != "/auth/two-factor" || !c.HttpOnly || c.MaxAge != 600 {
				t.Errorf("challenge cookie %+v: want HttpOnly, for /auth/two-factor, for ten minutes", c)
			}
			return loomtest.Mount(t, w.app, want, loomtest.Cookie(c.Name, c.Value))
		}
	}
	t.Fatal("no challenge cookie")
	return nil
}

func (w *world) passwordStep(t *testing.T, email, password, next string) *loomtest.Page {
	t.Helper()
	p := loomtest.Mount(t, w.app, "/auth/sign-in?next="+url.QueryEscape(next))
	p.Submit("form", loom.Payload{"email": email, "password": password})
	return w.challenge(t, p.Navigated(), next)
}

func TestTwoFactorOptional(t *testing.T) {
	w := setup(t, auth.Config{})
	ada := w.user(t, "ada@example.com", "analytical engine")

	// Off until turned on: the password signs in.
	p := loomtest.Mount(t, w.app, "/auth/sign-in")
	p.Submit("form", loom.Payload{"email": "ada@example.com", "password": "analytical engine"})
	w.grantUser(t, p, "/")

	key, used, codes := w.setUpTwoFactor(t, ada)

	// On: the password goes on to the code page, which signs in with a
	// newer code than the last one used, once.
	c := w.passwordStep(t, "ada@example.com", "analytical engine", "/team")
	c.AssertAccessible()
	c.AssertText(".as", "as ada@example.com")
	c.Submit("form", loom.Payload{"code": used}) // not recomputed: a step may have passed since
	c.AssertText(".error", "That code didn't work. Type the newest code your app shows for this account.")
	code := auth.Code(key, now())
	c.Submit("form", loom.Payload{"code": code})
	if got := w.grantUser(t, c, "/team"); got != "ada@example.com" {
		t.Errorf("signed in %q", got)
	}
	c = w.passwordStep(t, "ada@example.com", "analytical engine", "/team")
	c.Submit("form", loom.Payload{"code": code})
	c.AssertText(".error", "That code didn't work. Type the newest code your app shows for this account.")

	// A recovery code works instead, once, typed any case and spacing.
	c.Submit("form", loom.Payload{"code": " " + strings.ToUpper(codes[0]) + " "})
	w.grantUser(t, c, "/team")
	c = w.passwordStep(t, "ada@example.com", "analytical engine", "/")
	c.Submit("form", loom.Payload{"code": codes[0]})
	c.AssertText(".error", "That code didn't work. Type the newest code your app shows for this account.")

	// The account page says how many are left.
	acct := loomtest.Mount(t, w.app, "/auth/account", w.session(t, ada.ID))
	acct.AssertText(".note", "On. You have 9 recovery codes left.")
	acct.AssertAccessible()
}

func TestTwoFactorEveryWayIn(t *testing.T) {
	f := newFake(t)
	w := setup(t, auth.Config{Providers: f.providers()})
	ada := w.user(t, "ada@example.com", "analytical engine")
	// Proved already: proving it for the first time by a link would
	// remove the authenticator (TestProvingEmailWins).
	if _, err := w.db.Exec(ctx, `UPDATE auth_users SET email_verified_at = now() WHERE id = $1`, ada.ID); err != nil {
		t.Fatal(err)
	}
	key, _, _ := w.setUpTwoFactor(t, ada)

	// A sign-in link.
	p := loomtest.Mount(t, w.app, "/auth/sign-in")
	p.Change("input[name=email]", "ada@example.com")
	p.Click("button[on:click=email_link]")
	w.q.Drain(t)
	link := loomtest.Mount(t, w.app, w.box.Link(t))
	link.Click("button")
	w.challenge(t, link.Navigated(), "")

	// A password reset.
	forgot := loomtest.Mount(t, w.app, "/auth/forgot")
	forgot.Submit("form", loom.Payload{"email": "ada@example.com"})
	w.q.Drain(t)
	reset := loomtest.Mount(t, w.app, w.box.Link(t))
	reset.Submit("form", loom.Payload{"password": "difference engine"})
	w.challenge(t, reset.Navigated(), "")

	// Google: the callback goes to the code page, not signed in.
	res := w.oauth(t, f, "google", "/team", nil)
	c := w.codePage(t, res, "/team")
	c.Submit("form", loom.Payload{"code": auth.Code(key, now())})
	if got := w.grantUser(t, c, "/team"); got != "ada@example.com" {
		t.Errorf("signed in %q", got)
	}
}

func TestTwoFactorAttempts(t *testing.T) {
	w := setup(t, auth.Config{})
	ada := w.user(t, "ada@example.com", "analytical engine")
	key, _, _ := w.setUpTwoFactor(t, ada)

	c := w.passwordStep(t, "ada@example.com", "analytical engine", "")
	for range 5 {
		c.Submit("form", loom.Payload{"code": "000000"})
		c.AssertText(".error", "That code didn't work. Type the newest code your app shows for this account.")
	}
	c.Submit("form", loom.Payload{"code": auth.Code(key, now())})
	c.AssertText("[role=alert]", "Sign in again This sign-in took more than ten minutes, or had too many wrong codes.")
	if c.Navigated() != "" {
		t.Errorf("a sixth try went to %q", c.Navigated())
	}

	// The code page with no challenge, or another's, signs no one in.
	loomtest.Mount(t, w.app, "/auth/two-factor").AssertHTML("[role=alert]", 1)
	loomtest.Mount(t, w.app, "/auth/two-factor", loomtest.Cookie("loom_auth_challenge", "made-up")).AssertHTML("[role=alert]", 1)
}

func TestTwoFactorRequired(t *testing.T) {
	w := setup(t, auth.Config{TwoFactor: auth.TwoFactorRequired})

	// A new account sets up an authenticator before it is signed in.
	p := loomtest.Mount(t, w.app, "/auth/sign-up?next=%2Fteam")
	p.Submit("form", loom.Payload{"email": "grace@example.com", "password": "compiler compiler"})
	c := w.challenge(t, p.Navigated(), "/team")
	c.AssertAccessible()
	key := setUpKey(t, c)
	c.Submit("form", loom.Payload{"code": "000000"})
	c.AssertText(".error", "That code didn't work. Type the newest code your app shows for this account.")
	c.Submit("form", loom.Payload{"code": auth.Code(key, earlier())})
	recoveryCodes(t, c)
	c.Click("button[on:click=continue]")
	if got := w.grantUser(t, c, "/team"); got != "grace@example.com" {
		t.Errorf("signed in %q", got)
	}

	// From then on, the code.
	c = w.passwordStep(t, "grace@example.com", "compiler compiler", "/")
	c.AssertHTML("img", 0)
	c.Submit("form", loom.Payload{"code": auth.Code(key, now())})
	w.grantUser(t, c, "/")

	// It can't be turned off.
	u, err := w.auth.UserByEmail(ctx, w.db, "grace@example.com")
	if err != nil {
		t.Fatal(err)
	}
	acct := loomtest.Mount(t, w.app, "/auth/account", w.session(t, u.ID))
	acct.AssertHTML("button[on:click=turn_off]", 0)
	acct.AssertRefused("turn_off", nil)
	acct.AssertText(".note", "On. You have 10 recovery codes left.")
}

func TestTwoFactorManage(t *testing.T) {
	w := setup(t, auth.Config{})
	ada := w.user(t, "ada@example.com", "analytical engine")
	key, _, old := w.setUpTwoFactor(t, ada)

	acct := loomtest.Mount(t, w.app, "/auth/account", w.session(t, ada.ID))
	acct.Submit("form.section", loom.Payload{"code": "000000"})
	acct.AssertText(".error", "That code didn't work. Type the newest code your app shows for this account.")
	acct.Submit("form.section", loom.Payload{"code": auth.Code(key, now())})
	codes := recoveryCodes(t, acct)
	if codes[0] == old[0] {
		t.Error("the recovery codes didn't change")
	}

	// The old codes are gone; a new one turns two-factor off.
	acct.Change("input[name=code]", old[1])
	acct.Click("button[on:click=turn_off]")
	acct.AssertText(".error", "That code didn't work. Type the newest code your app shows for this account.")
	acct.Change("input[name=code]", codes[1])
	acct.Click("button[on:click=turn_off]")
	acct.AssertText("[role=status]", "Two-factor sign-in is off Signing in no longer asks for a code.")

	p := loomtest.Mount(t, w.app, "/auth/sign-in")
	p.Submit("form", loom.Payload{"email": "ada@example.com", "password": "analytical engine"})
	w.grantUser(t, p, "/")
}

func TestTwoFactorOff(t *testing.T) {
	w := setup(t, auth.Config{TwoFactor: auth.TwoFactorOff})
	ada := w.user(t, "ada@example.com", "analytical engine")
	// An authenticator set up while the policy was on is kept, unused.
	if _, err := w.db.Exec(ctx, `INSERT INTO auth_totp (user_id, secret, enabled_at) VALUES ($1, 'JBSWY3DPEHPK3PXP', now())`, ada.ID); err != nil {
		t.Fatal(err)
	}

	p := loomtest.Mount(t, w.app, "/auth/sign-in")
	p.Submit("form", loom.Payload{"email": "ada@example.com", "password": "analytical engine"})
	w.grantUser(t, p, "/")
	acct := loomtest.Mount(t, w.app, "/auth/account", w.session(t, ada.ID))
	if strings.Contains(html.UnescapeString(acct.HTML()), "Two-factor") {
		t.Errorf("the account page offers two-factor:\n%s", acct.HTML())
	}
	srv := httptest.NewServer(w.app)
	defer srv.Close()
	for _, path := range []string{"/auth/two-factor", "/auth/account/two-factor", "/auth/two-factor/begin/x"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, res.StatusCode)
		}
	}
}

// The continue button is only on the page after set up, with the
// recovery codes showing; sent directly before then, it signs no one in.
func TestTwoFactorContinueNeedsCode(t *testing.T) {
	w := setup(t, auth.Config{TwoFactor: auth.TwoFactorRequired})

	// Setting up: no authenticator yet.
	p := loomtest.Mount(t, w.app, "/auth/sign-up")
	p.Submit("form", loom.Payload{"email": "grace@example.com", "password": "compiler compiler"})
	c := w.challenge(t, p.Navigated(), "")
	key := setUpKey(t, c)
	c.AssertRefused("continue", nil)
	if c.Navigated() != "" {
		t.Errorf("continue while setting up went to %q", c.Navigated())
	}
	c.Submit("form", loom.Payload{"code": auth.Code(key, earlier())})
	recoveryCodes(t, c)

	// Signing in: the code is asked for.
	c = w.passwordStep(t, "grace@example.com", "compiler compiler", "")
	c.AssertRefused("continue", nil)
	if c.Navigated() != "" {
		t.Errorf("continue at the code went to %q", c.Navigated())
	}
	c.Submit("form", loom.Payload{"code": auth.Code(key, now())})
	w.grantUser(t, c, "/")
}

// Authenticator secrets are stored encrypted with the app's secret, and
// one stored before the app had a secret is encrypted at startup.
func TestTwoFactorSecretEncrypted(t *testing.T) {
	w := setup(t, auth.Config{})
	ada := w.user(t, "ada@example.com", "analytical engine")
	key, _, _ := w.setUpTwoFactor(t, ada)
	plain := strings.ReplaceAll(key, " ", "")
	stored := func() string {
		s, err := loom.Get[string](ctx, w.app.DB(), `SELECT secret FROM auth_totp WHERE user_id = $1`, ada.ID)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := stored(); !loom.IsSealed(s) || strings.Contains(s, plain) {
		t.Fatalf("stored secret %q isn't sealed", s)
	}
	if _, err := w.app.DB().Exec(ctx, `UPDATE auth_totp SET secret = $2 WHERE user_id = $1`, ada.ID, plain); err != nil {
		t.Fatal(err)
	}
	if err := w.app.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	if s := stored(); !loom.IsSealed(s) {
		t.Fatalf("a plain secret wasn't sealed at startup: %q", s)
	}
	c := w.passwordStep(t, "ada@example.com", "analytical engine", "/")
	c.Submit("form", loom.Payload{"code": auth.Code(key, now())})
	w.grantUser(t, c, "/")
}
