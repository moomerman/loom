package loom

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/moomerman/loom/trace"
)

// Signed-in users (docs/auth.md). Loom keeps who is signed in, not who
// the users are: the app (or a sign-in package) checks a password or an
// OAuth callback, then signs the user in by id. The browser holds a
// random token in an HttpOnly, SameSite=Lax cookie; the app's database
// holds its SHA-256, the user's id and the expiry, in
// loom_user_sessions (memory without a database). A stored session,
// unlike a signed cookie, can be revoked: signing out ends every open
// page of that session at its next event, on any node.
//
//	signedIn := app.Group(loom.SignedIn("/login"))   // required here
//	c.UserID()                                       // "" if no one
//	c.SignIn(id, "/")                                // from a live page's event
//	app.SignIn(w, r, id)                             // from a plain handler
//	loom.Users(app, store.UserByID)                  // the app's user type
//	me := loom.User[*User](c)                        // typed, on a SignedIn route

// SessionCookie is the name of the cookie holding the sign-in token.
const SessionCookie = "loom_session"

// grantFor is how long a sign-in grant (Ctx.SignIn) works: the browser
// redeems it at once.
const grantFor = time.Minute

// userFresh is how long a lookup of who is signed in is reused by the
// UserID and User calls of one page. The SignedIn check always looks
// again, before every event.
const userFresh = time.Second

// users holds the sessions and the app's user loader.
type users struct {
	mu       sync.Mutex
	ready    bool                     // the store is chosen, and its table exists
	stored   bool                     // sessions are in loom_user_sessions
	mem      map[string]sessionRecord // by token hash, without a database
	pruned   time.Time                // the last pruning of expired sessions
	load     func(context.Context, string) (any, error)
	loadType reflect.Type // the loader's user type, for User[T]
}

// sessionRecord is a stored token: a session, or a one-time grant.
type sessionRecord struct {
	kind    string // "session" or "grant"
	userID  string
	expires time.Time
}

// signedIn is a Ctx's last lookup of who is signed in.
type signedIn struct {
	at     time.Time // zero: not looked up yet
	id     string
	user   any // the loader's value, once User has asked
	loaded bool
}

// Users registers the app's user type and how to load one by id, so
// pages and layouts read the signed-in user typed, with User[T]. The
// SignedIn check loads the user too: a user the loader can't find
// (ErrNotFound) counts as signed out.
//
//	loom.Users(app, func(ctx context.Context, id string) (*User, error) {
//		return loom.Get[*User](ctx, app.DB(), `select * from users where id = $1`, id)
//	})
func Users[T any](app *App, load func(ctx context.Context, id string) (T, error)) {
	u := &app.users
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.load != nil {
		panic(&Error{Code: RT431, Msg: "loom.Users was called twice", Where: callerOutsideLoom(), Fix: "register the app's user type once"})
	}
	u.load = func(ctx context.Context, id string) (any, error) { return load(ctx, id) }
	u.loadType = reflect.TypeFor[T]()
}

// User returns the signed-in user, loaded with the func given to Users,
// once per event. Use it on routes that require sign-in (SignedIn); on
// others check c.UserID() first. No one signed in is RT430; a T other
// than the one given to Users, or no Users at all, is RT431.
//
//	me := loom.User[*User](c)
func User[T any](c *Ctx) T {
	a := c.app
	want := reflect.TypeFor[T]()
	if a == nil || a.users.load == nil {
		panic(&Error{Code: RT431, Msg: fmt.Sprintf("loom.User[%s] was called, but the app has no loom.Users", want), Where: callerOutsideLoom(),
			Fix: fmt.Sprintf("register how to load a user at startup: loom.Users(app, func(ctx context.Context, id string) (%s, error) { ... })", want)})
	}
	if want != a.users.loadType {
		panic(&Error{Code: RT431, Msg: fmt.Sprintf("loom.User[%s] was called, but loom.Users loads %s", want, a.users.loadType), Where: callerOutsideLoom(),
			Fix: fmt.Sprintf("write loom.User[%s](c)", a.users.loadType)})
	}
	id := c.UserID()
	if id == "" {
		panic(&Error{Code: RT430, Msg: "loom.User was called with no one signed in, on " + c.Request.URL.Path, Where: callerOutsideLoom(),
			Fix: `require sign-in on the route's group, app.Group(loom.SignedIn("/login")), or check c.UserID() != "" first on a page anyone may see`})
	}
	if !c.user.loaded {
		v, err := a.users.load(c.Context(), id)
		if err != nil {
			panic(err)
		}
		c.user.user, c.user.loaded = v, true
	}
	return c.user.user.(T)
}

// UserID returns the id of the signed-in user, or "" if no one is
// signed in.
func (c *Ctx) UserID() string {
	if c.app == nil || c.Request == nil {
		return ""
	}
	if c.user.at.IsZero() || time.Since(c.user.at) > userFresh {
		if err := c.lookupUser(); err != nil {
			panic(err)
		}
	}
	return c.user.id
}

// lookupUser looks up who the request's cookie signs in, afresh.
func (c *Ctx) lookupUser() error {
	id := ""
	if ck, err := c.Request.Cookie(SessionCookie); err == nil && ck.Value != "" {
		r, ok, err := c.app.users.get(c.Context(), c.app, hashToken(ck.Value))
		if err != nil {
			return err
		}
		if ok && r.kind == "session" {
			id = r.userID
		}
	}
	if id != c.user.id {
		c.user = signedIn{}
	}
	c.user.id, c.user.at, c.user.loaded = id, time.Now(), c.user.loaded && id != ""
	if !c.user.loaded {
		c.user.user = nil
	}
	return nil
}

// SignedIn is a group option requiring a signed-in user: an auth check
// that sends anyone else to signInPath, with ?next= the page they asked
// for. Like every auth check it runs on the page load and before every
// event, so a page whose session was signed out ends at its next event.
// With loom.Users, a user the loader can't find counts as signed out.
// The manifest lists it as loom.SignedIn.
//
//	signedIn := app.Group(loom.SignedIn("/login"))
func SignedIn(signInPath string) Option {
	if !LocalPath(signInPath) {
		panic(&Error{Code: RT416, Msg: fmt.Sprintf("loom.SignedIn(%q): the sign-in page isn't a path in the app", signInPath), Where: callerOutsideLoom(), Fix: `write a path like "/login"`})
	}
	return authOption{func(c *Ctx) error {
		if err := c.lookupUser(); err != nil {
			return err
		}
		ok := c.user.id != ""
		if ok && c.app.users.load != nil {
			c.user.loaded = false
			v, err := c.app.users.load(c.Context(), c.user.id)
			switch {
			case errors.Is(err, ErrNotFound):
				ok = false
			case err != nil:
				return err
			default:
				c.user.user, c.user.loaded = v, true
			}
		}
		if !ok {
			return Redirect(signInPath + "?next=" + url.QueryEscape(c.Request.URL.RequestURI()))
		}
		return nil
	}}
}

// SignIn signs userID in, from a live page's Mount or event handler,
// once the page knows who they are: a checked password, a new account.
// A live page can't set a cookie, so it sends the browser to
// /_loom/signin/<token>, which sets the session cookie and goes on to
// the page in the current URL's ?next= (as SignedIn sends), or else to
// next, a path in the app. The token works once, within a minute.
//
//	c.SignIn(user.ID, "/")
func (c *Ctx) SignIn(userID, next string) {
	secret, err := c.app.users.issue(c.Context(), c.app, "grant", userID, grantFor)
	if err != nil {
		panic(err)
	}
	if n := c.Request.URL.Query().Get("next"); LocalPath(n) {
		next = n
	}
	if !LocalPath(next) {
		next = "/"
	}
	c.Navigate("/_loom/signin/" + secret + "?next=" + url.QueryEscape(next))
}

// SignOut signs the page's user out at once, from a live page's event
// handler: their session ends (every open page of it ends at its next
// event), and the browser goes to /_loom/signout, which clears the
// cookie, then to next, a path in the app.
func (c *Ctx) SignOut(next string) {
	if ck, err := c.Request.Cookie(SessionCookie); err == nil {
		rec, ok, _ := c.app.users.get(c.Context(), c.app, hashToken(ck.Value))
		if err := c.app.users.delete(c.Context(), c.app, hashToken(ck.Value)); err != nil {
			panic(err)
		}
		if ok {
			c.app.stream.Emit(c.Context(), trace.UserSignOut, "user", rec.userID)
		}
	}
	c.user = signedIn{at: time.Now()}
	if !LocalPath(next) {
		next = "/"
	}
	c.Navigate("/_loom/signout?next=" + url.QueryEscape(next))
}

// SignIn signs userID in from a plain handler, such as an OAuth
// callback: it starts a session and sets its cookie on w. Redirect
// afterwards. Live pages use Ctx.SignIn.
func (a *App) SignIn(w http.ResponseWriter, r *http.Request, userID string) error {
	secret, err := a.users.issue(r.Context(), a, "session", userID, a.signInFor())
	if err != nil {
		return err
	}
	// A session the browser already had ends: signing in always starts a
	// new one, so a cookie planted before sign-in is never signed in.
	if old, err := r.Cookie(SessionCookie); err == nil {
		a.users.delete(r.Context(), a, hashToken(old.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: secret, Path: "/", MaxAge: int(a.signInFor() / time.Second),
		HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode})
	a.stream.Emit(r.Context(), trace.UserSignIn, "user", userID)
	return nil
}

// SignOut signs the request's user out from a plain handler: it ends
// the session and clears the cookie. Live pages use Ctx.SignOut.
func (a *App) SignOut(w http.ResponseWriter, r *http.Request) error {
	if ck, err := r.Cookie(SessionCookie); err == nil {
		rec, ok, _ := a.users.get(r.Context(), a, hashToken(ck.Value))
		if err := a.users.delete(r.Context(), a, hashToken(ck.Value)); err != nil {
			return err
		}
		if ok {
			a.stream.Emit(r.Context(), trace.UserSignOut, "user", rec.userID)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode})
	return nil
}

// SignOutEverywhere ends every session of userID, after a password
// change, say. Their open pages end at their next event.
func (a *App) SignOutEverywhere(ctx context.Context, userID string) error {
	return a.users.deleteUser(ctx, a, userID)
}

// SessionCookie starts a session for userID and returns its cookie,
// without a browser: for tests (loomtest.SignIn) and tools. Apps sign
// people in with Ctx.SignIn or App.SignIn.
func (a *App) SessionCookie(ctx context.Context, userID string) (*http.Cookie, error) {
	secret, err := a.users.issue(ctx, a, "session", userID, a.signInFor())
	if err != nil {
		return nil, err
	}
	return &http.Cookie{Name: SessionCookie, Value: secret, Path: "/"}, nil
}

func (a *App) signInFor() time.Duration { return a.cfg.SignInFor }

// serveSignIn redeems a grant made by Ctx.SignIn. Only a page of the app
// goes there: a link from another site is refused (RT429), or a page
// there could sign a visitor in as the account it got a grant for.
func (a *App) serveSignIn(w http.ResponseWriter, r *http.Request) {
	if crossSiteNavigation(r) {
		a.refuseCrossOrigin(w, r)
		return
	}
	rec, ok, err := a.users.takeGrant(r.Context(), a, hashToken(r.PathValue("token")))
	if err != nil {
		a.logError("", err)
		http.Error(w, "500 the session store failed", http.StatusInternalServerError)
		return
	}
	next := r.URL.Query().Get("next")
	if !LocalPath(next) {
		next = "/"
	}
	if !ok || rec.kind != "grant" {
		http.Redirect(w, r, next, http.StatusSeeOther) // a used grant: wherever it was going decides
		return
	}
	if err := a.SignIn(w, r, rec.userID); err != nil {
		a.logError("", err)
		http.Error(w, "500 the session store failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// serveSignOut clears the cookie of a session Ctx.SignOut ended. It
// is a GET, so it ends nothing itself: a page on another site that
// links here signs no one out.
func (a *App) serveSignOut(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Query().Get("next")
	if !LocalPath(next) {
		next = "/"
	}
	if ck, err := r.Cookie(SessionCookie); err == nil {
		if _, ok, err := a.users.get(r.Context(), a, hashToken(ck.Value)); err == nil && !ok {
			http.SetCookie(w, &http.Cookie{Name: SessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode})
		}
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// The session store: loom_user_sessions with a database (one of Loom's
// migrations, like loom_clients), else memory.

func (u *users) init(ctx context.Context, a *App) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ready {
		return nil
	}
	if db := a.DB(); db.SQL() != nil || db.IsInTx() {
		u.stored = true
	} else {
		u.mem = map[string]sessionRecord{}
	}
	u.ready = true
	return nil
}

func storeError(err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: RT432, Msg: "the sign-in session store failed: " + err.Error(),
		Fix: "check the database connection; sessions are kept in the loom_user_sessions table, one of Loom's migrations (`loom db install github.com/moomerman/loom`, then `loom migrate`)"}
}

// issue stores a new token and returns its secret.
func (u *users) issue(ctx context.Context, a *App, kind, userID string, ttl time.Duration) (string, error) {
	if userID == "" {
		panic(&Error{Code: RT430, Msg: "sign-in was asked to sign in an empty user id", Where: callerOutsideLoom(), Fix: "pass the id of the user from the app's users table"})
	}
	if err := u.init(ctx, a); err != nil {
		return "", err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(b)
	rec := sessionRecord{kind: kind, userID: userID, expires: time.Now().Add(ttl)}
	if u.stored {
		_, err := a.db.Exec(ctx, `INSERT INTO loom_user_sessions (token_hash, kind, user_id, expires_at) VALUES ($1, $2, $3, $4)`, hashToken(secret), kind, userID, rec.expires)
		if err != nil {
			return "", storeError(err)
		}
	} else {
		u.mu.Lock()
		u.mem[hashToken(secret)] = rec
		u.mu.Unlock()
	}
	u.prune(ctx, a)
	return secret, nil
}

// get returns the unexpired token with the hash.
func (u *users) get(ctx context.Context, a *App, hash string) (sessionRecord, bool, error) {
	return u.one(ctx, a, hash, false, "")
}

// take gets the token with the hash and deletes it, in one step, so a
// one-time grant works once even when two requests race for it.
func (u *users) take(ctx context.Context, a *App, hash string) (sessionRecord, bool, error) {
	return u.one(ctx, a, hash, true, "")
}

// takeGrant is take for a grant only: any other token with the hash (a
// session) is left alone.
func (u *users) takeGrant(ctx context.Context, a *App, hash string) (sessionRecord, bool, error) {
	return u.one(ctx, a, hash, true, "grant")
}

// one reads, and if take deletes, the token with the hash, of kind
// unless kind is "".
func (u *users) one(ctx context.Context, a *App, hash string, take bool, kind string) (sessionRecord, bool, error) {
	if err := u.init(ctx, a); err != nil {
		return sessionRecord{}, false, err
	}
	var r sessionRecord
	if u.stored {
		q := `SELECT kind, user_id, expires_at FROM loom_user_sessions WHERE token_hash = $1 AND ($2 = '' OR kind = $2)`
		if take {
			q = `DELETE FROM loom_user_sessions WHERE token_hash = $1 AND ($2 = '' OR kind = $2) RETURNING kind, user_id, expires_at`
		}
		err := a.db.QueryRow(ctx, q, hash, kind).Scan(&r.kind, &r.userID, &r.expires)
		if errors.Is(err, sql.ErrNoRows) {
			return r, false, nil
		}
		if err != nil {
			return r, false, storeError(err)
		}
	} else {
		u.mu.Lock()
		var ok bool
		r, ok = u.mem[hash]
		if ok && kind != "" && r.kind != kind {
			r, ok = sessionRecord{}, false
		}
		if ok && take {
			delete(u.mem, hash)
		}
		u.mu.Unlock()
		if !ok {
			return r, false, nil
		}
	}
	return r, time.Now().Before(r.expires), nil
}

func (u *users) delete(ctx context.Context, a *App, hash string) error {
	_, _, err := u.take(ctx, a, hash)
	return err
}

func (u *users) deleteUser(ctx context.Context, a *App, userID string) error {
	if err := u.init(ctx, a); err != nil {
		return err
	}
	if u.stored {
		_, err := a.db.Exec(ctx, `DELETE FROM loom_user_sessions WHERE user_id = $1`, userID)
		return storeError(err)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for h, r := range u.mem {
		if r.userID == userID {
			delete(u.mem, h)
		}
	}
	return nil
}

// prune deletes expired sessions, at most once an hour.
func (u *users) prune(ctx context.Context, a *App) {
	u.mu.Lock()
	due := time.Since(u.pruned) > time.Hour
	if due {
		u.pruned = time.Now()
		if !u.stored {
			for h, r := range u.mem {
				if !time.Now().Before(r.expires) {
					delete(u.mem, h)
				}
			}
		}
	}
	u.mu.Unlock()
	if due && u.stored {
		if _, err := a.db.Exec(ctx, `DELETE FROM loom_user_sessions WHERE expires_at <= now()`); err != nil {
			a.logError("", fmt.Errorf("pruning expired sign-in sessions: %w", err))
		}
	}
}

// hashToken returns the hex SHA-256 of a secret, as stored: a leaked
// table or backup signs no one in.
func hashToken(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// LocalPath reports whether p is a path on this site, so redirecting to
// it (a ?next= page, say) can't send anyone to another site. Browsers
// read "//host" and "/\host" as other sites, and drop tabs and newlines
// before reading, so "/\t/host" is one too: a local path starts with
// one slash and has no control characters or backslashes. Sub-apps that
// redirect to a page they were given check it with this.
func LocalPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return false
	}
	for i := 0; i < len(p); i++ {
		if c := p[i]; c < 0x20 || c == 0x7f || c == '\\' {
			return false
		}
	}
	u, err := url.Parse(p)
	return err == nil && u.Scheme == "" && u.Host == "" && u.Opaque == "" && u.User == nil
}

// secureRequest reports whether the browser reached the app over HTTPS,
// here or at a proxy in front of it, so cookies are sent only over HTTPS.
func secureRequest(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
