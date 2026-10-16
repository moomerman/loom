package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Time-based one-time passwords (RFC 6238), the six-digit codes of
// authenticator apps: HMAC-SHA1 over the count of 30-second steps since
// 1970, with a 160-bit secret shared once by QR code. These are the
// parameters every authenticator app supports; others (SHA-256, eight
// digits) are ignored by some apps, so codes would silently differ.

const (
	totpPeriod = 30 // seconds a code lasts
	totpDigits = 6
	totpSkew   = 1 // steps either side of now that are accepted, for clock drift
)

// base32NoPad is how authenticator apps take secrets: RFC 4648 base32,
// without padding.
var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret returns a new random secret, base32 as apps take it.
func newTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32NoPad.EncodeToString(b), nil
}

// totpCode is the code for a step of a secret.
func totpCode(secret string, step int64) (string, error) {
	key, err := base32NoPad.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("a TOTP secret isn't base32: %w", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, n%1_000_000), nil
}

// totpStep is the step a time falls in.
func totpStep(t time.Time) int64 { return t.Unix() / totpPeriod }

// checkTOTP returns the step whose code is code, within totpSkew steps
// of now, or false. Callers record the step and refuse it, and any
// earlier one, next time, so a code works once.
func checkTOTP(secret, code string, now time.Time) (int64, bool) {
	code = normalizeCode(code)
	if len(code) != totpDigits {
		return 0, false
	}
	step := totpStep(now)
	for s := step - totpSkew; s <= step+totpSkew; s++ {
		want, err := totpCode(secret, s)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

// normalizeCode drops what people type around a code: spaces and
// dashes ("123 456", "abcde-fghij"), and case.
func normalizeCode(code string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, code))
}

// totpURI is the otpauth:// address an authenticator app reads from the
// QR code: the secret, and labels naming the app and the account.
func totpURI(issuer, account, secret string) string {
	return "otpauth://totp/" + uriEscape(issuer) + ":" + uriEscape(account) + "?secret=" + secret + "&issuer=" + uriEscape(issuer)
}

// uriEscape escapes a label or a query value, a space as %20: some apps
// show a "+" as it is.
func uriEscape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

// groupSecret spaces a secret in fours, for typing it in by hand.
func groupSecret(secret string) string {
	var b strings.Builder
	for i, r := range secret {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}
