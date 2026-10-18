package data

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ServerStats is what Postgres itself counts about a database: how each
// table is read (sequential against index scans) and, with the
// pg_stat_statements extension, every statement's totals across every
// node. QueryStats counts one node's statements as Loom ran them; this
// is the database's side, and where a missing index shows.
type ServerStats struct {
	// Since is when Postgres last reset the counts, if it says.
	Since  *time.Time  `json:"since"`
	Tables []TableStat `json:"tables"` // the most rows read by sequential scans first
	// Statements are pg_stat_statements' busiest, by total time, or
	// empty without the extension; StatementsNote then says why.
	Statements     []StatementStat `json:"statements"`
	StatementsNote string          `json:"statements_note,omitempty"`
}

// TableStat is how one table has been read and written.
type TableStat struct {
	Name       string `json:"name"`
	Rows       int64  `json:"rows"` // live rows, as Postgres estimates
	Bytes      int64  `json:"bytes"`
	SeqScans   int64  `json:"seq_scans"`
	SeqRows    int64  `json:"seq_rows"` // rows those scans read
	IndexScans int64  `json:"index_scans"`
	Inserts    int64  `json:"inserts"`
	Updates    int64  `json:"updates"`
	Deletes    int64  `json:"deletes"`
	// Warning says when the table is read mostly by sequential scans of
	// many rows: a query on it has no index it can use.
	Warning string `json:"warning,omitempty"`
}

// StatementStat is one statement's totals in pg_stat_statements.
type StatementStat struct {
	Query   string  `json:"query"`
	Calls   int64   `json:"calls"`
	TotalMS float64 `json:"total_ms"`
	MeanMS  float64 `json:"mean_ms"`
	Rows    int64   `json:"rows"`
}

// seqScanRows is how many rows a table's sequential scans must read on
// average, with more of them than index scans, before TableStat warns.
// Below it a scan is often the planner's best choice.
const seqScanRows = 1000

// Stats reads the database's own statistics for its tables in the
// current schema and, if installed, pg_stat_statements.
func (db *DB) Stats(ctx context.Context) (ServerStats, error) {
	out := ServerStats{Tables: []TableStat{}, Statements: []StatementStat{}}
	var since sql.NullTime
	if err := db.QueryRow(ctx, `SELECT stats_reset FROM pg_stat_database WHERE datname = current_database()`).Scan(&since); err != nil {
		return out, err
	}
	if since.Valid {
		t := since.Time.UTC()
		out.Since = &t
	}
	rows, err := db.Query(ctx, `
		SELECT relname, n_live_tup, pg_total_relation_size(relid), seq_scan, seq_tup_read, COALESCE(idx_scan, 0),
			n_tup_ins, n_tup_upd, n_tup_del
		FROM pg_stat_user_tables WHERE schemaname = current_schema()
		ORDER BY seq_tup_read DESC, relname`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var t TableStat
		if err := rows.Scan(&t.Name, &t.Rows, &t.Bytes, &t.SeqScans, &t.SeqRows, &t.IndexScans, &t.Inserts, &t.Updates, &t.Deletes); err != nil {
			return out, err
		}
		if t.SeqScans > t.IndexScans && t.SeqRows/max(t.SeqScans, 1) >= seqScanRows {
			t.Warning = fmt.Sprintf("%d sequential scans read about %d rows each, more than its %d index scans: a query on %s has no index it can use; find it in `loom queries` and run it with EXPLAIN (`loom db query`)",
				t.SeqScans, t.SeqRows/t.SeqScans, t.IndexScans, t.Name)
		}
		out.Tables = append(out.Tables, t)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	var installed bool
	if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')`).Scan(&installed); err != nil {
		return out, err
	}
	if !installed {
		out.StatementsNote = "pg_stat_statements isn't installed: add it to shared_preload_libraries and run CREATE EXTENSION pg_stat_statements for every statement's totals across nodes; `loom queries` counts this node's meanwhile"
		return out, nil
	}
	srows, err := db.Query(ctx, `
		SELECT query, calls, total_exec_time, mean_exec_time, rows FROM pg_stat_statements
		WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
		ORDER BY total_exec_time DESC LIMIT 25`)
	if err != nil {
		out.StatementsNote = "pg_stat_statements is installed but can't be read: " + err.Error()
		return out, nil
	}
	defer srows.Close()
	for srows.Next() {
		var s StatementStat
		if err := srows.Scan(&s.Query, &s.Calls, &s.TotalMS, &s.MeanMS, &s.Rows); err != nil {
			return out, err
		}
		s.Query = oneLine(s.Query)
		s.TotalMS, s.MeanMS = float64(int64(s.TotalMS*10))/10, float64(int64(s.MeanMS*10))/10
		out.Statements = append(out.Statements, s)
	}
	return out, srows.Err()
}
