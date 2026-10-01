package costs

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// The ledger is the only lasting record once Claude Code deletes old
// transcripts. A resumed session re-copies earlier records with smaller or
// zeroed counts, and an upgrade must keep every row: either mistake silently
// loses spend history that cannot be recovered.
func TestLedgerKeepsLargestUsageAndUpgradesWithoutLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// The version 1 schema, exactly as the claude-costs script created it.
	for _, statement := range []string{
		`CREATE TABLE responses (
		  request_id TEXT PRIMARY KEY, ts TEXT NOT NULL, model TEXT NOT NULL,
		  project TEXT NOT NULL, session_id TEXT NOT NULL, account TEXT NOT NULL,
		  account_source TEXT NOT NULL, input INTEGER NOT NULL, output INTEGER NOT NULL,
		  cache_write_5m INTEGER NOT NULL, cache_write_1h INTEGER NOT NULL, cache_read INTEGER NOT NULL)`,
		`CREATE INDEX responses_ts ON responses (ts)`,
		`CREATE INDEX responses_project_model ON responses (project, model)`,
		`CREATE INDEX responses_account ON responses (account)`,
		`CREATE TABLE files (path TEXT PRIMARY KEY, offset INTEGER NOT NULL, size INTEGER NOT NULL,
		  mtime_ns INTEGER NOT NULL, last_seen TEXT NOT NULL)`,
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta VALUES ('schema_version', '1')`,
		`INSERT INTO responses VALUES ('req-1', '2026-09-01T10:00:00.000Z', 'claude-opus-5', '/w/a', 's1',
		  'a@example.test', 'sweep', 100, 200, 300, 0, 1000)`,
		`INSERT INTO responses VALUES ('req-2', '2026-09-02T10:00:00.000Z', 'claude-opus-5', '/w/b', 's2',
		  'a@example.test', 'sweep', 1, 2, 3, 4, 5)`,
	} {
		if _, err := old.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	ledger, err := OpenLedger(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()
	var rows, tools int
	var version string
	if err := ledger.db.QueryRow("SELECT COUNT(*), SUM(tool = 'claude') FROM responses").
		Scan(&rows, &tools); err != nil {
		t.Fatal(err)
	}
	if version, err = ledger.Meta("schema_version"); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || tools != 2 || version != "3" {
		t.Fatalf(
			"upgrade kept %d rows (%d tagged claude) at version %q, want 2, 2, \"3\"",
			rows,
			tools,
			version,
		)
	}
	var stateCol, headCol, tierTables int
	if err := ledger.db.QueryRow(`SELECT
	    (SELECT COUNT(*) FROM pragma_table_info('files') WHERE name = 'state'),
	    (SELECT COUNT(*) FROM pragma_table_info('files') WHERE name = 'head'),
	    (SELECT COUNT(*) FROM sqlite_master WHERE name IN ('tier_changes', 'tier_pending'))`).
		Scan(&stateCol, &headCol, &tierTables); err != nil {
		t.Fatal(err)
	}
	if stateCol != 1 || headCol != 1 || tierTables != 2 {
		t.Fatalf("upgrade left files.state=%d files.head=%d tier tables=%d, want 1, 1, 2",
			stateCol, headCol, tierTables)
	}

	record := func(output, cacheRead int64) {
		t.Helper()
		err := ledger.Transaction(context.Background(), nil, func(tx *Tx) error {
			return tx.Upsert(Usage{
				Tool:         "claude",
				RequestID:    "req-1",
				Model:        "claude-opus-5",
				Project:      "/w/a",
				Session:      "s1",
				Account:      "a@example.test",
				Time:         time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC),
				Input:        100,
				Output:       output,
				CacheWrite5m: 300,
				CacheRead:    cacheRead,
			}, "sweep")
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	stored := func() (output, cacheRead int64) {
		t.Helper()
		if err := ledger.db.QueryRow(
			"SELECT output, cache_read FROM responses WHERE request_id = 'req-1'",
		).Scan(&output, &cacheRead); err != nil {
			t.Fatal(err)
		}
		return output, cacheRead
	}
	record(50, 0) // a resumed session's partial copy
	if output, cacheRead := stored(); output != 200 || cacheRead != 1000 {
		t.Fatalf(
			"a smaller copy lowered the usage to output=%d cache_read=%d, want 200 and 1000",
			output,
			cacheRead,
		)
	}
	record(250, 1500) // later, complete usage
	if output, cacheRead := stored(); output != 250 || cacheRead != 1500 {
		t.Fatalf(
			"a larger copy stored output=%d cache_read=%d, want 250 and 1500",
			output,
			cacheRead,
		)
	}
}

// A version 2 ledger (the Workbench ledger before Codex) holds rows and file
// offsets; its upgrade must keep both.
func TestLedgerUpgradesVersionTwoWithoutLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE responses (
		  request_id TEXT PRIMARY KEY, ts TEXT NOT NULL, model TEXT NOT NULL,
		  project TEXT NOT NULL, session_id TEXT NOT NULL, account TEXT NOT NULL,
		  account_source TEXT NOT NULL, input INTEGER NOT NULL, output INTEGER NOT NULL,
		  cache_write_5m INTEGER NOT NULL, cache_write_1h INTEGER NOT NULL, cache_read INTEGER NOT NULL,
		  tool TEXT NOT NULL DEFAULT 'claude')`,
		`CREATE TABLE files (path TEXT PRIMARY KEY, offset INTEGER NOT NULL, size INTEGER NOT NULL,
		  mtime_ns INTEGER NOT NULL, last_seen TEXT NOT NULL)`,
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta VALUES ('schema_version', '2')`,
		`INSERT INTO responses VALUES ('req-1', '2026-09-01T10:00:00.000Z', 'claude-opus-5', '/w/a', 's1',
		  'a@example.test', 'sweep', 100, 200, 300, 0, 1000, 'claude')`,
		`INSERT INTO files VALUES ('/w/t.jsonl', 4096, 4096, 1, '2026-09-01T10:00:00+00:00')`,
	} {
		if _, err := old.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	ledger, err := OpenLedger(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()
	var rows int
	if err := ledger.db.QueryRow("SELECT COUNT(*) FROM responses").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	file, found, err := ledger.File("/w/t.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if version, _ := ledger.Meta("schema_version"); rows != 1 || !found || file.Offset != 4096 ||
		file.Head != "" || version != "3" {
		t.Fatalf(
			"upgrade kept %d rows, file %v (found %v), version %q; want 1 row, offset 4096, version 3",
			rows,
			file,
			found,
			version,
		)
	}
}
