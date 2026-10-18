package loom

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// The app secret (Config.Secret) encrypts values the app must keep but
// can't hash, such as two-factor secrets, so a leaked database or backup
// doesn't leak them. Seal and Open are AES-256-GCM with a key derived
// from the secret for each purpose, so a value sealed for one purpose
// can't be opened as another.

// MinSecretLen is the shortest Config.Secret New accepts.
const MinSecretLen = 32

// sealPrefix marks a sealed value, and its format's version.
const sealPrefix = "v1:"

// ErrNoSecret is returned by Seal and Open when Config.Secret is empty.
var ErrNoSecret = errors.New("loom: the app has no secret (loom.Config.Secret)")

// ErrSealed is returned by Open for a value that wasn't sealed with this
// secret and purpose: the secret changed, or the value was altered.
var ErrSealed = errors.New("loom: the value can't be opened with this secret and purpose")

// HasSecret reports whether the app has a secret (Config.Secret), so
// Seal and Open work.
func (a *App) HasSecret() bool { return a.cfg.Secret != "" }

// Seal encrypts plaintext for purpose (e.g. "auth.totp") with the app's
// secret. The result is text, safe to store in a column. ErrNoSecret
// without Config.Secret.
func (a *App) Seal(purpose string, plaintext []byte) (string, error) {
	gcm, err := a.cipher(purpose)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, plaintext, []byte(purpose))
	return sealPrefix + base64.RawURLEncoding.EncodeToString(out), nil
}

// Open decrypts a value Seal made for purpose: ErrNoSecret without
// Config.Secret, ErrSealed if it wasn't sealed with this secret and
// purpose.
func (a *App) Open(purpose, sealed string) ([]byte, error) {
	gcm, err := a.cipher(purpose)
	if err != nil {
		return nil, err
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(sealed, sealPrefix))
	if !strings.HasPrefix(sealed, sealPrefix) || err != nil || len(b) < gcm.NonceSize() {
		return nil, ErrSealed
	}
	out, err := gcm.Open(nil, b[:gcm.NonceSize()], b[gcm.NonceSize():], []byte(purpose))
	if err != nil {
		return nil, ErrSealed
	}
	return out, nil
}

// IsSealed reports whether s is a value Seal made, rather than one
// stored before the app had a secret.
func IsSealed(s string) bool { return strings.HasPrefix(s, sealPrefix) }

// cipher is AES-256-GCM keyed by HMAC-SHA256(secret, purpose).
func (a *App) cipher(purpose string) (cipher.AEAD, error) {
	if a.cfg.Secret == "" {
		return nil, ErrNoSecret
	}
	return cipherFor(a.cfg.Secret, purpose)
}

func cipherFor(secret, purpose string) (cipher.AEAD, error) {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("loom.seal:" + purpose))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
