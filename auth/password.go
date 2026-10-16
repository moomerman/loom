package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/moomerman/loom"
)

// Passwords are hashed with PBKDF2-HMAC-SHA256 (RFC 8018) from Go's
// crypto/pbkdf2, 600,000 iterations and a 16-byte random salt: OWASP's
// recommendation for PBKDF2, and the strongest password hash in the
// standard library (bcrypt and argon2 are in golang.org/x/crypto,
// which would be the runtime's second dependency; docs/design.md). A hash
// records its iterations, so raising them later still checks old ones.
//
//	pbkdf2-sha256$600000$<salt>$<key>     (base64, no padding)

const (
	iterations  = 600_000
	MinPassword = 8    // the shortest password HashPassword takes, in bytes
	MaxPassword = 1024 // the longest
	hashScheme  = "pbkdf2-sha256"
	saltLen     = 16
	keyLen      = 32
)

// HashPassword returns a hash of password to store. A password shorter
// than MinPassword or longer than MaxPassword is AU905; the sign-up and
// reset forms check the length before they get here.
func HashPassword(password string) (string, error) {
	if len(password) < MinPassword || len(password) > MaxPassword {
		return "", &loom.Error{Code: AU905, Msg: fmt.Sprintf("a password of %d bytes can't be hashed: passwords are %d to %d bytes", len(password), MinPassword, MaxPassword),
			Fix: "check the length where the person types it, and ask for a longer password"}
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, keyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s$%d$%s$%s", hashScheme, iterations, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// CheckPassword reports whether password matches hash: nil, or
// ErrWrongPassword. Pass "" as the hash when there is no such user (or
// the user has no password): it takes as long as a real check, so the
// time taken doesn't tell an attacker which emails have accounts.
func CheckPassword(hash, password string) error {
	if hash == "" {
		pbkdf2.Key(sha256.New, password, make([]byte, saltLen), iterations, keyLen)
		return ErrWrongPassword
	}
	iter, salt, key, err := parseHash(hash)
	if err != nil {
		return err
	}
	if len(password) > MaxPassword {
		return ErrWrongPassword
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(key))
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return ErrWrongPassword
	}
	return nil
}

var b64 = base64.RawStdEncoding

func parseHash(hash string) (iter int, salt, key []byte, err error) {
	parts := strings.Split(hash, "$")
	if len(parts) == 4 && parts[0] == hashScheme {
		iter, err = strconv.Atoi(parts[1])
		if err == nil && iter > 0 {
			salt, err = b64.DecodeString(parts[2])
			if err == nil {
				key, err = b64.DecodeString(parts[3])
				if err == nil && len(key) > 0 {
					return iter, salt, key, nil
				}
			}
		}
	}
	return 0, nil, nil, &loom.Error{Code: AU904, Msg: "the stored password hash isn't one auth.HashPassword made",
		Fix: "store the string auth.HashPassword returns, unchanged, in auth_users.password_hash; to recover, clear it and have the person reset their password"}
}
