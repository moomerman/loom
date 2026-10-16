-- loom:from github.com/moomerman/loom 0003_loom_rate_limits.sql
-- Installed by `loom db install github.com/moomerman/loom`; applied by `loom migrate` like the app's own.

-- loom_rate_limits: loom.RateLimit's counts, two windows per key, so
-- limits hold across nodes (docs/live.md, "Rate limits").
CREATE TABLE IF NOT EXISTS loom_rate_limits (
	key text NOT NULL,
	bucket bigint NOT NULL,
	hits int NOT NULL,
	expires_at timestamptz NOT NULL,
	PRIMARY KEY (key, bucket)
);
