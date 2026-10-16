-- The todo lists, one per user. Tracked for the future sync engine:
-- every change gets a version, and deletes leave tombstones.
CREATE TABLE todos (
	id bigserial PRIMARY KEY,
	user_id text NOT NULL,
	title text NOT NULL,
	done boolean NOT NULL DEFAULT false,
	done_at timestamptz,
	inserted_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX todos_user_id ON todos (user_id, id);
-- loom:track todos
