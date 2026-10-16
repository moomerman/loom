-- Accounts at other sites that sign in as a user: Google, GitHub. A user
-- can have several. (provider, subject) is the other site's stable id for
-- the person, never their email, which can change there. An OpenID
-- Connect issuer added later is one more provider name here.
CREATE TABLE auth_identities (
	provider text NOT NULL,
	subject text NOT NULL,
	user_id text NOT NULL REFERENCES auth_users (id) ON DELETE CASCADE,
	email text NOT NULL,              -- the email the provider gave at first sign-in
	created_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (provider, subject)
);
CREATE INDEX auth_identities_user ON auth_identities (user_id);
