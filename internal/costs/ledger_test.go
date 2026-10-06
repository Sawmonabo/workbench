package costs

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The ledger is the only lasting record once Claude Code deletes old
// transcripts. A resumed session re-copies earlier records with smaller or
// zeroed counts, and an upgrade must keep every row: either mistake silently
// loses spend history that cannot be recovered.
func TestLedgerKeepsLargestUsageAndUpgradesWithoutLoss(t *testing.T) {
	// The version 1 schema, exactly as the claude-costs script created it.
	path := oldLedger(
		t,
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
	)

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
	if rows != 2 || tools != 2 || version != "4" {
		t.Fatalf(
			"upgrade kept %d rows (%d tagged claude) at version %q, want 2, 2, \"3\"",
			rows,
			tools,
			version,
		)
	}
	// One run's record of committed rows lets ingest skip a later copy that
	// changes nothing; a skip that also swallowed larger usage would lose spend.
	run := NewRun()
	record := func(output, cacheRead int64, minute int) {
		t.Helper()
		err := ledger.Transaction(context.Background(), run, func(tx *Tx) error {
			return tx.Upsert(Usage{
				Tool:         "claude",
				RequestID:    "req-1",
				Model:        "claude-opus-5",
				Project:      "/w/a",
				Session:      "s1",
				Account:      "a@example.test",
				Time:         time.Date(2026, 9, 1, 10, minute, 0, 0, time.UTC),
				Input:        100,
				Output:       output,
				CacheWrite5m: 300,
				CacheRead:    cacheRead,
			})
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
	record(50, 0, 0) // a resumed session's partial copy
	if output, cacheRead := stored(); output != 200 || cacheRead != 1000 {
		t.Fatalf(
			"a smaller copy lowered the usage to output=%d cache_read=%d, want 200 and 1000",
			output,
			cacheRead,
		)
	}
	record(250, 1500, 5) // a later copy with complete usage
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
	path := oldLedger(
		t,
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
	)
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
	version, err := ledger.Meta("schema_version")
	if err != nil {
		t.Fatal(err)
	}
	if rows != 1 || !found || file.Offset != 4096 || file.Head != "" || version != "4" {
		t.Fatalf(
			"upgrade kept %d rows, file %v (found %v), version %q; want 1 row, offset 4096, version 4",
			rows,
			file,
			found,
			version,
		)
	}
}

// The report splits spend by the subscription each row names, and a row's
// email and subscription are each decided by the strongest evidence for them.
// A weaker copy or a later resolution that overwrote a stronger attribution,
// a run cancelled before the resolution that left a row on the wrong
// session's evidence, or a Claude Code row given the email of a session bound
// to another organization than the one its transcript names (a /login in a
// running session), would move spend to the wrong account or subscription
// with no error, and the transcript that proved it may be gone by the time
// anyone notices.
func TestStrongerAttributionSurvivesWeakerEvidence(t *testing.T) {
	ctx := context.Background()
	ledger, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.sqlite"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()
	at := func(hour int) time.Time { return time.Date(2026, 9, 1, hour, 0, 0, 0, time.UTC) }
	personal := SignIn{Account: "a@example.test", Subscription: "claude:personal", Label: "Max"}
	team := SignIn{
		Account:      "a@example.test",
		Subscription: "claude:team",
		Label:        "Team (Example Org)",
	}
	other := SignIn{Account: "b@example.test", Subscription: "claude:other", Label: "Max"}
	stamped := func(id string, hour int, s SignIn) Usage {
		return Usage{
			Tool:              "claude",
			RequestID:         id,
			Model:             "claude-opus-5",
			Root:              "s1",
			Time:              at(hour),
			Input:             1,
			Account:           s.Account,
			Subscription:      s.Subscription,
			SubscriptionLabel: s.Label,
		}
	}
	run := NewRun()
	store := func(u Usage) {
		t.Helper()
		if err := ledger.Transaction(
			ctx,
			run,
			func(tx *Tx) error { return tx.Upsert(u) },
		); err != nil {
			t.Fatal(err)
		}
	}
	// A session started under the personal plan and was resumed under the team
	// plan; rows were read before either binding existed.
	store(stamped("resumed", 5, other))
	store(stamped("started", 2, other))
	// A row whose transcript named its plan.
	named := stamped("named", 3, other)
	named.Account, named.AccountEvidence = "c@example.test", EvidenceTranscript
	named.Subscription, named.SubscriptionEvidence = "codex:pro", EvidenceTranscript
	store(named)
	for _, bind := range []struct {
		hour int
		in   SignIn
	}{{1, personal}, {4, team}} {
		if err := ledger.BindSession(ctx, "claude", "s1", at(bind.hour), bind.in); err != nil {
			t.Fatal(err)
		}
	}
	if err := ledger.Transaction(ctx, run, func(tx *Tx) error {
		return tx.ObserveSignIn("claude", at(0), other)
	}); err != nil {
		t.Fatal(err)
	}
	// Later copies of the same rows, stamped with a sign-in that is not theirs.
	store(stamped("resumed", 6, other))
	store(stamped("started", 7, other))
	store(stamped("named", 8, other))
	if err := ledger.ResolveAccounts(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	store(stamped("resumed", 9, other))
	store(stamped("started", 9, other))
	store(
		stamped("started", 0, other),
	) // a copy from before the binding moves the row's time earlier
	if err := ledger.ResolveAccounts(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	// A Codex row whose transcript named its plan but not its account takes
	// the email of an observation, then of a later binding, and keeps its plan.
	codexPro := SignIn{Account: "a@example.test", Subscription: "codex:pro"}
	planOnly := Usage{
		Tool: "codex", RequestID: "plan-only", Model: "gpt-5", Root: "s2",
		Time: at(3), Input: 1, Subscription: "codex:pro", SubscriptionEvidence: EvidenceTranscript,
	}
	store(planOnly)
	if err := ledger.Transaction(ctx, run, func(tx *Tx) error {
		if err := tx.ObserveSignIn("codex", at(0), other); err != nil {
			return err
		}
		return tx.ObserveSignIn("codex", at(10), other)
	}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.ResolveAccounts(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	if err := ledger.BindSession(ctx, "codex", "s2", at(1), codexPro); err != nil {
		t.Fatal(err)
	}
	// Claude Code rows of a session bound to the team, whose transcripts name
	// other organizations: one a sign-in on disk names, one no sign-in names.
	// Neither takes the team's email.
	if err := ledger.BindSession(ctx, "claude", "s3", at(4), team); err != nil {
		t.Fatal(err)
	}
	for id, org := range map[string]string{"moved-org": "claude:disk", "unseen-org": "claude:unseen"} {
		u := stamped(id, 5, other)
		u.Root, u.Account, u.Subscription = "s3", "unknown", org
		u.SubscriptionEvidence = EvidenceTranscript
		store(u)
	}
	onDisk := map[string][]SignIn{
		"claude": {{Account: "d@example.test", Subscription: "claude:disk"}},
	}
	if err := ledger.ResolveAccounts(ctx, run, onDisk); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string][4]string{
		"started": {
			personal.Account, EvidenceSession, personal.Subscription, EvidenceSession,
		},
		"resumed": {team.Account, EvidenceSession, team.Subscription, EvidenceSession},
		"named": {
			"c@example.test", EvidenceTranscript, "codex:pro", EvidenceTranscript,
		},
		"codex:plan-only": { // the ledger keys every tool but Claude Code's by <tool>:<id>
			codexPro.Account, EvidenceSession, "codex:pro", EvidenceTranscript,
		},
		"moved-org":  {"d@example.test", EvidenceTranscript, "claude:disk", EvidenceTranscript},
		"unseen-org": {"unknown", EvidenceUnknown, "claude:unseen", EvidenceTranscript},
	} {
		var got [4]string
		if err := ledger.db.QueryRow(
			`SELECT account, account_source, subscription, subscription_source
			   FROM responses WHERE request_id = ?`, id,
		).Scan(&got[0], &got[1], &got[2], &got[3]); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s is attributed to %v, want %v", id, got, want)
		}
	}
	// A copy with stronger evidence from another session keeps the stored
	// root, and that is the session a resolution cut short must still name.
	store(stamped("moved", 5, other))
	if err := ledger.ResolveAccounts(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	copied := stamped("moved", 6, other)
	copied.Root, copied.SubscriptionEvidence = "s9", EvidenceTranscript
	store(copied)
	if note, err := ledger.Meta(accountsUnresolved); err != nil ||
		!strings.Contains(note, rootKey("claude", "s1")) {
		t.Errorf("rows of s1 are not marked for attribution: %q, %v", note, err)
	}
}

// oldLedger writes a ledger of an earlier schema with statements and returns
// its path.
func oldLedger(t *testing.T, statements ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range statements {
		if _, err := old.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
