package loom

import (
	"bytes"
	"compress/flate"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// Kept state (docs/live.md, "Keeping state across a remount"). A page
// or live layout lives in the server's memory, so a restart loses it,
// and the browser remounts the page on a new session that starts over:
// a player forgets what it was playing, a wizard its step. A page or
// live layout with a Keep method names the part of its state to keep.
// After each render the runtime encodes it, seals it with the app's
// secret and hands it to the browser, which holds it; a remount sends
// it back, and the new copy gets it before Mount runs. Sealing means it
// comes back exactly as the server last rendered it, for the browser it
// was given to, or not at all: the browser can't read, change or swap
// it.

// Keeper is implemented by a page or live layout whose state a remount
// keeps. Keep returns a pointer to that state: a struct of exported,
// JSON-encodable fields, usually a field of the page itself. It is
// restored before Mount, so Mount sees it and loads what it names.
//
//	type Player struct {
//		store   *Store
//		playing Playing // kept: what a restart mustn't lose
//		tracks  map[int]Track
//	}
//
//	type Playing struct {
//		Station string
//		History []int // track ids, oldest first
//		At      int
//	}
//
//	func (p *Player) Keep() any { return &p.playing }
type Keeper interface {
	Keep() any
}

// keepHeader is the request header of a remount carrying the state the
// browser was given.
const keepHeader = "X-Loom-Kept"

// MaxKept is the most bytes of sealed state a page and its live layout
// may keep: it travels in a request header, which proxies limit.
const MaxKept = 6 << 10

// keptPurpose is what kept state is sealed for (App.Seal).
const keptPurpose = "loom.kept"

// devKeepSecret seals kept state in dev without Config.Secret, so a
// remount after `loom dev` rebuilds the app still restores it. Dev
// serves only this machine (RT440), so it needn't be secret.
const devKeepSecret = "loom dev: kept state, sealed with a key that isn't secret"

// kept is what is sealed: the browser it is for, and the state of the
// live layout and the page, each with its Go type, so a deploy that
// changed the type restores nothing rather than the wrong thing.
type kept struct {
	Browser    string          `json:"b"`
	Layout     string          `json:"l,omitempty"`
	LayoutKept json.RawMessage `json:"lk,omitempty"`
	Page       string          `json:"p,omitempty"`
	PageKept   json.RawMessage `json:"pk,omitempty"`
}

// checkKeeper checks a page's or live layout's Keep method at startup
// (RT452): it must return a non-nil pointer to a struct that encodes as
// JSON, since it is encoded after every render.
func checkKeeper(p Page, where string) {
	k, ok := p.(Keeper)
	if !ok {
		return
	}
	v := reflect.ValueOf(k.Keep())
	fix := fmt.Sprintf("return a pointer to a struct field of the state, e.g. func (p %T) Keep() any { return &p.kept }", p)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		panic(&Error{Code: RT452, Where: where, Msg: fmt.Sprintf("%T.Keep returned %s, but must return a pointer to a struct", p, typeName(v)), Fix: fix})
	}
	if _, err := json.Marshal(v.Interface()); err != nil {
		panic(&Error{Code: RT452, Where: where, Msg: fmt.Sprintf("%T.Keep's %s can't be encoded as JSON: %v", p, v.Elem().Type(), err),
			Fix: "keep plain data (strings, numbers, bools, slices, maps and structs of them), such as ids, and load the rest in Mount"})
	}
	if keptFields(v.Elem().Type()) == nil {
		panic(&Error{Code: RT452, Where: where, Msg: fmt.Sprintf("%T.Keep's %s has no exported fields, so nothing would be kept", p, v.Elem().Type()),
			Fix: "export the fields to keep (Station, not station): only exported fields are encoded"})
	}
}

func typeName(v reflect.Value) string {
	if !v.IsValid() {
		return "nil"
	}
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return "a nil " + v.Type().String()
	}
	return v.Type().String()
}

// keptFields returns the JSON names of a kept struct's fields, as the
// manifest lists them, or nil for a page that keeps nothing.
func keptFields(t reflect.Type) []string {
	var names []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		names = append(names, name)
	}
	return names
}

// keeps returns the fields p keeps, or nil if it keeps nothing.
func keeps(p Page) []string {
	k, ok := p.(Keeper)
	if !ok || p == nil {
		return nil
	}
	return keptFields(reflect.TypeOf(k.Keep()).Elem())
}

// usesKeep reports whether any route's page or live layout keeps state.
func (a *App) usesKeep() bool {
	for _, rt := range a.routes {
		if _, ok := rt.prototype.(Keeper); ok {
			return true
		}
		if l := rt.group.appLayout(); l != nil && l.live != nil {
			if _, ok := l.live.(Keeper); ok {
				return true
			}
		}
	}
	return false
}

// checkKeep refuses to serve a production app whose pages keep state
// without Config.Secret (RT453): the key would change with every
// restart, the moment kept state is for, so it would never come back.
func (a *App) checkKeep() error {
	if a.dev || a.cfg.Secret != "" || !a.usesKeep() {
		return nil
	}
	return &Error{Code: RT453, Msg: "pages keep state across a remount (Keep), but the app has no secret to seal it with, so a restart would lose it",
		Fix: "set loom.Config.Secret, e.g. Secret: os.Getenv(\"LOOM_SECRET\"), with a value made by `openssl rand -base64 32`, the same on every node"}
}

// keepSecret is the key kept state is sealed with: Config.Secret, else
// in dev a fixed one, else (tests; Run refuses, RT453) one for the
// process.
func (a *App) keepSecret() string {
	switch {
	case a.cfg.Secret != "":
		return a.cfg.Secret
	case a.dev:
		return devKeepSecret
	}
	a.keepOnce.Do(func() {
		b := make([]byte, 32)
		rand.Read(b)
		a.keepKey = base64.RawURLEncoding.EncodeToString(b)
	})
	return a.keepKey
}

// sealKept compresses and seals kept state for the browser.
func (a *App) sealKept(plain []byte) (string, error) {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.BestCompression)
	w.Write(plain)
	w.Close()
	gcm, err := cipherFor(a.keepSecret(), keptPurpose)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, buf.Bytes(), []byte(keptPurpose))), nil
}

// openKept opens what sealKept made: ErrSealed if this app didn't seal
// it (another secret, or it was altered).
func (a *App) openKept(sealed string) ([]byte, error) {
	gcm, err := cipherFor(a.keepSecret(), keptPurpose)
	if err != nil {
		return nil, err
	}
	b, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil || len(b) < gcm.NonceSize() {
		return nil, ErrSealed
	}
	z, err := gcm.Open(nil, b[:gcm.NonceSize()], b[gcm.NonceSize():], []byte(keptPurpose))
	if err != nil {
		return nil, ErrSealed
	}
	return io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(z)), 1<<20))
}

// Kept returns the page's kept state as the browser holds it, sealed:
// "" if neither the page nor its live layout keeps anything, or what
// they keep is over MaxKept (RT454). The runtime sends it with each
// render that changes it.
func (v *View) Kept() string { return v.kept }

// keep encodes and seals what the page and its live layout keep, after
// a render. It reseals only when the state changed; state over MaxKept
// is reported (RT454) and not kept, so a remount starts afresh rather
// than from an older state.
func (v *View) keep() {
	k := kept{Browser: v.browser}
	if v.live != nil && !v.lent {
		if kp, ok := v.live.page.(Keeper); ok {
			k.Layout = fmt.Sprintf("%T", v.live.page)
			k.LayoutKept, _ = json.Marshal(kp.Keep())
		}
	}
	if kp, ok := v.page.page.(Keeper); ok {
		k.Page = fmt.Sprintf("%T", v.page.page)
		k.PageKept, _ = json.Marshal(kp.Keep())
	}
	if k.Layout == "" && k.Page == "" {
		return
	}
	plain, err := json.Marshal(k)
	if err != nil || bytes.Equal(plain, v.keptPlain) {
		return
	}
	v.keptPlain = plain
	sealed, err := v.ctx.app.sealKept(plain)
	if err == nil && len(sealed) > MaxKept {
		err = &Error{Code: RT454, Where: keepWhere(v),
			Msg: fmt.Sprintf("the state the page keeps (Keep) is %d bytes sealed, over the %d bytes a remount can send back, so it isn't kept: a remount starts afresh", len(sealed), MaxKept),
			Fix: "keep less: ids rather than records, loaded again in Mount, and a bound on any list"}
	}
	if err != nil {
		v.kept = ""
		v.ctx.failed = append(v.ctx.failed, err)
		return
	}
	v.kept = sealed
}

// keepWhere names the type that keeps state, for RT454.
func keepWhere(v *View) string {
	if _, ok := v.page.page.(Keeper); ok {
		return fmt.Sprintf("%T.Keep", v.page.page)
	}
	return fmt.Sprintf("%T.Keep", v.live.page)
}

// restore gives a page's freshly copied state, and its live layout's
// unless a live navigation brought it, what the browser kept for them,
// before Mount. It returns what happened, for page.mount's "kept":
// "restored the layout's", "dropped: another browser's" and so on, or
// "" when there was nothing to restore. A deploy that changed a kept
// type so the old state no longer decodes drops that state alone.
func (v *View) restore(sealed string, carried bool) string {
	if sealed == "" {
		return ""
	}
	plain, err := v.ctx.app.openKept(sealed)
	if err != nil {
		return "dropped: not sealed by this app"
	}
	var k kept
	if err := json.Unmarshal(plain, &k); err != nil {
		return "dropped: unreadable"
	}
	if k.Browser != v.browser {
		return "dropped: another browser's"
	}
	var notes []string
	if v.live != nil && !carried && k.Layout != "" {
		if restoreInto(v.live.page, k.Layout, k.LayoutKept) {
			notes = append(notes, "restored the layout's")
		} else {
			notes = append(notes, "dropped the layout's: it no longer fits "+fmt.Sprintf("%T", v.live.page))
		}
	}
	if k.Page != "" {
		if restoreInto(v.page.page, k.Page, k.PageKept) {
			notes = append(notes, "restored the page's")
		} else {
			notes = append(notes, "dropped the page's: it no longer fits "+fmt.Sprintf("%T", v.page.page))
		}
	}
	return strings.Join(notes, "; ")
}

// restoreInto decodes data into p's kept state if p is still of type
// name and keeps state. It decodes into a fresh value first, so a
// failure leaves p as it was.
func restoreInto(p Page, name string, data json.RawMessage) bool {
	kp, ok := p.(Keeper)
	if !ok || fmt.Sprintf("%T", p) != name {
		return false
	}
	dst := reflect.ValueOf(kp.Keep())
	fresh := reflect.New(dst.Elem().Type())
	if err := json.Unmarshal(data, fresh.Interface()); err != nil {
		return false
	}
	dst.Elem().Set(fresh.Elem())
	return true
}
