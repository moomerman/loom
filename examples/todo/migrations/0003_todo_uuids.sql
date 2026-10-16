-- Todos get UUID keys, so the browser can add one offline and keep its
-- key (a mutator's :new_id). Existing todos keep their order: their
-- ids become UUIDs that sort before any new one.
ALTER TABLE todos ALTER COLUMN id DROP DEFAULT;
ALTER TABLE todos ALTER COLUMN id TYPE uuid USING lpad(to_hex(id), 32, '0')::uuid;
DROP SEQUENCE todos_id_seq;
-- An empty title is refused, in the browser too (the input is required).
ALTER TABLE todos ADD CONSTRAINT todos_title_present CHECK (btrim(title) <> '');
