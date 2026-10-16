-- loom:from github.com/moomerman/loom 0005_loom_nodes.sql
-- Installed by `loom db install github.com/moomerman/loom`; applied by `loom migrate` like the app's own.

-- loom_nodes: the job runners' node registry (docs/jobs.md). Each node
-- records its queues, running counts and leadership every third of its
-- lease; one whose lease has ended is stale.
CREATE TABLE IF NOT EXISTS loom_nodes (
	name        text PRIMARY KEY,
	queues      jsonb NOT NULL DEFAULT '[]',
	leader      boolean NOT NULL DEFAULT false,
	started_at  timestamptz NOT NULL,
	seen_at     timestamptz NOT NULL,
	lease_until timestamptz NOT NULL
);
