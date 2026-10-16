-- 0001_create_schema: people, groups, expenses and how they're split,
-- settlements, recurring bills and notifications. Money is integer cents.

CREATE TABLE people (
	id text PRIMARY KEY,
	name text NOT NULL,
	email text NOT NULL UNIQUE,
	color text NOT NULL DEFAULT 'slate'
);

CREATE TABLE groups (
	id bigserial PRIMARY KEY,
	name text NOT NULL,
	emoji text NOT NULL DEFAULT '🧾',
	color text NOT NULL DEFAULT 'teal',
	created_by text NOT NULL REFERENCES people(id),
	created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE members (
	group_id bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	person_id text NOT NULL REFERENCES people(id),
	joined_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (group_id, person_id)
);

CREATE TABLE expenses (
	id bigserial PRIMARY KEY,
	group_id bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	description text NOT NULL,
	category text NOT NULL DEFAULT 'general',
	amount_cents bigint NOT NULL CHECK (amount_cents > 0),
	paid_by text NOT NULL REFERENCES people(id),
	spent_on date NOT NULL DEFAULT current_date,
	note text NOT NULL DEFAULT '',
	created_by text NOT NULL REFERENCES people(id),
	created_at timestamptz NOT NULL DEFAULT now(),
	recurring_id bigint
);
CREATE INDEX expenses_group_idx ON expenses (group_id, spent_on DESC, id DESC);

-- Who owes what of each expense. The shares of an expense add up to its amount.
CREATE TABLE shares (
	expense_id bigint NOT NULL REFERENCES expenses(id) ON DELETE CASCADE,
	person_id text NOT NULL REFERENCES people(id),
	amount_cents bigint NOT NULL CHECK (amount_cents >= 0),
	PRIMARY KEY (expense_id, person_id)
);

CREATE TABLE settlements (
	id bigserial PRIMARY KEY,
	group_id bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	from_person text NOT NULL REFERENCES people(id),
	to_person text NOT NULL REFERENCES people(id),
	amount_cents bigint NOT NULL CHECK (amount_cents > 0),
	created_by text NOT NULL REFERENCES people(id),
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX settlements_group_idx ON settlements (group_id, created_at DESC);

-- A bill that repeats every month on day_of_month, posted by the
-- PostRecurring cron job, split equally among the group's members.
CREATE TABLE recurring (
	id bigserial PRIMARY KEY,
	group_id bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	description text NOT NULL,
	category text NOT NULL DEFAULT 'general',
	amount_cents bigint NOT NULL CHECK (amount_cents > 0),
	paid_by text NOT NULL REFERENCES people(id),
	day_of_month int NOT NULL CHECK (day_of_month BETWEEN 1 AND 28),
	last_posted date
);

CREATE TABLE notifications (
	id bigserial PRIMARY KEY,
	person_id text NOT NULL REFERENCES people(id),
	group_id bigint REFERENCES groups(id) ON DELETE CASCADE,
	body text NOT NULL,
	read boolean NOT NULL DEFAULT false,
	job_id bigint, -- the job that wrote it, so a job that runs twice writes once
	created_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (job_id, person_id)
);
CREATE INDEX notifications_person_idx ON notifications (person_id, created_at DESC);

-- loom:track groups
-- loom:track members
-- loom:track expenses
-- loom:track shares
-- loom:track settlements
-- loom:track recurring
-- loom:track notifications
