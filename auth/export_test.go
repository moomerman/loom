package auth

import "time"

// Code is the authenticator code for secret (spaced as the set-up page
// shows it) at t, for the tests of package auth_test.
func Code(secret string, t time.Time) string {
	c, err := totpCode(normalizeKey(secret), totpStep(t))
	if err != nil {
		panic(err)
	}
	return c
}

func normalizeKey(s string) string {
	out := []rune{}
	for _, r := range s {
		if r != ' ' {
			out = append(out, r)
		}
	}
	return string(out)
}
