-- The issue tracker's tables. Designed for the sync engine: every table a
-- team's pages read carries team_id, so one shape per table,
-- "team_id = ?", replicates a team's data with predicates on the row's
-- own columns only (no joins). Every such table is tracked: each change
-- gets a version, and deletes leave tombstones.

-- People who can sign in. Shared by every team, so not team-scoped; a
-- shape replicates them whole (they hold no secrets).
CREATE TABLE users (
	id text PRIMARY KEY,               -- a short handle, e.g. "ada"
	name text NOT NULL,
	email text NOT NULL UNIQUE,
	color text NOT NULL DEFAULT 'slate' -- the avatar color, a token name
);
-- loom:track users

-- Teams own issues. key prefixes issue identifiers: ENG-12.
CREATE TABLE teams (
	id bigserial PRIMARY KEY,
	key text NOT NULL UNIQUE,
	name text NOT NULL,
	color text NOT NULL DEFAULT 'indigo',
	issue_count integer NOT NULL DEFAULT 0 -- the last issue number given out
);
-- loom:track teams

CREATE TABLE team_members (
	team_id bigint NOT NULL REFERENCES teams ON DELETE CASCADE,
	user_id text NOT NULL REFERENCES users ON DELETE CASCADE,
	PRIMARY KEY (team_id, user_id)
);
CREATE INDEX team_members_user ON team_members (user_id);
-- loom:track team_members

CREATE TABLE labels (
	id bigserial PRIMARY KEY,
	team_id bigint NOT NULL REFERENCES teams ON DELETE CASCADE,
	name text NOT NULL,
	color text NOT NULL DEFAULT 'slate',
	UNIQUE (team_id, name)
);
-- loom:track labels

-- status is one of backlog, todo, in_progress, in_review, done,
-- canceled. priority is 0 (none), 1 (urgent), 2 (high), 3 (medium) or
-- 4 (low), as in Linear, so "order by priority" needs 0 last.
CREATE TABLE issues (
	id bigserial PRIMARY KEY,
	team_id bigint NOT NULL REFERENCES teams ON DELETE CASCADE,
	number integer NOT NULL,
	title text NOT NULL,
	description text NOT NULL DEFAULT '',
	status text NOT NULL DEFAULT 'todo'
		CHECK (status IN ('backlog', 'todo', 'in_progress', 'in_review', 'done', 'canceled')),
	priority integer NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 4),
	assignee_id text REFERENCES users ON DELETE SET NULL,
	creator_id text NOT NULL REFERENCES users,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	completed_at timestamptz,
	UNIQUE (team_id, number)
);
CREATE INDEX issues_assignee ON issues (assignee_id);
-- loom:track issues

-- team_id is copied from the issue so the table can be shaped by team.
CREATE TABLE issue_labels (
	issue_id bigint NOT NULL REFERENCES issues ON DELETE CASCADE,
	label_id bigint NOT NULL REFERENCES labels ON DELETE CASCADE,
	team_id bigint NOT NULL REFERENCES teams ON DELETE CASCADE,
	PRIMARY KEY (issue_id, label_id)
);
CREATE INDEX issue_labels_team ON issue_labels (team_id);
-- loom:track issue_labels

CREATE TABLE comments (
	id bigserial PRIMARY KEY,
	issue_id bigint NOT NULL REFERENCES issues ON DELETE CASCADE,
	team_id bigint NOT NULL REFERENCES teams ON DELETE CASCADE,
	author_id text NOT NULL REFERENCES users,
	body text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX comments_issue ON comments (issue_id, id);
-- loom:track comments

-- The history shown on an issue: "Ada changed status from Todo to Done".
-- kind is created, status, priority, assignee, title or labels; from_value
-- and to_value are display text, recorded when the change happens.
CREATE TABLE issue_events (
	id bigserial PRIMARY KEY,
	issue_id bigint NOT NULL REFERENCES issues ON DELETE CASCADE,
	team_id bigint NOT NULL REFERENCES teams ON DELETE CASCADE,
	actor_id text NOT NULL REFERENCES users,
	kind text NOT NULL,
	from_value text NOT NULL DEFAULT '',
	to_value text NOT NULL DEFAULT '',
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX issue_events_issue ON issue_events (issue_id, id);
-- loom:track issue_events

-- A user's inbox, written by the Notify job. Scoped by user_id rather
-- than team: its shape is "user_id = ?", bound to the signed-in user.
-- job_id makes the job idempotent: jobs run at least once, and a second
-- run's insert conflicts and does nothing.
CREATE TABLE notifications (
	id bigserial PRIMARY KEY,
	job_id bigint NOT NULL UNIQUE,
	user_id text NOT NULL REFERENCES users ON DELETE CASCADE,
	issue_id bigint NOT NULL REFERENCES issues ON DELETE CASCADE,
	actor_id text NOT NULL REFERENCES users,
	kind text NOT NULL, -- assigned or commented
	created_at timestamptz NOT NULL DEFAULT now(),
	read_at timestamptz
);
CREATE INDEX notifications_user ON notifications (user_id, id);
-- loom:track notifications
