package loom_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/notestest"
	"github.com/moomerman/loom/internal/signintest"
	"github.com/moomerman/loom/loomtest"
)

func signinApp(t *testing.T, db *loom.DB) (*loom.App, *httptest.Server) {
	app := quietApp()
	if db != nil {
		app.UseDB(db)
	}
	signintest.Setup(app, signintest.Users{"ada": {ID: "ada", Name: "Ada Lovelace", Password: "engine"}})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return app, srv
}

// browse sends a request as a browser on the app's own page would.
func browse(t *testing.T, method, u string, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	r, _ := http.NewRequest(method, u, nil)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	res, err := noRedirects.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func sessionCookie(t *testing.T, res *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == loom.SessionCookie {
			return c
		}
	}
	t.Fatalf("no %s cookie in %v", loom.SessionCookie, res.Header["Set-Cookie"])
	return nil
}

func TestSignIn(t *testing.T) { testSignIn(t, nil) }

func TestSignInPostgres(t *testing.T) { testSignIn(t, loomtest.DB(t)) }

// testSignIn walks through signing in from a live page: sent to the
// sign-in page, a wrong password, the grant, the cookie, the typed user,
// and signing out.
func testSignIn(t *testing.T, db *loom.DB) {
	app, srv := signinApp(t, db)

	res, _ := browse(t, "GET", srv.URL+"/other?tab=2")
	if res.StatusCode != 303 || res.Header.Get("Location") != "/login?next="+url.QueryEscape("/other?tab=2") {
		t.Fatalf("signed out: %d %s, want a redirect to the sign-in page", res.StatusCode, res.Header.Get("Location"))
	}

	login := loomtest.Mount(t, app, "/login?next="+url.QueryEscape("/other?tab=2"))
	login.Submit("form", loom.Payload{"name": "ada", "password": "wrong"})
	login.AssertText(".error", "Wrong name or password.")
	login.Submit("form", loom.Payload{"name": "ada", "password": "engine"})
	grant := login.Navigated()
	if !strings.HasPrefix(grant, "/_loom/signin/") || !strings.HasSuffix(grant, "?next="+url.QueryEscape("/other?tab=2")) {
		t.Fatalf("signing in went to %q, want the grant with the next page", grant)
	}

	// A link to the grant from another site is refused and leaves it:
	// else a site could sign its visitors in to an account of its own.
	r, _ := http.NewRequest("GET", srv.URL+grant, nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if res, err := noRedirects.Do(r); err != nil || res.StatusCode != 403 || len(res.Cookies()) != 0 {
		t.Fatalf("grant from another site: %v %v, want 403 and no cookie", res, err)
	}

	res, _ = browse(t, "GET", srv.URL+grant)
	if res.StatusCode != 303 || res.Header.Get("Location") != "/other?tab=2" {
		t.Fatalf("grant: %d %s, want a redirect to the next page", res.StatusCode, res.Header.Get("Location"))
	}
	ck := sessionCookie(t, res)
	if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.MaxAge != 30*24*3600 || ck.Secure {
		t.Errorf("session cookie %+v, want HttpOnly, SameSite=Lax, Path=/, 30 days, not Secure over http", ck)
	}
	if res, _ := browse(t, "GET", srv.URL+grant); res.StatusCode != 303 || len(res.Cookies()) != 0 {
		t.Errorf("grant used twice: %d %v, want no cookie", res.StatusCode, res.Cookies())
	}
	if res, body := browse(t, "GET", srv.URL+"/", ck); res.StatusCode != 200 || !strings.Contains(body, "Signed in as Ada Lovelace") {
		t.Fatalf("signed in: %d\n%s", res.StatusCode, body)
	}
	// A session's own secret at the grant address is no grant, and
	// leaves the session signed in.
	if res, _ := browse(t, "GET", srv.URL+"/_loom/signin/"+ck.Value); len(res.Cookies()) != 0 {
		t.Errorf("a session's secret redeemed as a grant: %v", res.Cookies())
	}
	if res, _ := browse(t, "GET", srv.URL+"/", ck); res.StatusCode != 200 {
		t.Errorf("a session's secret at the grant address signed it out: %d", res.StatusCode)
	}
	if res, _ := browse(t, "GET", srv.URL+"/", &http.Cookie{Name: loom.SessionCookie, Value: "forged"}); res.StatusCode != 303 {
		t.Errorf("a forged cookie: %d, want a redirect to sign in", res.StatusCode)
	}

	// Signing out from a live page ends the session at once, then the
	// browser clears the cookie.
	home := loomtest.Mount(t, app, "/", loomtest.Cookie(ck.Name, ck.Value))
	home.Click("button[on:click=sign_out]")
	if home.Navigated() != "/_loom/signout?next=%2Flogin" {
		t.Fatalf("sign out went to %q", home.Navigated())
	}
	if res, _ := browse(t, "GET", srv.URL+"/", ck); res.StatusCode != 303 {
		t.Errorf("the signed-out cookie still works: %d", res.StatusCode)
	}
	res, _ = browse(t, "GET", srv.URL+home.Navigated(), ck)
	if res.StatusCode != 303 || res.Header.Get("Location") != "/login" || sessionCookie(t, res).MaxAge >= 0 {
		t.Errorf("signout: %d %s %v", res.StatusCode, res.Header.Get("Location"), res.Cookies())
	}
	// A GET of /_loom/signout ends nothing: a link from another site
	// signs no one out.
	ck2, _ := app.SessionCookie(context.Background(), "ada")
	if res, _ := browse(t, "GET", srv.URL+"/_loom/signout", ck2); len(res.Cookies()) != 0 {
		t.Errorf("signout of a live session cleared its cookie: %v", res.Cookies())
	}
	if res, _ := browse(t, "GET", srv.URL+"/", ck2); res.StatusCode != 200 {
		t.Errorf("after a stray signout link: %d", res.StatusCode)
	}
}

// A page whose session is signed out, in another tab or everywhere,
// ends at its next event.
func TestSignOutEndsOpenPages(t *testing.T) {
	app, _ := signinApp(t, nil)
	r := httptest.NewRequest("GET", "/", nil)
	ck, err := app.SessionCookie(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	r.AddCookie(ck)
	v, err := app.NewView(r, "test", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Event("ping", nil); err != nil {
		t.Fatalf("signed in: %v", err)
	}
	if err := app.SignOutEverywhere(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}
	var redirect *loom.RedirectError
	if _, err := v.Event("ping", nil); !errors.As(err, &redirect) || !strings.HasPrefix(redirect.URL, "/login?next=") {
		t.Fatalf("event after signing out: %v, want the auth check's redirect", err)
	}
}

// loomtest.SignIn mounts a page as a signed-in user; the manifest names
// the check.
func TestSignInHelper(t *testing.T) {
	app, _ := signinApp(t, nil)
	p := loomtest.Mount(t, app, "/", loomtest.SignIn(t, app, "ada"))
	p.AssertText(".user", "Ada Lovelace")
	ck, _ := app.SessionCookie(context.Background(), "nobody")
	if _, err := app.NewView(withCookie(httptest.NewRequest("GET", "/", nil), ck), "test", true); err == nil {
		t.Error("a session of a user the loader can't find was let in")
	}
	if m := app.Manifest().Text(); !strings.Contains(m, "auth: loom.SignedIn") {
		t.Errorf("manifest:\n%s", m)
	}
}

func withCookie(r *http.Request, c *http.Cookie) *http.Request { r.AddCookie(c); return r }

func TestUserMistakes(t *testing.T) {
	app, _ := signinApp(t, nil)
	_, err := app.NewView(httptest.NewRequest("GET", "/open", nil), "test", true)
	var le *loom.Error
	if !errors.As(err, &le) || le.Code != loom.RT430 {
		t.Errorf("loom.User on a page anyone may see: %v, want RT430", err)
	}
	defer func() {
		if le, _ := recover().(*loom.Error); le == nil || le.Code != loom.RT431 {
			t.Errorf("loom.Users twice: %v, want RT431", le)
		}
	}()
	loom.Users(app, func(context.Context, string) (string, error) { return "", nil })
}

// A mounted sub-app's migrations must be applied (RT433).
func TestSubAppMigrations(t *testing.T) {
	app := quietApp()
	app.Mount("/notes", &notestest.App{})
	var le *loom.Error
	if err := app.Validate(context.Background()); !errors.As(err, &le) || le.Code != loom.RT433 || !strings.Contains(le.Fix, "loom db install github.com/moomerman/loom/internal/notestest") {
		t.Fatalf("no database: %v, want RT433", err)
	}

	db := loomtest.DB(t)
	app = quietApp()
	app.UseDB(db)
	app.Mount("/notes", &notestest.App{})
	if err := app.Validate(context.Background()); !errors.As(err, &le) || le.Code != loom.RT433 || !strings.Contains(le.Msg, "0001_notes.sql, 0002_tags.sql") {
		t.Fatalf("not applied: %v, want RT433 naming both", err)
	}
	if _, err := db.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS loom_migrations (version bigint PRIMARY KEY, name text NOT NULL, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now());
INSERT INTO loom_migrations (version, name, checksum) VALUES (90001, 'notestest_notes', 'x'), (90002, 'notestest_tags', 'x')`); err != nil {
		t.Fatal(err)
	}
	if err := app.Validate(context.Background()); err != nil {
		t.Fatalf("applied: %v", err)
	}
}

// With a database, Loom's own migrations must be applied (RT433): its
// tables are never created at run time.
func TestLoomMigrations(t *testing.T) {
	app := quietApp()
	app.UseDB(loomtest.DB(t, loomtest.Migrations("internal/notestest/migrations")))
	var le *loom.Error
	if err := app.Validate(context.Background()); !errors.As(err, &le) || le.Code != loom.RT433 ||
		!strings.Contains(le.Msg, "0001_loom_clients.sql") || !strings.Contains(le.Fix, "loom db install github.com/moomerman/loom") {
		t.Fatalf("not applied: %v, want RT433 naming Loom's migrations", err)
	}

	app = quietApp()
	db := loomtest.DB(t) // ./migrations: Loom's own
	app.UseDB(db)
	if err := app.Validate(context.Background()); err != nil {
		t.Fatalf("applied: %v", err)
	}

	// An app that installed Loom's migrations before the node registry
	// existed is told to install 0005_loom_nodes.sql.
	if _, err := db.Exec(context.Background(), `DELETE FROM loom_migrations WHERE name = 'loom_nodes'`); err != nil {
		t.Fatal(err)
	}
	if err := app.Validate(context.Background()); !errors.As(err, &le) || le.Code != loom.RT433 ||
		!strings.Contains(le.Msg, "needs its migrations 0005_loom_nodes.sql, which aren't applied") {
		t.Fatalf("without loom_nodes: %v, want RT433 naming 0005_loom_nodes.sql", err)
	}
}

func TestLocalPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/":                true,
		"/team?tab=open":   true,
		"/a/b#c":           true,
		"/%09/evil.com":    true, // escaped: a path segment, not a tab
		"":                 false,
		"team":             false,
		"//evil.com":       false,
		"/\\evil.com":      false,
		"/\t/evil.com":     false,
		"/\n/evil.com":     false,
		"/\r/evil.com":     false,
		"/\x00":            false,
		"/\x7f":            false,
		"/..//evil.com":    true, // a path on this site; the browser keeps it here
		"https://evil.com": false,
		"javascript:alert": false,
		" /evil":           false,
	} {
		if got := loom.LocalPath(p); got != want {
			t.Errorf("LocalPath(%q) = %v, want %v", p, got, want)
		}
	}
}
