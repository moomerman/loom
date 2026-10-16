package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/moomerman/loom"
)

// The queries behind the pages, on the tables in migrations/. Every
// email comparison is on lower(email), which the unique index covers,
// so Ada@Example.com and ada@example.com are one account.

// User is a person who can sign in.
type User struct {
	ID              string
	Email           string
	EmailVerifiedAt *time.Time // when they first used a mailed link; nil if never
	CreatedAt       time.Time
}

const userColumns = `id, email, email_verified_at, created_at`

// User returns the user with id, or an error matching loom.ErrNotFound.
// An app whose user type is auth.User registers it as the loader:
// loom.Users(app, signIn.User).
func (a *App) User(ctx context.Context, id string) (User, error) {
	return loom.Get[User](ctx, a.app.DB(), `SELECT `+userColumns+` FROM auth_users WHERE id = $1`, id)
}

// UserByEmail returns the user with email, ignoring case, or an error
// matching loom.ErrNotFound.
func (a *App) UserByEmail(ctx context.Context, q loom.Querier, email string) (User, error) {
	return loom.Get[User](ctx, q, `SELECT `+userColumns+` FROM auth_users WHERE lower(email) = lower($1)`, email)
}

// CreateUser adds a user in tx: for seeding, invitations and tests. A
// password of "" makes a user who signs in by link only. It doesn't run
// Config.OnSignUp; the app makes its own rows beside it. An email that
// already has an account is an error matching loom.ErrUniqueViolation.
func (a *App) CreateUser(ctx context.Context, tx loom.Tx, email, password string) (User, error) {
	var hash *string
	if password != "" {
		h, err := HashPassword(password)
		if err != nil {
			return User{}, err
		}
		hash = &h
	}
	return loom.Get[User](ctx, tx, `INSERT INTO auth_users (email, password_hash) VALUES ($1, $2) RETURNING `+userColumns, email, hash)
}

// signUp creates a user and runs Config.OnSignUp, in tx, so the app's
// rows exist exactly when the account does. verified marks the email
// proved, for an account made by following a mailed link.
func (a *App) signUp(ctx context.Context, tx loom.Tx, email, password string, verified bool) (User, error) {
	u, err := a.CreateUser(ctx, tx, email, password)
	if err != nil {
		return User{}, err
	}
	if verified {
		if u, _, err = a.verify(ctx, tx, u.ID); err != nil {
			return User{}, err
		}
	}
	if a.cfg.OnSignUp != nil {
		if err := a.cfg.OnSignUp(ctx, tx, u); err != nil {
			return User{}, err
		}
	}
	return u, nil
}

// verify records that the user has shown they read mail sent to their
// address (or a provider vouched for it), the first time they do.
//
// Proving the email wins: an account whose email was never proved may
// have been made by someone else, who typed this address at sign-up
// with a password of their own. So the first proof removes every way
// in that the account had before it: the password, provider
// identities, the authenticator and recovery codes, and sign-ins
// waiting for a code. claimed reports that it did, so the caller also
// ends the account's sessions (App.SignOutEverywhere) once tx commits.
func (a *App) verify(ctx context.Context, tx loom.Tx, id string) (u User, claimed bool, err error) {
	claimed, err = loom.Get[bool](ctx, tx, `SELECT email_verified_at IS NULL FROM auth_users WHERE id = $1 FOR UPDATE`, id)
	if err != nil {
		return User{}, false, err
	}
	if claimed {
		for _, q := range []string{
			`UPDATE auth_users SET password_hash = NULL WHERE id = $1`,
			`DELETE FROM auth_identities WHERE user_id = $1`,
			`DELETE FROM auth_totp WHERE user_id = $1`,
			`DELETE FROM auth_recovery_codes WHERE user_id = $1`,
			`DELETE FROM auth_challenges WHERE user_id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, id); err != nil {
				return User{}, false, err
			}
		}
	}
	u, err = loom.Get[User](ctx, tx, `UPDATE auth_users SET email_verified_at = coalesce(email_verified_at, now()) WHERE id = $1 RETURNING `+userColumns, id)
	return u, claimed, err
}

// checkPassword returns the user whose email and password these are, or
// ErrWrongPassword, taking as long whether or not the account exists.
func (a *App) checkPassword(ctx context.Context, email, password string) (User, error) {
	row, err := loom.Get[userWithHash](ctx, a.app.DB(), `SELECT `+userColumns+`, password_hash FROM auth_users WHERE lower(email) = lower($1)`, email)
	if err != nil && !errors.Is(err, loom.ErrNotFound) {
		return User{}, err
	}
	hash := ""
	if row.PasswordHash != nil {
		hash = *row.PasswordHash
	}
	if err := CheckPassword(hash, password); err != nil {
		return User{}, err
	}
	return User{ID: row.ID, Email: row.Email, EmailVerifiedAt: row.EmailVerifiedAt, CreatedAt: row.CreatedAt}, nil
}

// userWithHash is a user's row with their password hash, which never
// leaves this file.
type userWithHash struct {
	ID              string
	Email           string
	EmailVerifiedAt *time.Time
	CreatedAt       time.Time
	PasswordHash    *string
}

// setPassword stores a new password for the user.
func (a *App) setPassword(ctx context.Context, q loom.Querier, id, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE auth_users SET password_hash = $2 WHERE id = $1`, id, hash)
	return err
}

// deleteLinks deletes every link, of either kind, sent to email: once
// the password changes or every session is signed out, a link mailed
// before then must not sign anyone in.
func (a *App) deleteLinks(ctx context.Context, q loom.Querier, email string) error {
	_, err := q.Exec(ctx, `DELETE FROM auth_links WHERE lower(email) = lower($1)`, email)
	return err
}

// hasPassword reports whether the user has a password, so the account
// page asks for it before changing it.
func (a *App) hasPassword(ctx context.Context, id string) (bool, error) {
	return loom.Get[bool](ctx, a.app.DB(), `SELECT password_hash IS NOT NULL FROM auth_users WHERE id = $1`, id)
}

// signInWith returns the user an identity at a provider signs in as:
// the one it signed in as before; else the account with its email,
// which it joins; else a new account, unless sign-up is off. Only an
// email the provider has verified finds or makes an account, so nobody
// claims another's account by typing their address at a provider. why
// says why they can't sign in: "unverified" or "no_account".
func (a *App) signInWith(ctx context.Context, provider string, id Identity) (u User, why string, err error) {
	var claimed bool
	err = a.app.DB().Tx(ctx, func(tx loom.Tx) error {
		u, err = loom.Get[User](ctx, tx, `SELECT u.id, u.email, u.email_verified_at, u.created_at
			FROM auth_identities i JOIN auth_users u ON u.id = i.user_id WHERE i.provider = $1 AND i.subject = $2`, provider, id.Subject)
		if !isNotFound(err) {
			return err
		}
		if !id.EmailVerified || id.Email == "" {
			why = "unverified"
			return nil
		}
		u, err = a.UserByEmail(ctx, tx, id.Email)
		switch {
		case isNotFound(err) && a.cfg.NoSignUp:
			why = "no_account"
			return nil
		case isNotFound(err):
			u, err = a.signUp(ctx, tx, id.Email, "", true)
		case err == nil:
			u, claimed, err = a.verify(ctx, tx, u.ID)
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO auth_identities (provider, subject, user_id, email) VALUES ($1, $2, $3, $4)`, provider, id.Subject, u.ID, id.Email)
		return err
	})
	if err == nil && claimed {
		err = a.app.SignOutEverywhere(ctx, u.ID)
	}
	return u, why, err
}

// identities returns the providers a user has signed in with, for the
// account page.
func (a *App) identities(ctx context.Context, userID string) ([]string, error) {
	return loom.Select[string](ctx, a.app.DB(), `SELECT provider FROM auth_identities WHERE user_id = $1 ORDER BY created_at`, userID)
}

// Mailed links. A link's secret is in the mail only; the table keeps its
// SHA-256, its kind and the email it was sent to.

const (
	kindSignIn = "sign_in"
	kindReset  = "reset"
)

// link is a stored link.
type link struct {
	Kind  string
	Email string
	Live  bool // not expired
}

// newLink stores a link of kind for email, valid for Config.LinkFor,
// and returns its secret. It replaces any earlier link of the kind for
// the email, so only the newest mail's link works, and prunes expired
// links on the way.
func (a *App) newLink(ctx context.Context, q loom.Querier, kind, email string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(b)
	if _, err := q.Exec(ctx, `DELETE FROM auth_links WHERE expires_at < now() OR (kind = $1 AND lower(email) = lower($2))`, kind, email); err != nil {
		return "", err
	}
	_, err := q.Exec(ctx, `INSERT INTO auth_links (token_hash, kind, email, expires_at) VALUES ($1, $2, $3, now() + $4 * interval '1 second')`,
		hashSecret(secret), kind, email, int64(a.cfg.LinkFor/time.Second))
	return secret, err
}

// peekLink returns the unexpired link of kind with secret, leaving it,
// so a page can show whose it is before it is used.
func (a *App) peekLink(ctx context.Context, kind, secret string) (link, bool, error) {
	return oneLink(loom.Get[link](ctx, a.app.DB(), `SELECT kind, email, expires_at > now() AS live FROM auth_links WHERE token_hash = $1 AND kind = $2`, hashSecret(secret), kind))
}

// takeLink uses the link: it deletes it and returns it if it was
// unexpired, in one statement, so a link works once even when two
// requests race for it.
func (a *App) takeLink(ctx context.Context, q loom.Querier, kind, secret string) (link, bool, error) {
	return oneLink(loom.Get[link](ctx, q, `DELETE FROM auth_links WHERE token_hash = $1 AND kind = $2 RETURNING kind, email, expires_at > now() AS live`, hashSecret(secret), kind))
}

func oneLink(l link, err error) (link, bool, error) {
	if errors.Is(err, loom.ErrNotFound) {
		return link{}, false, nil
	}
	if err != nil {
		return link{}, false, err
	}
	return l, l.Live, nil
}

// hashSecret is the stored form of a link's secret.
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func isNotFound(err error) bool { return errors.Is(err, loom.ErrNotFound) }

// Two-factor sign-in (twofactor.go): the account's authenticator, its
// recovery codes, and sign-ins waiting for a code.

const (
	challengeFor      = 10 * time.Minute // how long the code page waits
	challengeAttempts = 5                // wrong codes before it gives up
	recoveryCodes     = 10               // made at a time
)

// totp is an account's authenticator.
type totp struct {
	Secret    string
	EnabledAt *time.Time // nil: being set up
	LastStep  int64
	Locked    bool // too many wrong codes: none is accepted for now
}

// totpPurpose is what authenticator secrets are sealed for (loom's
// App.Seal), so no other sealed value opens as one.
const totpPurpose = "auth.totp"

// sealTOTP is an authenticator secret as stored: encrypted with the
// app's secret, or as it is in dev without one.
func (a *App) sealTOTP(secret string) (string, error) {
	if !a.app.HasSecret() {
		return secret, nil
	}
	return a.app.Seal(totpPurpose, []byte(secret))
}

// openTOTP is the authenticator secret t holds. A secret stored before
// the app had one is plain (checkSecret seals those at startup).
func (a *App) openTOTP(t *totp, userID string) error {
	if !loom.IsSealed(t.Secret) {
		return nil
	}
	b, err := a.app.Open(totpPurpose, t.Secret)
	if err != nil {
		return &loom.Error{Code: AU908, Msg: fmt.Sprintf("the two-factor secret of user %s can't be decrypted: %v", userID, err),
			Fix: "restore the loom.Config.Secret it was sealed with, the same on every node; if it is lost, the person signs in with a recovery code, or DELETE their auth_totp row so they set two-factor up again"}
	}
	t.Secret = string(b)
	return nil
}

// totpOf returns the user's authenticator, set up or being set up, or
// false if they have none.
func (a *App) totpOf(ctx context.Context, q loom.Querier, userID string) (totp, bool, error) {
	t, err := loom.Get[totp](ctx, q, `SELECT secret, enabled_at, last_step, coalesce(locked_until > now(), false) AS locked FROM auth_totp WHERE user_id = $1`, userID)
	if isNotFound(err) {
		return totp{}, false, nil
	}
	if err == nil {
		err = a.openTOTP(&t, userID)
	}
	return t, err == nil, err
}

// checkSecret is the startup check, with two-factor on, that the app
// has a secret to encrypt authenticators with outside dev (AU909). With
// one, it encrypts secrets stored before the app had it.
func (a *App) checkSecret(ctx context.Context) error {
	if !a.app.HasSecret() {
		if a.app.Dev() {
			return nil
		}
		return &loom.Error{Code: AU909, Msg: "two-factor sign-in is on, but loom.Config.Secret is empty, so authenticator secrets would be stored unencrypted",
			Fix: "make a secret with `openssl rand -base64 32` and pass it from the environment, the same on every node: loom.New(loom.Config{Secret: os.Getenv(\"LOOM_SECRET\")})"}
	}
	db := a.app.DB()
	if db.SQL() == nil {
		return nil
	}
	type plain struct{ UserID, Secret string }
	rows, err := loom.Select[plain](ctx, db, `SELECT user_id, secret FROM auth_totp WHERE secret NOT LIKE 'v1:%'`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		sealed, err := a.sealTOTP(r.Secret)
		if err != nil {
			return err
		}
		if _, err := db.Exec(ctx, `UPDATE auth_totp SET secret = $2 WHERE user_id = $1 AND secret = $3`, r.UserID, sealed, r.Secret); err != nil {
			return err
		}
	}
	return nil
}

// twoFactorOn reports whether the user signs in with a code.
func (a *App) twoFactorOn(ctx context.Context, q loom.Querier, userID string) (bool, error) {
	t, ok, err := a.totpOf(ctx, q, userID)
	return ok && t.EnabledAt != nil, err
}

// pendingTOTP returns the secret of the authenticator the user is
// setting up, making one the first time, so a page reloaded or remounted
// after a restart shows the same QR code the person already scanned.
// It returns "" if two-factor is on already.
func (a *App) pendingTOTP(ctx context.Context, userID string) (string, error) {
	secret, err := newTOTPSecret()
	if err != nil {
		return "", err
	}
	if secret, err = a.sealTOTP(secret); err != nil {
		return "", err
	}
	db := a.app.DB()
	if _, err := db.Exec(ctx, `INSERT INTO auth_totp (user_id, secret) VALUES ($1, $2) ON CONFLICT (user_id) DO NOTHING`, userID, secret); err != nil {
		return "", err
	}
	t, _, err := a.totpOf(ctx, db, userID)
	if err != nil || t.EnabledAt != nil {
		return "", err
	}
	return t.Secret, nil
}

// enableTOTP turns two-factor on, if code is from the authenticator
// being set up, and returns the account's new recovery codes.
func (a *App) enableTOTP(ctx context.Context, userID, code string) (codes []string, ok bool, err error) {
	err = a.app.DB().Tx(ctx, func(tx loom.Tx) error {
		t, err := loom.Get[totp](ctx, tx, `SELECT secret, enabled_at, last_step FROM auth_totp WHERE user_id = $1 AND enabled_at IS NULL FOR UPDATE`, userID)
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := a.openTOTP(&t, userID); err != nil {
			return err
		}
		step, match := checkTOTP(t.Secret, code, time.Now())
		if !match {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE auth_totp SET enabled_at = now(), last_step = $2 WHERE user_id = $1`, userID, step); err != nil {
			return err
		}
		codes, err = a.newRecoveryCodes(ctx, tx, userID)
		ok = err == nil
		return err
	})
	return codes, ok, err
}

// disableTOTP turns two-factor off: the authenticator and the recovery
// codes are deleted.
func (a *App) disableTOTP(ctx context.Context, userID string) error {
	return a.app.DB().Tx(ctx, func(tx loom.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM auth_recovery_codes WHERE user_id = $1`, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM auth_totp WHERE user_id = $1`, userID)
		return err
	})
}

// codeLockAfter wrong codes in a row, across sign-ins and the account
// page, lock an account's codes for codeLockFor: a challenge allows five
// tries, but a password opens a new challenge, so without this an
// attacker who has the password could try every code in a few hours.
const (
	codeLockAfter = 10
	codeLockFor   = 15 * time.Minute
)

// errCodesLocked is useCode's answer while the account's codes are
// locked: no code is checked, right or wrong.
var errCodesLocked = errors.New("auth: too many wrong two-factor codes")

// useCode reports whether code signs the user in, using it up: six
// digits from their authenticator, newer than the last code used, or one
// of their recovery codes. A wrong code counts towards the lock; while
// it is on, every code is refused with errCodesLocked.
func (a *App) useCode(ctx context.Context, q loom.Querier, userID, code string) (bool, error) {
	t, ok, err := a.totpOf(ctx, q, userID)
	if err != nil || !ok || t.EnabledAt == nil {
		return false, err
	}
	if t.Locked {
		return false, errCodesLocked
	}
	used, err := a.spendCode(ctx, q, userID, t, code)
	if err != nil {
		return false, err
	}
	if used {
		_, err = q.Exec(ctx, `UPDATE auth_totp SET failed = 0 WHERE user_id = $1`, userID)
		return err == nil, err
	}
	_, err = q.Exec(ctx, `UPDATE auth_totp SET
	failed = CASE WHEN failed + 1 >= $2 THEN 0 ELSE failed + 1 END,
	locked_until = CASE WHEN failed + 1 >= $2 THEN now() + make_interval(secs => $3) ELSE locked_until END
WHERE user_id = $1`, userID, codeLockAfter, codeLockFor.Seconds())
	return false, err
}

// spendCode uses code up if it is right.
func (a *App) spendCode(ctx context.Context, q loom.Querier, userID string, t totp, code string) (bool, error) {
	code = normalizeCode(code)
	if len(code) == totpDigits && strings.Trim(code, "0123456789") == "" {
		step, match := checkTOTP(t.Secret, code, time.Now())
		if !match {
			return false, nil
		}
		// The step only moves forward, in one statement, so a code (or an
		// older one) can't be used twice, even by two requests at once.
		res, err := q.Exec(ctx, `UPDATE auth_totp SET last_step = $2 WHERE user_id = $1 AND last_step < $2 AND enabled_at IS NOT NULL`, userID, step)
		return affected(res, err)
	}
	res, err := q.Exec(ctx, `DELETE FROM auth_recovery_codes WHERE user_id = $1 AND code_hash = $2`, userID, hashSecret(code))
	return affected(res, err)
}

func affected(res interface{ RowsAffected() (int64, error) }, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// newRecoveryCodes replaces the user's recovery codes and returns the
// new ones, to show once: ten codes of ten base32 characters (50 bits),
// written "abcde-fghij".
func (a *App) newRecoveryCodes(ctx context.Context, q loom.Querier, userID string) ([]string, error) {
	if _, err := q.Exec(ctx, `DELETE FROM auth_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return nil, err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	codes := make([]string, recoveryCodes)
	for i := range codes {
		b := make([]byte, 7)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		s := strings.ToLower(enc.EncodeToString(b))[:10]
		codes[i] = s[:5] + "-" + s[5:]
		if _, err := q.Exec(ctx, `INSERT INTO auth_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, hashSecret(normalizeCode(codes[i]))); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

// recoveryCodesLeft counts the user's unused recovery codes.
func (a *App) recoveryCodesLeft(ctx context.Context, userID string) (int, error) {
	return loom.Get[int](ctx, a.app.DB(), `SELECT count(*)::int FROM auth_recovery_codes WHERE user_id = $1`, userID)
}

// newChallenge stores a sign-in waiting for the user's code and returns
// its secret. Expired ones are pruned on the way.
func (a *App) newChallenge(ctx context.Context, userID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(b)
	db := a.app.DB()
	if _, err := db.Exec(ctx, `DELETE FROM auth_challenges WHERE expires_at < now()`); err != nil {
		return "", err
	}
	_, err := db.Exec(ctx, `INSERT INTO auth_challenges (token_hash, user_id, expires_at) VALUES ($1, $2, now() + $3 * interval '1 second')`,
		hashSecret(secret), userID, int64(challengeFor/time.Second))
	return secret, err
}

// peekChallenge returns the user an unexpired challenge is for.
func (a *App) peekChallenge(ctx context.Context, secret string) (string, bool, error) {
	return oneID(loom.Get[string](ctx, a.app.DB(), `SELECT user_id FROM auth_challenges WHERE token_hash = $1 AND expires_at > now()`, hashSecret(secret)))
}

// attemptChallenge counts a code typed for a challenge and returns its
// user, or false once it has expired or had challengeAttempts tries, when
// it is deleted: the person signs in again, password first.
func (a *App) attemptChallenge(ctx context.Context, secret string) (string, bool, error) {
	db := a.app.DB()
	id, ok, err := oneID(loom.Get[string](ctx, db, `UPDATE auth_challenges SET attempts = attempts + 1
		WHERE token_hash = $1 AND expires_at > now() AND attempts < $2 RETURNING user_id`, hashSecret(secret), challengeAttempts))
	if err == nil && !ok {
		_, err = db.Exec(ctx, `DELETE FROM auth_challenges WHERE token_hash = $1`, hashSecret(secret))
	}
	return id, ok, err
}

// takeChallenge uses a challenge: it deletes it and returns its user if
// it was unexpired, in one statement, so it signs in once.
func (a *App) takeChallenge(ctx context.Context, q loom.Querier, secret string) (string, bool, error) {
	return oneID(loom.Get[string](ctx, q, `DELETE FROM auth_challenges WHERE token_hash = $1 RETURNING CASE WHEN expires_at > now() THEN user_id ELSE '' END`, hashSecret(secret)))
}

func oneID(id string, err error) (string, bool, error) {
	if isNotFound(err) {
		return "", false, nil
	}
	return id, err == nil && id != "", err
}
