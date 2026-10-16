-- loom_jobs: background jobs with a database (docs/jobs.md).
CREATE TABLE IF NOT EXISTS loom_jobs (
	id           bigserial PRIMARY KEY,
	type         text NOT NULL,
	queue        text NOT NULL,
	args         json NOT NULL,
	state        text NOT NULL,
	attempt      integer NOT NULL DEFAULT 0,
	max_attempts integer NOT NULL,
	inserted_at  timestamptz NOT NULL,
	scheduled_at timestamptz NOT NULL,
	attempted_at timestamptz,
	finished_at  timestamptz,
	attempted_by text NOT NULL DEFAULT '',
	lease_until  timestamptz,
	errors       jsonb NOT NULL DEFAULT '[]',
	unique_key   text NOT NULL DEFAULT '',
	trace        text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS loom_jobs_fetch ON loom_jobs (queue, scheduled_at, id) WHERE state IN ('available', 'scheduled', 'retryable');
CREATE INDEX IF NOT EXISTS loom_jobs_lease ON loom_jobs (lease_until) WHERE state = 'executing';
CREATE INDEX IF NOT EXISTS loom_jobs_unique ON loom_jobs (unique_key, inserted_at) WHERE unique_key <> '';
CREATE INDEX IF NOT EXISTS loom_jobs_finished ON loom_jobs (finished_at) WHERE state IN ('completed', 'discarded', 'cancelled');
CREATE INDEX IF NOT EXISTS loom_jobs_state ON loom_jobs (state, id);
-- Tables created by Loom before its migrations existed lack trace.
ALTER TABLE loom_jobs ADD COLUMN IF NOT EXISTS trace text NOT NULL DEFAULT '';
