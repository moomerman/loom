package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/moomerman/loom"
)

// Signing in with another site's account: OAuth 2.0's authorization
// code flow with PKCE, on the standard library. The browser goes to
// /oauth/<provider>, which sends it to the provider with a fresh state
// and code challenge kept in a short-lived cookie; the provider sends
// it back to /oauth/<provider>/callback with a code, which is exchanged
// (with the verifier) for a token that reads who the person is.

// Provider is another site people sign in with. Make one with Google or
// GitHub; the URL fields are exported so tests can point them at a fake.
type Provider struct {
	Name         string // in the URLs and the auth_identities table: "google"
	Label        string // on the button: "Continue with Google"
	ClientID     string
	ClientSecret string
	AuthURL      string   // where the browser is sent to sign in
	TokenURL     string   // where the code is exchanged for a token
	UserURL      string   // who the token belongs to
	Scopes       []string // what the app asks to read: the email, no more

	identity func(ctx context.Context, p *Provider, token string) (Identity, error)
}

// Identity is who signed in at a provider.
type Identity struct {
	Subject       string // the provider's id for them, which never changes
	Email         string
	EmailVerified bool // the provider has proved they read mail sent to Email
}

// Google signs in with a Google account. Make the client in the Google
// Cloud console (APIs & Services, Credentials, OAuth client ID, a web
// application) with the authorized redirect URI
// <BaseURL><prefix>/oauth/google/callback.
func Google(clientID, clientSecret string) Provider {
	return Provider{Name: "google", Label: "Google", ClientID: clientID, ClientSecret: clientSecret,
		AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: "https://oauth2.googleapis.com/token",
		UserURL:  "https://openidconnect.googleapis.com/v1/userinfo",
		Scopes:   []string{"openid", "email"},
		identity: googleIdentity}
}

// GitHub signs in with a GitHub account. Make the client under Settings,
// Developer settings, OAuth Apps, with the authorization callback URL
// <BaseURL><prefix>/oauth/github/callback.
func GitHub(clientID, clientSecret string) Provider {
	return Provider{Name: "github", Label: "GitHub", ClientID: clientID, ClientSecret: clientSecret,
		AuthURL:  "https://github.com/login/oauth/authorize",
		TokenURL: "https://github.com/login/oauth/access_token",
		UserURL:  "https://api.github.com/user",
		Scopes:   []string{"read:user", "user:email"},
		identity: githubIdentity}
}

// googleIdentity reads OpenID Connect's userinfo.
func googleIdentity(ctx context.Context, p *Provider, token string) (Identity, error) {
	var u struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := getJSON(ctx, p.UserURL, token, &u); err != nil {
		return Identity{}, err
	}
	return Identity{Subject: u.Sub, Email: u.Email, EmailVerified: u.EmailVerified}, nil
}

// githubIdentity reads the user and their emails: the profile's email
// is whatever they chose to make public, so the verified primary one
// comes from /user/emails.
func githubIdentity(ctx context.Context, p *Provider, token string) (Identity, error) {
	var u struct {
		ID int64 `json:"id"`
	}
	if err := getJSON(ctx, p.UserURL, token, &u); err != nil {
		return Identity{}, err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := getJSON(ctx, p.UserURL+"/emails", token, &emails); err != nil {
		return Identity{}, err
	}
	id := Identity{Subject: strconv.FormatInt(u.ID, 10)}
	for _, e := range emails {
		if e.Primary {
			id.Email, id.EmailVerified = e.Email, e.Verified
		}
	}
	return id, nil
}

// oauthClient bounds every call to a provider.
var oauthClient = &http.Client{Timeout: 15 * time.Second}

func getJSON(ctx context.Context, u, token string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	res, err := oauthClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s: %.200s", u, res.Status, body)
	}
	return json.Unmarshal(body, v)
}

// oauthCookie keeps a sign-in's state, PKCE verifier and next page
// between leaving for the provider and coming back. It is scoped to the
// callback's path and lives ten minutes.
const oauthCookie = "loom_oauth"

// serveStart sends the browser to the provider.
func (a *App) serveStart(p *Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, verifier := randomString(), randomString()
		next := r.URL.Query().Get("next")
		if !loom.LocalPath(next) {
			next = ""
		}
		http.SetCookie(w, &http.Cookie{Name: oauthCookie, Value: state + "." + verifier + "." + base64.RawURLEncoding.EncodeToString([]byte(next)),
			Path: a.prefix + "/oauth/" + p.Name, MaxAge: 600, HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode})
		challenge := sha256.Sum256([]byte(verifier))
		q := url.Values{
			"response_type":         {"code"},
			"client_id":             {p.ClientID},
			"redirect_uri":          {a.callbackURL(r, p)},
			"scope":                 {strings.Join(p.Scopes, " ")},
			"state":                 {state},
			"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
			"code_challenge_method": {"S256"},
		}
		http.Redirect(w, r, p.AuthURL+"?"+q.Encode(), http.StatusFound)
	}
}

// serveCallback finishes a sign-in: it checks the state, exchanges the
// code, reads who signed in and signs them in as that user, making the
// account if it is new, or sends them on to type their code when they
// sign in with two factors. Anything wrong goes back to the sign-in page,
// which says what happened.
func (a *App) serveCallback(p *Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fail := func(why string) {
			http.Redirect(w, r, a.prefix+"/sign-in?"+url.Values{"with": {p.Name}, "failed": {why}}.Encode(), http.StatusSeeOther)
		}
		ck, err := r.Cookie(oauthCookie)
		http.SetCookie(w, &http.Cookie{Name: oauthCookie, Path: a.prefix + "/oauth/" + p.Name, MaxAge: -1, HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode})
		if err != nil {
			fail("expired")
			return
		}
		parts := strings.Split(ck.Value, ".")
		q := r.URL.Query()
		if len(parts) != 3 || subtle.ConstantTimeCompare([]byte(parts[0]), []byte(q.Get("state"))) != 1 {
			fail("expired")
			return
		}
		if q.Get("error") != "" || q.Get("code") == "" {
			fail("cancelled")
			return
		}
		verifier := parts[1]
		nextBytes, _ := base64.RawURLEncoding.DecodeString(parts[2])
		next := string(nextBytes)

		token, err := a.exchange(r, p, q.Get("code"), verifier)
		if err != nil {
			a.app.ReportError(&loom.Error{Code: AU907, Msg: fmt.Sprintf("signing in with %s failed: %v", p.Label, err),
				Fix: fmt.Sprintf("check the %s client's ID and secret, and that %s is one of its authorized redirect URIs", p.Label, a.callbackURL(r, p))})
			fail("error")
			return
		}
		id, err := p.identity(r.Context(), p, token)
		if err != nil || id.Subject == "" {
			a.app.ReportError(&loom.Error{Code: AU907, Msg: fmt.Sprintf("reading who signed in with %s failed: %v", p.Label, err),
				Fix: "check that the client asks for the scopes auth.Google or auth.GitHub set"})
			fail("error")
			return
		}
		u, why, err := a.signInWith(r.Context(), p.Name, id)
		if err != nil {
			a.app.ReportError(err)
			fail("error")
			return
		}
		if why != "" {
			fail(why)
			return
		}
		secret, err := a.challenge(r.Context(), u.ID)
		if err != nil {
			a.app.ReportError(err)
			fail("error")
			return
		}
		if secret != "" {
			a.beginChallenge(w, r, secret, next)
			return
		}
		if err := a.app.SignIn(w, r, u.ID); err != nil {
			a.app.ReportError(err)
			fail("error")
			return
		}
		if next == "" {
			next = a.afterSignIn(u)
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
	}
}

// exchange trades the code for an access token.
func (a *App) exchange(r *http.Request, p *Provider, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {a.callbackURL(r, p)},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(r.Context(), "POST", p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := oauthClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var t struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &t); err != nil {
		return "", fmt.Errorf("the token endpoint answered %s: %.200s", res.Status, body)
	}
	if t.AccessToken == "" {
		return "", fmt.Errorf("the token endpoint answered %s: %s %s", res.Status, t.Error, t.Description)
	}
	return t.AccessToken, nil
}

// callbackURL is where the provider sends the browser back: BaseURL's,
// or in dev without one, the address the browser used.
func (a *App) callbackURL(r *http.Request, p *Provider) string {
	base := a.app.BaseURL()
	if base == "" {
		base = origin(r)
	}
	return strings.TrimRight(base, "/") + a.prefix + "/oauth/" + p.Name + "/callback"
}

// providerMessage says on the sign-in page why signing in with a
// provider didn't work.
func providerMessage(label, why string) string {
	switch why {
	case "cancelled":
		return "Signing in with " + label + " was cancelled."
	case "expired":
		return "That sign-in took too long or was already used. Try again."
	case "unverified":
		return "Your " + label + " account has no verified email address."
	case "no_account":
		return "There's no account for your " + label + " email."
	}
	return "Signing in with " + label + " didn't work. Try again."
}

func randomString() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
