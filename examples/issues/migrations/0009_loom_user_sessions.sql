-- loom:from github.com/moomerman/loom 0002_loom_user_sessions.sql
-- Installed by `loom db install github.com/moomerman/loom`; applied by `loom migrate` like the app's own.

-- loom_user_sessions: sign-in sessions and one-time sign-in grants, by
-- the hash of their token (docs/live.md, "Signing in").
CREATE TABLE IF NOT EXISTS loom_user_sessions (
	token_hash text PRIMARY KEY,
	kind text NOT NULL,
	user_id text NOT NULL,
	expires_at timestamptz NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS loom_user_sessions_user ON loom_user_sessions (user_id);
