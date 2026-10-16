package auth_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/auth"
	"github.com/moomerman/loom/loomtest"
)

// fakeProvider stands in for Google and GitHub: an OAuth server that
// gives code "good" a token, if the client secret and the PKCE verifier
// are right, and says the token is whoever sub, email and verified say.
type fakeProvider struct {
	*httptest.Server
	challenge string // from the redirect to /authorize, checked at /token
	sub       string
	email     string
	verified  bool
}

func newFake(t *testing.T) *fakeProvider {
	f := &fakeProvider{sub: "1001", email: "ada@example.com", verified: true}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "good" || r.Form.Get("client_secret") != "secret" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge ||
			!strings.HasPrefix(r.Form.Get("redirect_uri"), "https://app.test/auth/oauth/") {
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "tok", "token_type": "bearer"})
	})
	authed := func(h func(w http.ResponseWriter)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, "bad token", http.StatusUnauthorized)
				return
			}
			h(w)
		}
	}
	mux.HandleFunc("GET /userinfo", authed(func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{"sub": f.sub, "email": f.email, "email_verified": f.verified})
	}))
	mux.HandleFunc("GET /user", authed(func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{"id": 42, "email": nil})
	}))
	mux.HandleFunc("GET /user/emails", authed(func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"email": "old@example.com", "primary": false, "verified": true},
			{"email": f.email, "primary": true, "verified": f.verified},
		})
	}))
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// providers are auth.Google and auth.GitHub pointed at f.
func (f *fakeProvider) providers() []auth.Provider {
	g, gh := auth.Google("id", "secret"), auth.GitHub("id", "secret")
	g.AuthURL, g.TokenURL, g.UserURL = f.URL+"/authorize", f.URL+"/token", f.URL+"/userinfo"
	gh.AuthURL, gh.TokenURL, gh.UserURL = f.URL+"/authorize", f.URL+"/token", f.URL+"/user"
	return []auth.Provider{g, gh}
}

// oauth signs in with a provider in a browser of its own: the sign-in
// button's link, the provider's redirect back, and the callback, which
// tamper can change. It returns the callback's response.
func (w *world) oauth(t *testing.T, f *fakeProvider, name, next string, tamper func(url.Values)) *http.Response {
	t.Helper()
	srv := httptest.NewServer(w.app)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(srv.URL + "/auth/oauth/" + name + "?next=" + url.QueryEscape(next))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	to, _ := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(to.String(), f.URL+"/authorize?") {
		t.Fatalf("start: %d to %q, want a redirect to the provider", res.StatusCode, to)
	}
	q := to.Query()
	if q.Get("redirect_uri") != "https://app.test/auth/oauth/"+name+"/callback" || q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "id" {
		t.Fatalf("authorize query %v", q)
	}
	f.challenge = q.Get("code_challenge")
	back := url.Values{"code": {"good"}, "state": {q.Get("state")}}
	if tamper != nil {
		tamper(back)
	}
	res, err = client.Get(srv.URL + "/auth/oauth/" + name + "/callback?" + back.Encode())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

// signedInAs returns the email on the account page for the session res
// started, and the line saying which providers it signs in with.
func (w *world) signedInAs(t *testing.T, res *http.Response, next string) (email, via string) {
	t.Helper()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != next {
		t.Fatalf("callback: %d to %q, want %s", res.StatusCode, res.Header.Get("Location"), next)
	}
	for _, c := range res.Cookies() {
		if c.Name == loom.SessionCookie {
			acct := loomtest.Mount(t, w.app, "/auth/account", loomtest.Cookie(c.Name, c.Value))
			page := acct.HTML()
			if m := regexp.MustCompile(`<p class="via[^"]*">([^<]*)</p>`).FindStringSubmatch(page); m != nil {
				via = m[1]
			}
			return regexp.MustCompile(`<p class="email[^"]*">([^<]*)</p>`).FindStringSubmatch(page)[1], via
		}
	}
	t.Fatal("the callback set no session cookie")
	return "", ""
}

// failed checks the callback went back to the sign-in page, and returns
// what the page says.
func (w *world) failed(t *testing.T, res *http.Response) string {
	t.Helper()
	loc := res.Header.Get("Location")
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/auth/sign-in?") {
		t.Fatalf("callback: %d to %q, want the sign-in page", res.StatusCode, loc)
	}
	for _, c := range res.Cookies() {
		if c.Name == loom.SessionCookie {
			t.Fatal("a failed sign-in set a session cookie")
		}
	}
	p := loomtest.Mount(t, w.app, loc)
	m := regexp.MustCompile(`role="alert"[^>]*>(?:<[^>]*>)*([^<]+)`).FindStringSubmatch(p.HTML())
	if m == nil {
		t.Fatalf("no alert on the sign-in page:\n%s", p.HTML())
	}
	return html.UnescapeString(strings.TrimSpace(m[1]))
}

func TestGoogleSignIn(t *testing.T) {
	f := newFake(t)
	w := setup(t, auth.Config{Providers: f.providers()})

	p := loomtest.Mount(t, w.app, "/auth/sign-in?next=%2Fteam")
	p.AssertAccessible()
	p.AssertText(`a[href="/auth/oauth/google?next=%2Fteam"]`, "Continue with Google")
	p.AssertText(`a[href="/auth/oauth/github?next=%2Fteam"]`, "Continue with GitHub")

	// A new email makes an account, verified, with the app's rows.
	email, via := w.signedInAs(t, w.oauth(t, f, "google", "/team", nil), "/team")
	if email != "ada@example.com" || via != "You also sign in with Google." {
		t.Errorf("signed in %q (%q)", email, via)
	}
	if strings.Join(w.signUps, " ") != "ada@example.com" {
		t.Errorf("OnSignUp saw %v", w.signUps)
	}
	if u, _ := w.auth.UserByEmail(ctx, w.db, "ada@example.com"); u.EmailVerifiedAt == nil {
		t.Error("the account's email isn't verified")
	}

	// The same Google account signs in as the same user after its email
	// changes there: the provider's id is what counts.
	f.email = "ada@lovelace.dev"
	if email, _ := w.signedInAs(t, w.oauth(t, f, "google", "", nil), "/"); email != "ada@example.com" {
		t.Errorf("came back as %q", email)
	}
	if len(w.signUps) != 1 {
		t.Errorf("OnSignUp ran again: %v", w.signUps)
	}
}

// GitHub, with the verified primary email from /user/emails, joins the
// account that already has it.
func TestGitHubJoinsAccount(t *testing.T) {
	f := newFake(t)
	w := setup(t, auth.Config{Providers: f.providers(), AfterSignIn: func(u auth.User) string { return "/home" }})
	w.user(t, "Ada@Example.com", "analytical engine")

	email, via := w.signedInAs(t, w.oauth(t, f, "github", "", nil), "/home")
	if email != "Ada@Example.com" || via != "You also sign in with GitHub." {
		t.Errorf("signed in %q (%q)", email, via)
	}
	if len(w.signUps) != 0 {
		t.Errorf("OnSignUp ran: %v", w.signUps)
	}
}

func TestProviderFailures(t *testing.T) {
	f := newFake(t)
	w := setup(t, auth.Config{Providers: f.providers()})

	if got := w.failed(t, w.oauth(t, f, "google", "", func(q url.Values) { q.Set("state", "forged") })); got != "That sign-in took too long or was already used. Try again." {
		t.Errorf("forged state: %q", got)
	}
	if got := w.failed(t, w.oauth(t, f, "github", "", func(q url.Values) { q.Del("code"); q.Set("error", "access_denied") })); got != "Signing in with GitHub was cancelled." {
		t.Errorf("cancelled: %q", got)
	}
	if got := w.failed(t, w.oauth(t, f, "google", "", func(q url.Values) { q.Set("code", "stolen") })); got != "Signing in with Google didn't work. Try again." {
		t.Errorf("refused code: %q", got)
	}
	if !strings.Contains(w.logs.String(), "AU907") || !strings.Contains(w.logs.String(), "https://app.test/auth/oauth/google/callback") {
		t.Errorf("a refused code logged %q, want AU907 naming the redirect URI", w.logs.String())
	}

	// An unverified email finds or makes no account.
	f.verified = false
	if got := w.failed(t, w.oauth(t, f, "google", "", nil)); got != "Your Google account has no verified email address." {
		t.Errorf("unverified: %q", got)
	}
	if len(w.signUps) != 0 {
		t.Errorf("made accounts %v", w.signUps)
	}

	// With sign-up off, a new email has no account.
	f.verified = true
	w2 := setup(t, auth.Config{Providers: f.providers(), NoSignUp: true})
	if got := w2.failed(t, w2.oauth(t, f, "google", "", nil)); got != "There's no account for your Google email." {
		t.Errorf("no sign-up: %q", got)
	}
}
