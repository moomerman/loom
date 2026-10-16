-- Loom's own tables. Install them in an app with `loom db install
-- github.com/moomerman/loom` (`loom new` does), then `loom migrate`.
--
-- loom_clients: the last event each browser tab had handled, so an event
-- it resends after a restart runs once (docs/live.md, "Events").
CREATE TABLE IF NOT EXISTS loom_clients (
	client_id text PRIMARY KEY,
	last_seq bigint NOT NULL,
	seen_at timestamptz NOT NULL DEFAULT now()
);
