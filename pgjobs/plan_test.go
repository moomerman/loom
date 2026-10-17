package pgjobs

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/moomerman/loom/data"
)

// The runner sends the fetch and the unique check every second on every
// node, as prepared statements, so Postgres soon plans them generically,
// without the parameters' values. Each must still use its partial index
// then, not scan loom_jobs.
func TestQueriesUseTheirIndexes(t *testing.T) {
	url := os.Getenv("LOOM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LOOM_TEST_DATABASE_URL to a database URL to run the Postgres tests")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := data.Migrate(ctx, data.FromSQL(db), os.DirFS("../migrations")); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// The test table is tiny, where a scan is cheapest, so rule scans out
	// and see which index each plan can use.
	for _, q := range []string{`SET plan_cache_mode = force_generic_plan`, `SET enable_seqscan = off`, `SET enable_bitmapscan = off`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	defer conn.ExecContext(ctx, `RESET ALL`)
	tests := []struct {
		name, sql, args, index string
	}{
		{"fetch", fetchSQL, `('default', 10, now(), 'node', now())`, "loom_jobs_fetch"},
		{"unique", uniqueSQL, `('key', now())`, "loom_jobs_unique"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := conn.ExecContext(ctx, `PREPARE plan_`+tc.name+` AS `+tc.sql); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(ctx, `DEALLOCATE plan_`+tc.name)
			rows, err := conn.QueryContext(ctx, `EXPLAIN EXECUTE plan_`+tc.name+tc.args)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, line)
			}
			text := strings.Join(plan, "\n")
			if !strings.Contains(text, "Index Scan using "+tc.index+" on loom_jobs") || strings.Contains(text, "Seq Scan") {
				t.Fatalf("the generic plan doesn't scan %s:\n%s", tc.index, text)
			}
		})
	}
}
