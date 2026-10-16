package auth

import "errors"

// Auth error codes, documented in docs/errors.md. The errors are
// *loom.Error, so `loom errors` and loomtest show their code and fix.
// Startup errors (AU901 to AU903, AU906, AU909) stop app.Run and fail loomtest.Mount.
const (
	AU901 = "AU901" // email is on, but the app has no mailer
	AU902 = "AU902" // email or a provider is on, but loom.Config.BaseURL is empty outside dev
	AU903 = "AU903" // a Config field is invalid
	AU904 = "AU904" // a stored password hash isn't one HashPassword made
	AU905 = "AU905" // a password is too short or too long to hash
	AU906 = "AU906" // a provider has no client ID or secret
	AU907 = "AU907" // a provider refused a sign-in, or its answer couldn't be read
	AU908 = "AU908" // a stored two-factor secret can't be decrypted with loom.Config.Secret
	AU909 = "AU909" // two-factor is on, but loom.Config.Secret is empty outside dev
)

// ErrWrongPassword is returned by CheckPassword when the password
// doesn't match, or there is no such user. Tell the person "wrong email
// or password", never which of the two.
var ErrWrongPassword = errors.New("wrong email or password")
