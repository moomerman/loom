-- Tables for the table watch tests: items is tracked, notes is not.
CREATE TABLE items (
	id bigserial PRIMARY KEY,
	title text NOT NULL
);
-- loom:track items
CREATE TABLE notes (
	id bigserial PRIMARY KEY,
	body text NOT NULL
);
