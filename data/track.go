package data

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Change tracking. A tracked table sends a NOTIFY on channel loom_sync,
// naming itself, when a transaction that changed it commits (a NOTIFY in
// a transaction that rolls back is never delivered). Live pages watch
// tables this way (loom.Watch): every node LISTENs. Turn tracking on
// with a `-- loom:track <table>` line in a migration.
//
// Databases tracked by an older Loom also have a version and a loom_xid
// column on each tracked table, a loom_version_seq sequence and a
// loom_tombstones table. Nothing uses them any more; they are left in
// place, and may be dropped by a migration.

// ChangeChannel is the channel of the NOTIFY sent when a transaction
// changes a tracked table. The payload is the table's name. The channel
// keeps the name it had when it served the sync engine, so databases
// tracked then keep notifying it without a migration.
const ChangeChannel = "loom_sync"

// trackSchema is the shared part of tracking, created once and brought
// up to date by every migrate.
const trackSchema = `
CREATE OR REPLACE FUNCTION loom_track_notify() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	PERFORM pg_notify('` + ChangeChannel + `', TG_TABLE_NAME);
	RETURN NULL;
END $$;
`

// track turns on change tracking for table, in a migration's
// transaction. It is idempotent, and brings a table tracked by an older
// Loom up to date: its version and delete triggers, which wrote columns
// and tombstones nothing reads, are dropped.
func track(ctx context.Context, tx *sql.Tx, table string) error {
	if _, err := tx.ExecContext(ctx, trackSchema); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, quoteIdent(table)).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return &Error{Code: DB616, Msg: fmt.Sprintf("`-- loom:track %s`: there is no table %s", table, table),
			Fix: "create the table in this migration or an earlier one (annotations take effect after the migration's SQL), or fix the name"}
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`
DROP TRIGGER IF EXISTS loom_track_version ON %[1]s;
DROP TRIGGER IF EXISTS loom_track_delete ON %[1]s;
CREATE OR REPLACE TRIGGER loom_track_notify AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON %[1]s FOR EACH STATEMENT EXECUTE FUNCTION loom_track_notify();
`, quoteIdent(table)))
	return err
}

// retrack brings tracked tables' triggers up to date, for databases
// migrated by an older Loom. Migrate runs it after applying migrations.
// It touches only tables whose triggers differ from what track creates,
// since replacing a trigger locks its table against writes.
func retrack(ctx context.Context, conn *sql.Conn) error {
	var tracking bool
	if err := conn.QueryRowContext(ctx, `SELECT to_regproc('loom_track_notify') IS NOT NULL`).Scan(&tracking); err != nil || !tracking {
		return MapError(err)
	}
	// Replacing the function locks no table.
	if _, err := conn.ExecContext(ctx, trackSchema); err != nil {
		return MapError(err)
	}
	rows, err := conn.QueryContext(ctx, staleTables)
	if err != nil {
		return MapError(err)
	}
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(tables) == 0 {
		return err
	}
	tx, err := beginLocked(ctx, conn)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range tables {
		if err := track(ctx, tx, t); err != nil {
			return MapError(err)
		}
	}
	return MapError(tx.Commit())
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

const trackedTables = `
SELECT DISTINCT c.relname FROM pg_trigger t
JOIN pg_class c ON c.oid = t.tgrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE t.tgname IN ('loom_track_notify', 'loom_track_version') AND n.nspname = current_schema()
ORDER BY c.relname`

// staleTables are the tracked tables whose triggers aren't the one
// track creates: an older Loom's version and delete triggers, or a
// notify trigger that isn't an AFTER INSERT, UPDATE, DELETE and
// TRUNCATE statement trigger (tgtype 60) calling loom_track_notify.
const staleTables = `
SELECT c.relname FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND EXISTS (
	SELECT 1 FROM pg_trigger t WHERE t.tgrelid = c.oid AND t.tgname IN ('loom_track_notify', 'loom_track_version')
) AND (
	EXISTS (SELECT 1 FROM pg_trigger t WHERE t.tgrelid = c.oid AND t.tgname IN ('loom_track_version', 'loom_track_delete'))
	OR NOT EXISTS (
		SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid = t.tgfoid
		WHERE t.tgrelid = c.oid AND t.tgname = 'loom_track_notify' AND t.tgtype = 60 AND p.proname = 'loom_track_notify'
	)
)
ORDER BY c.relname`

// Tracked returns the names of the tracked tables.
func Tracked(ctx context.Context, q Querier) ([]string, error) {
	return Select[string](ctx, q, trackedTables)
}

// jsonValue turns a scanned value into one that encodes well as JSON:
// text bytes become strings.
func jsonValue(v any) any {
	if b, ok := v.([]byte); ok {
		if utf8.Valid(b) {
			return string(b)
		}
		return fmt.Sprintf("\\x%x", b)
	}
	return v
}

// SchemaInfo describes a database's tables: what `loom db schema` prints.
type SchemaInfo struct {
	Tables []Table `json:"tables"`
}

// Table describes a table.
type Table struct {
	Name       string   `json:"name"`
	Columns    []Column `json:"columns"`
	PrimaryKey []string `json:"primary_key"`
	Tracked    bool     `json:"tracked"`
}

// Column describes a column.
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // as Postgres writes it, e.g. "timestamp with time zone"
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"` // SQL expression
}

// Schema describes the tables of the current schema (usually public),
// leaving out Loom's own loom_* tables and tracked tables' loom_xid.
func Schema(ctx context.Context, q Querier) (*SchemaInfo, error) {
	type col struct {
		Table, Name, Type string
		Nullable          bool
		Default           string
	}
	cols, err := Select[col](ctx, q, `
SELECT c.relname AS "table", a.attname AS name, format_type(a.atttypid, a.atttypmod) AS type,
	NOT a.attnotnull AS nullable, coalesce(pg_get_expr(d.adbin, d.adrelid), '') AS "default"
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p') AND c.relname NOT LIKE 'loom\_%' AND a.attname <> 'loom_xid'
ORDER BY c.relname, a.attnum`)
	if err != nil {
		return nil, err
	}
	type pk struct {
		Table, Name string
	}
	pks, err := Select[pk](ctx, q, `
SELECT c.relname AS "table", a.attname AS name
FROM pg_index i
JOIN pg_class c ON c.oid = i.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
WHERE i.indisprimary AND n.nspname = current_schema()
ORDER BY c.relname, array_position(i.indkey::int2[], a.attnum)`)
	if err != nil {
		return nil, err
	}
	tracked, err := Tracked(ctx, q)
	if err != nil {
		return nil, err
	}
	s := &SchemaInfo{Tables: []Table{}}
	byName := map[string]int{}
	for _, c := range cols {
		i, ok := byName[c.Table]
		if !ok {
			i = len(s.Tables)
			byName[c.Table] = i
			s.Tables = append(s.Tables, Table{Name: c.Table, Columns: []Column{}, PrimaryKey: []string{}})
		}
		s.Tables[i].Columns = append(s.Tables[i].Columns, Column{Name: c.Name, Type: c.Type, Nullable: c.Nullable, Default: c.Default})
	}
	for _, k := range pks {
		if i, ok := byName[k.Table]; ok {
			s.Tables[i].PrimaryKey = append(s.Tables[i].PrimaryKey, k.Name)
		}
	}
	for _, t := range tracked {
		if i, ok := byName[t]; ok {
			s.Tables[i].Tracked = true
		}
	}
	return s, nil
}

// QueryResult is the result of QueryReadOnly: what `loom db query` prints.
type QueryResult struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated"` // there were more than limit rows
}

// QueryReadOnly runs a query in a read-only transaction that is rolled
// back, with a 10 second statement timeout, and returns at most limit
// rows. It is for dev tools (`loom db query`): writes fail.
func QueryReadOnly(ctx context.Context, db *DB, query string, limit int) (*QueryResult, error) {
	if db.sql == nil {
		return nil, noDatabase()
	}
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, MapError(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout = '10s'`); err != nil {
		return nil, MapError(err)
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &QueryResult{Columns: cols, Rows: [][]any{}}
	for rows.Next() {
		if len(res.Rows) == limit {
			res.Truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i := range vals {
			vals[i] = jsonValue(vals[i])
		}
		res.Rows = append(res.Rows, vals)
	}
	return res, MapError(rows.Err())
}
