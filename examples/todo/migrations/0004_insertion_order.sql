-- Todos are listed in the order they were added. clock_timestamp()
-- differs between statements of one transaction, as now() doesn't.
ALTER TABLE todos ALTER COLUMN inserted_at SET DEFAULT clock_timestamp();
