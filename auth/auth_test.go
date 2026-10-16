package auth_test

import (
	"bytes"
	"context"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/auth"
	"github.com/moomerman/loom/loomtest"
)

var ctx = context.Background()

// world is an app with auth mounted at /auth on a test database built
// from the installed copies of auth's migrations (testdata/migrations,
// made with `loom db install`), with a mailbox and a job queue.
type world struct {
	app     *loom.App
	auth    *auth.App
	db      *loom.DB
	q       *loomtest.JobQueue
	box     *loomtest.Mailbox
	signUps []string     // emails OnSignUp saw
	logs    bytes.Buffer // what the app logged
}

func setup(t *testing.T, cfg auth.Config) *world {
	// Committed: the grant is redeemed over HTTP while pages run, so the
	// database is used from several goroutines at once.
	w := &world{db: loomtest.DB(t, loomtest.Committed(), loomtest.Migrations("testdata/migrations"))}
	w.q = loomtest.Jobs(t, loomtest.UseDB(w.db))
	w.app = loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(&w.logs, nil)), BaseURL: "https://app.test", Secret: "a-test-secret-of-at-least-32-chars"})
	w.app.UseDB(w.db)
	w.q.Attach(w.app)
	w.box = loomtest.Mail(t, w.app)
	cfg.AppName = "Tests"
	cfg.OnSignUp = func(ctx context.Context, tx loom.Tx, u auth.User) error {
		w.signUps = append(w.signUps, u.Email)
		return nil
	}
	w.auth = auth.New(cfg)
	w.app.Mount("/auth", w.auth)
	return w
}

// user makes an account.
func (w *world) user(t *testing.T, email, password string) auth.User {
	var u auth.User
	err := w.db.Tx(ctx, func(tx loom.Tx) error {
		var err error
		u, err = w.auth.CreateUser(ctx, tx, email, password)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// grantUser checks that the page signed someone in, going on to next,
// and returns their email, from the account page.
func (w *world) grantUser(t *testing.T, p *loomtest.Page, next string) string {
	t.Helper()
	nav := p.Navigated()
	if !strings.HasPrefix(nav, "/_loom/signin/") || !strings.HasSuffix(nav, "?next="+url.QueryEscape(next)) {
		t.Fatalf("went to %q, want a sign-in grant going on to %s", nav, next)
	}
	srv := httptest.NewServer(w.app)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(srv.URL + nav)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == loom.SessionCookie {
			acct := loomtest.Mount(t, w.app, "/auth/account", loomtest.Cookie(c.Name, c.Value))
			m := regexp.MustCompile(`<p class="email[^"]*">([^<]*)</p>`).FindStringSubmatch(acct.HTML())
			if m == nil {
				t.Fatalf("no email on the account page:\n%s", acct.HTML())
			}
			return m[1]
		}
	}
	t.Fatalf("the grant set no session cookie: %d %s", res.StatusCode, body)
	return ""
}

func TestPasswordSignIn(t *testing.T) {
	w := setup(t, auth.Config{})
	w.user(t, "Ada@Example.com", "analytical engine")

	p := loomtest.Mount(t, w.app, "/auth/sign-in?next=%2Fteam")
	p.AssertAccessible()
	p.Submit("form", loom.Payload{"email": "nope"})
	p.AssertText(".error", "Enter your email address.")
	p.Submit("form", loom.Payload{"email": "ada@example.com", "password": "wrong"})
	p.AssertText(".error", "Wrong email or password.")
	p.Submit("form", loom.Payload{"email": "grace@example.com", "password": "analytical engine"})
	p.AssertText(".error", "Wrong email or password.")
	p.Submit("form", loom.Payload{"email": "ada@example.com", "password": "analytical engine"})
	if got := w.grantUser(t, p, "/team"); got != "Ada@Example.com" {
		t.Errorf("signed in %q", got)
	}
}

func TestSignUp(t *testing.T) {
	w := setup(t, auth.Config{AfterSignIn: func(u auth.User) string { return "/welcome" }})
	w.user(t, "grace@example.com", "")

	p := loomtest.Mount(t, w.app, "/auth/sign-up")
	p.AssertAccessible()
	p.Submit("form", loom.Payload{"email": "ada@example.com", "password": "short"})
	p.AssertText(".error", "Use at least 8 characters.")
	p.Submit("form", loom.Payload{"email": "GRACE@example.com", "password": "long enough"})
	p.AssertText(".error", "There's already an account for this email. Sign in instead.")
	p.Submit("form", loom.Payload{"email": "ada@example.com", "password": "long enough"})
	if got := w.grantUser(t, p, "/welcome"); got != "ada@example.com" {
		t.Errorf("signed in %q", got)
	}
	if strings.Join(w.signUps, " ") != "ada@example.com" {
		t.Errorf("OnSignUp saw %v", w.signUps)
	}
}

// A sign-in link: mailed by a job, opened (which uses nothing), then
// used by the button, once.
func TestSignInLink(t *testing.T) {
	w := setup(t, auth.Config{})
	w.user(t, "ada@example.com", "")

	p := loomtest.Mount(t, w.app, "/auth/sign-in?next=%2Fissues")
	p.Change("input[name=email]", "ada@example.com")
	p.Click("button[on:click=email_link]")
	p.AssertText(".alert", "We sent a sign-in link to ada@example.com")
	w.q.AssertEnqueued(t, auth.SendLink{Email: "ada@example.com", Kind: "sign_in"})
	if args := w.q.All()[0].Args; strings.Contains(string(args), "/link/") {
		t.Errorf("the job's args hold the link: %s", args)
	}
	w.q.Drain(t)
	m := w.box.Last(t)
	if m.To != "ada@example.com" || m.Subject != "Sign in to Tests" || !strings.Contains(m.Text, "https://app.test/auth/link/") {
		t.Fatalf("mail %+v", m)
	}

	link := w.box.Link(t)
	if !strings.HasSuffix(link, "?next=%2Fissues") {
		t.Errorf("link %q lost the next page", link)
	}
	lp := loomtest.Mount(t, w.app, link)
	lp.AssertText("p", "as ada@example.com")
	lp.AssertAccessible()
	lp = loomtest.Mount(t, w.app, link) // opening it twice uses nothing
	lp.Click("button")
	if got := w.grantUser(t, lp, "/issues"); got != "ada@example.com" {
		t.Errorf("signed in %q", got)
	}
	u, err := w.auth.UserByEmail(ctx, w.db, "ada@example.com")
	if err != nil || u.EmailVerifiedAt == nil {
		t.Errorf("the link didn't prove the email: %+v %v", u, err)
	}
	again := loomtest.Mount(t, w.app, link)
	again.AssertText(".alert", "This link doesn't work")
}

// A link to an email with no account makes one; with sign-up off it
// isn't sent.
func TestSignUpByLink(t *testing.T) {
	w := setup(t, auth.Config{})
	p := loomtest.Mount(t, w.app, "/auth/sign-in")
	p.Change("input[name=email]", "new@example.com")
	p.Click("button[on:click=email_link]")
	w.q.Drain(t)
	if !strings.Contains(w.box.Last(t).Text, "make your Tests account") {
		t.Errorf("mail %q", w.box.Last(t).Text)
	}
	lp := loomtest.Mount(t, w.app, w.box.Link(t))
	lp.Click("button")
	if got := w.grantUser(t, lp, "/"); got != "new@example.com" || strings.Join(w.signUps, " ") != "new@example.com" {
		t.Errorf("signed in %q, OnSignUp saw %v", got, w.signUps)
	}

	w = setup(t, auth.Config{NoSignUp: true})
	p = loomtest.Mount(t, w.app, "/auth/sign-in")
	p.AssertHTML("a[href='/auth/sign-up']", 0)
	p.Change("input[name=email]", "new@example.com")
	p.Click("button[on:click=email_link]")
	p.AssertText(".alert", "We sent a sign-in link") // says the same either way
	w.q.Drain(t)
	if n := len(w.box.Mail()); n != 0 {
		t.Errorf("sign-up is off, but %d mails went out", n)
	}
}

func TestResetPassword(t *testing.T) {
	w := setup(t, auth.Config{})
	ada := w.user(t, "ada@example.com", "old password")
	other, err := w.app.SessionCookie(ctx, ada.ID) // another browser, signed out by the reset
	if err != nil {
		t.Fatal(err)
	}

	p := loomtest.Mount(t, w.app, "/auth/forgot")
	p.AssertAccessible()
	p.Submit("form", loom.Payload{"email": "nobody@example.com"})
	p.AssertText(".alert", "If there's an account for nobody@example.com")
	w.q.Drain(t)
	if len(w.box.Mail()) != 0 {
		t.Fatal("a reset went to an email with no account")
	}
	p = loomtest.Mount(t, w.app, "/auth/forgot")
	p.Submit("form", loom.Payload{"email": "ada@example.com"})
	w.q.Drain(t)
	if m := w.box.Last(t); m.Subject != "Reset your Tests password" {
		t.Fatalf("mail %+v", m)
	}

	rp := loomtest.Mount(t, w.app, w.box.Link(t))
	rp.AssertText(".for", "For ada@example.com.")
	rp.Submit("form", loom.Payload{"password": "short"})
	rp.AssertText(".error", "Use at least 8 characters.")
	rp.Submit("form", loom.Payload{"password": "new password"})
	if got := w.grantUser(t, rp, "/"); got != "ada@example.com" {
		t.Errorf("signed in %q", got)
	}
	in := loomtest.Mount(t, w.app, "/auth/sign-in")
	in.Submit("form", loom.Payload{"email": "ada@example.com", "password": "new password"})
	if !strings.HasPrefix(in.Navigated(), "/_loom/signin/") {
		t.Error("the new password doesn't sign in")
	}
	loomtest.Mount(t, w.app, w.box.Link(t)).AssertText(".alert", "This link doesn't work")

	// The other browser was signed out.
	srv := httptest.NewServer(w.app)
	defer srv.Close()
	r, _ := http.NewRequest("GET", srv.URL+"/auth/account", nil)
	r.AddCookie(other)
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Errorf("the other browser is still signed in: %d", res.StatusCode)
	}
}

func TestAccount(t *testing.T) {
	w := setup(t, auth.Config{AfterSignOut: "/bye"})
	ada := w.user(t, "ada@example.com", "old password")

	p := loomtest.Mount(t, w.app, "/auth/account", loomtest.SignIn(t, w.app, ada.ID))
	p.AssertAccessible()
	p.Submit("form", loom.Payload{"current": "wrong", "new": "new password"})
	p.AssertText(".error", "That isn't your current password.")
	p.Submit("form", loom.Payload{"current": "old password", "new": "new password"})
	if !strings.HasSuffix(p.Navigated(), "?next="+url.QueryEscape("/auth/account?changed=1")) {
		t.Fatalf("went to %q", p.Navigated())
	}
	p = loomtest.Mount(t, w.app, "/auth/account?changed=1", loomtest.SignIn(t, w.app, ada.ID))
	p.AssertText(".alert", "Password saved")
	p.Click("button[on:click=sign_out]")
	if p.Navigated() != "/_loom/signout?next=%2Fbye" {
		t.Errorf("sign out went to %q", p.Navigated())
	}

	// No password yet: no current password to give.
	grace := w.user(t, "grace@example.com", "")
	p = loomtest.Mount(t, w.app, "/auth/account", loomtest.SignIn(t, w.app, grace.ID))
	p.AssertHTML("input[name=current]", 0)
	p.AssertText("h2", "Set a password")

	srv := httptest.NewServer(w.app)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(srv.URL + "/auth/account")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/auth/sign-in?next=%2Fauth%2Faccount" {
		t.Errorf("signed out: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
}

// The startup checks: a mailer (AU901) and the address links point to
// (AU902), unless email is off.
func TestStartupChecks(t *testing.T) {
	db := loomtest.DB(t, loomtest.Migrations("testdata/migrations"))
	check := func(cfg auth.Config, base string, mailer, dev, noSecret bool) error {
		mode := loom.DevOff
		if dev {
			mode = loom.DevOn
		}
		secret := "a-test-secret-of-at-least-32-chars"
		if noSecret {
			secret = ""
		}
		app := loom.New(loom.Config{BaseURL: base, Dev: mode, Secret: secret})
		app.UseDB(db)
		if mailer {
			loomtest.Mail(t, app)
		}
		app.Mount("/auth", auth.New(cfg))
		return app.Validate(ctx)
	}
	code := func(err error) string {
		var le *loom.Error
		if errors.As(err, &le) {
			return le.Code
		}
		return ""
	}
	for _, c := range []struct {
		name     string
		cfg      auth.Config
		base     string
		mailer   bool
		dev      bool
		want     string
		noSecret bool
	}{
		{"no mailer", auth.Config{}, "https://a.test", false, false, auth.AU901, false},
		{"no base URL", auth.Config{}, "", true, false, auth.AU902, false},
		{"ready", auth.Config{}, "https://a.test", true, false, "", false},
		{"dev keeps mail", auth.Config{}, "", false, true, "", false},
		{"no email", auth.Config{NoEmail: true}, "", false, false, "", false},
		{"provider without a secret", auth.Config{NoEmail: true, Providers: []auth.Provider{auth.Google("id", "")}}, "https://a.test", false, false, auth.AU906, false},
		{"provider without a base URL", auth.Config{NoEmail: true, Providers: []auth.Provider{auth.GitHub("id", "secret")}}, "", false, false, auth.AU902, false},
		{"provider in dev", auth.Config{NoEmail: true, Providers: []auth.Provider{auth.GitHub("id", "secret")}}, "", false, true, "", false},
		{"two-factor without a secret", auth.Config{NoEmail: true, TwoFactor: auth.TwoFactorOptional}, "https://a.test", false, false, auth.AU909, true},
		{"two-factor in dev", auth.Config{NoEmail: true, TwoFactor: auth.TwoFactorOptional}, "", false, true, "", true},
	} {
		if got := code(check(c.cfg, c.base, c.mailer, c.dev, c.noSecret)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	for name, cfg := range map[string]auth.Config{
		"AfterSignOut //evil.com": {AfterSignOut: "//evil.com"},
		"a provider twice":        {Providers: []auth.Provider{auth.Google("a", "b"), auth.Google("c", "d")}},
		"a homemade provider":     {Providers: []auth.Provider{{Name: "okta"}}},
	} {
		func() {
			defer func() {
				if r := recover(); r == nil || code(r.(error)) != auth.AU903 {
					t.Errorf("%s: %v, want AU903", name, r)
				}
			}()
			auth.New(cfg)
		}()
	}
}

func TestPasswords(t *testing.T) {
	h, err := auth.HashPassword("correct horse")
	if err != nil || !strings.HasPrefix(h, "pbkdf2-sha256$600000$") {
		t.Fatalf("HashPassword = %q, %v", h, err)
	}
	if h2, _ := auth.HashPassword("correct horse"); h2 == h {
		t.Error("two hashes of a password are the same: no salt")
	}
	if err := auth.CheckPassword(h, "correct horse"); err != nil {
		t.Errorf("right password: %v", err)
	}
	for _, wrong := range []string{"Correct horse", strings.Repeat("x", 2000)} {
		if err := auth.CheckPassword(h, wrong); !errors.Is(err, auth.ErrWrongPassword) {
			t.Errorf("CheckPassword(%.10q) = %v, want ErrWrongPassword", wrong, err)
		}
	}
	if err := auth.CheckPassword("", "anything"); !errors.Is(err, auth.ErrWrongPassword) {
		t.Errorf("no user: %v, want ErrWrongPassword", err)
	}
	var le *loom.Error
	if _, err := auth.HashPassword("short"); !errors.As(err, &le) || le.Code != auth.AU905 {
		t.Errorf("short password: %v, want AU905", err)
	}
	if err := auth.CheckPassword("$2a$10$bcrypt", "x"); !errors.As(err, &le) || le.Code != auth.AU904 {
		t.Errorf("foreign hash: %v, want AU904", err)
	}
}

// The installed copies in testdata/migrations are what `loom db install`
// makes today: a changed migration needs a new file (and a reinstall).
func TestInstalledMigrationsCurrent(t *testing.T) {
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range entries {
		src, _ := os.ReadFile("migrations/" + e.Name())
		name := loom.InstalledName("github.com/moomerman/loom/auth", e.Name())
		installed, err := os.ReadFile("testdata/migrations/" + e.Name()[:4] + "_" + name + ".sql")
		if err != nil {
			t.Fatalf("migration %d, %s: %v (run `loom db install --dir auth/testdata/migrations github.com/moomerman/loom/auth`)", i, e.Name(), err)
		}
		if !strings.HasSuffix(string(installed), string(src)) {
			t.Errorf("testdata's copy of %s is stale", e.Name())
		}
	}
}

// TestCodesDocumented keeps docs/errors.md in sync with the AU codes.
func TestCodesDocumented(t *testing.T) {
	doc, err := os.ReadFile("../docs/errors.md")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("errors.go")
	if err != nil {
		t.Fatal(err)
	}
	documented := regexp.MustCompile(`(?m)^### (AU\d{3})$`).FindAllStringSubmatch(string(doc), -1)
	defined := regexp.MustCompile(`(?m)^\t(AU\d{3}) = "AU\d{3}"`).FindAllStringSubmatch(string(src), -1)
	if len(defined) == 0 || len(documented) != len(defined) {
		t.Fatalf("errors.go defines %d AU codes, docs/errors.md documents %d", len(defined), len(documented))
	}
	for i := range defined {
		if defined[i][1] != documented[i][1] {
			t.Errorf("code %d: errors.go has %s, docs/errors.md has %s", i, defined[i][1], documented[i][1])
		}
	}
}

// Proving the email wins: someone who signs up with another's address
// and a password of their own keeps nothing once the owner proves the
// address, by a link or a provider.
func TestProvingEmailWins(t *testing.T) {
	f := newFake(t)
	w := setup(t, auth.Config{Providers: f.providers()})

	// claimed makes the account the way an attacker would: sign up with
	// ada's address, set up two-factor, and stay signed in.
	claimed := func() *http.Cookie {
		t.Helper()
		p := loomtest.Mount(t, w.app, "/auth/sign-up")
		p.Submit("form", loom.Payload{"email": "ada@example.com", "password": "not ada's password"})
		u, err := w.auth.UserByEmail(ctx, w.db, "ada@example.com")
		if err != nil || u.EmailVerifiedAt != nil {
			t.Fatalf("sign up: %+v %v", u, err)
		}
		w.setUpTwoFactor(t, u)
		ck, err := w.app.SessionCookie(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return ck
	}
	// lost checks the attacker has no way back in.
	lost := func(ck *http.Cookie) {
		t.Helper()
		p := loomtest.Mount(t, w.app, "/auth/sign-in")
		p.Submit("form", loom.Payload{"email": "ada@example.com", "password": "not ada's password"})
		p.AssertText(".error", "Wrong email or password.")
		srv := httptest.NewServer(w.app)
		defer srv.Close()
		req, _ := http.NewRequest("GET", srv.URL+"/auth/account", nil)
		req.AddCookie(ck)
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther {
			t.Errorf("the attacker's session still works: %d", res.StatusCode)
		}
	}
	remove := func() {
		t.Helper()
		if _, err := w.db.Exec(ctx, `DELETE FROM auth_users`); err != nil {
			t.Fatal(err)
		}
	}

	// A sign-in link: no code is asked for, since the authenticator was
	// the attacker's.
	ck := claimed()
	p := loomtest.Mount(t, w.app, "/auth/sign-in")
	p.Change("input[name=email]", "ada@example.com")
	p.Click("button[on:click=email_link]")
	w.q.Drain(t)
	link := loomtest.Mount(t, w.app, w.box.Link(t))
	link.Click("button")
	if got := w.grantUser(t, link, "/"); got != "ada@example.com" {
		t.Errorf("signed in %q", got)
	}
	lost(ck)

	// Google.
	remove()
	ck = claimed()
	if email, _ := w.signedInAs(t, w.oauth(t, f, "google", "", nil), "/"); email != "ada@example.com" {
		t.Errorf("signed in %q", email)
	}
	lost(ck)

	// Once proved, a link leaves the account's password alone.
	link = loomtest.Mount(t, w.app, "/auth/account", w.session(t, mustUser(t, w, "ada@example.com").ID))
	link.Submit("form", loom.Payload{"new": "ada's own password"})
	w.q.Advance(3 * time.Minute) // past the wait between links to one email
	p = loomtest.Mount(t, w.app, "/auth/sign-in")
	p.Change("input[name=email]", "ada@example.com")
	p.Click("button[on:click=email_link]")
	w.q.Drain(t)
	loomtest.Mount(t, w.app, w.box.Link(t)).Click("button")
	p = loomtest.Mount(t, w.app, "/auth/sign-in")
	p.Submit("form", loom.Payload{"email": "ada@example.com", "password": "ada's own password"})
	w.grantUser(t, p, "/")
}

func mustUser(t *testing.T, w *world, email string) auth.User {
	t.Helper()
	u, err := w.auth.UserByEmail(ctx, w.db, email)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Asking for links again and again sends one mail, and only the newest
// link works. Changing the password or signing out everywhere makes
// every link already mailed useless.
func TestLinksLimited(t *testing.T) {
	w := setup(t, auth.Config{})
	ada := w.user(t, "ada@example.com", "analytical engine")
	ask := func(kind string) {
		t.Helper()
		if kind == "reset" {
			loomtest.Mount(t, w.app, "/auth/forgot").Submit("form", loom.Payload{"email": "ada@example.com"})
		} else {
			p := loomtest.Mount(t, w.app, "/auth/sign-in")
			p.Change("input[name=email]", "ada@example.com")
			p.Click("button[on:click=email_link]")
			p.AssertText(".alert", "We sent a sign-in link to ada@example.com")
		}
		w.q.Drain(t)
	}
	works := func(link string) bool {
		t.Helper()
		return !strings.Contains(html.UnescapeString(loomtest.Mount(t, w.app, link).HTML()), "This link doesn't work")
	}

	for range 5 {
		ask("sign_in")
	}
	if n := len(w.q.Matching(auth.SendLink{Email: "ada@example.com", Kind: "sign_in"})); n != 1 {
		t.Errorf("five asks enqueued %d mails, want 1", n)
	}
	first := w.box.Link(t)
	w.q.Advance(3 * time.Minute)
	ask("sign_in")
	second := w.box.Link(t)
	if first == second || works(first) || !works(second) {
		t.Errorf("after a second link: the first works %v, the second %v; want only the second", works(first), works(second))
	}

	// A password change ends both kinds.
	w.q.Advance(3 * time.Minute)
	ask("reset")
	reset := w.box.Link(t)
	acct := loomtest.Mount(t, w.app, "/auth/account", w.session(t, ada.ID))
	acct.Submit("form", loom.Payload{"current": "analytical engine", "new": "difference engine"})
	if works(second) || works(reset) {
		t.Errorf("after a password change: the sign-in link works %v, the reset link %v", works(second), works(reset))
	}

	// So does signing out everywhere.
	w.q.Advance(3 * time.Minute)
	ask("sign_in")
	third := w.box.Link(t)
	acct = loomtest.Mount(t, w.app, "/auth/account", w.session(t, ada.ID))
	acct.Click("button[on:click=sign_out_everywhere]")
	if works(third) {
		t.Error("after signing out everywhere the sign-in link still works")
	}
}
