-- Two-factor sign-in: after a password, a link or a provider, a code
-- from an authenticator app, or one of the account's recovery codes.
-- Whether it is offered or required is the app's auth.Config.TwoFactor.

-- The account's authenticator: the secret the app was given by QR code,
-- kept as it is, since checking a code needs it (unlike a password).
-- A row with no enabled_at is being set up: the secret is shown, and
-- turns on once a code from it is typed back.
CREATE TABLE auth_totp (
	user_id text PRIMARY KEY REFERENCES auth_users (id) ON DELETE CASCADE,
	secret text NOT NULL,                 -- base32, as authenticator apps take it
	enabled_at timestamptz,               -- null: being set up
	last_step bigint NOT NULL DEFAULT 0,  -- the 30-second step of the last code used: each code works once
	created_at timestamptz NOT NULL DEFAULT now()
);

-- One-time codes for when the authenticator is lost, shown once when
-- made. Only their SHA-256 is stored; a used code is deleted.
CREATE TABLE auth_recovery_codes (
	user_id text NOT NULL REFERENCES auth_users (id) ON DELETE CASCADE,
	code_hash text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (user_id, code_hash)
);

-- Sign-ins halfway: the first step passed, the code not yet typed. The
-- browser holds the secret in a cookie; a challenge lasts ten minutes
-- and takes five wrong codes before it is deleted.
CREATE TABLE auth_challenges (
	token_hash text PRIMARY KEY,
	user_id text NOT NULL REFERENCES auth_users (id) ON DELETE CASCADE,
	attempts int NOT NULL DEFAULT 0,
	expires_at timestamptz NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX auth_challenges_user ON auth_challenges (user_id);
