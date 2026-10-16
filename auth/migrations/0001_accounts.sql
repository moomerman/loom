-- People who can sign in, and how. The app's own tables refer to them
-- by id (an app's users table can share it: auth.Config.OnSignUp makes
-- the app's row in the same transaction). An OAuth provider or an OIDC
-- issuer added later is a table of its own keyed by user_id; nothing
-- here assumes passwords.
CREATE TABLE auth_users (
	id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
	email text NOT NULL,
	password_hash text,               -- null: signs in by link only
	email_verified_at timestamptz,    -- set once a mailed link is used
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX auth_users_email ON auth_users (lower(email));

-- One-time links sent by email: sign in, or set a new password. Only
-- the SHA-256 of a link's secret is stored, so a leaked table or backup
-- opens no account. Used links are deleted; expired ones are pruned.
CREATE TABLE auth_links (
	token_hash text PRIMARY KEY,
	kind text NOT NULL CHECK (kind IN ('sign_in', 'reset')),
	email text NOT NULL,
	expires_at timestamptz NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX auth_links_email ON auth_links (lower(email));
