package loom

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
)

// A page's session id is in the page, and in the stream's URL, so it can
// end up in proxy logs, access logs and error reports. Knowing it must
// not be enough to act on the page. So each browser gets a random cookie
// the first time it loads a page, the session keeps the cookie's hash,
// and every request that names the session (an event, the stream, a
// patch or a navigation leaving it) must carry the same cookie. A
// request without it is answered as if the session were unknown: a real
// browser that lost the cookie remounts the page and carries on, and
// anyone else gets nothing.

// BrowserCookie is the cookie that binds a page's session to the browser
// that loaded it. It is HttpOnly and lasts until the browser closes.
const BrowserCookie = "loom_browser"

// browserID returns r's browser cookie, setting a new one on w if r has
// none, and returns its hash for the session to keep.
func browserID(w http.ResponseWriter, r *http.Request) string {
	if ck, err := r.Cookie(BrowserCookie); err == nil && len(ck.Value) >= 32 {
		return hashToken(ck.Value)
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	secret := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{Name: BrowserCookie, Value: secret, Path: "/",
		HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode})
	return hashToken(secret)
}

// pageSession returns the session sid if r comes from the browser that
// loaded it, else nil.
func (a *App) pageSession(r *http.Request, sid string) *session {
	s := a.session(sid)
	if s == nil {
		return nil
	}
	ck, err := r.Cookie(BrowserCookie)
	if err != nil || subtle.ConstantTimeCompare([]byte(hashToken(ck.Value)), []byte(s.browser)) != 1 {
		return nil
	}
	return s
}
