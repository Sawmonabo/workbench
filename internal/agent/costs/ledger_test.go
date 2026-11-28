package costs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
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

	ledger, err := openLedger(t.Context(), path, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()
	var rows, tools int
	var version string
	if err := ledger.db.QueryRow(`SELECT COUNT(*),
	    SUM(tool = (SELECT id FROM names WHERE kind = ? AND name = 'claude')) FROM responses`,
		nameTool).Scan(&rows, &tools); err != nil {
		t.Fatal(err)
	}
	if version, err = ledger.Meta(t.Context(), "schema_version"); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || tools != 2 || version != schemaVersion {
		t.Fatalf(
			"upgrade kept %d rows (%d tagged claude) at version %q, want 2, 2, %q",
			rows,
			tools,
			version,
			schemaVersion,
		)
	}
	// One run's record of committed rows lets ingest skip a later copy that
	// changes nothing; a skip that also swallowed larger usage would lose spend.
	run := newRun()
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
			"SELECT output, cache_read FROM responses WHERE request_id = ?", packID("req-1"),
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
	ledger, err := openLedger(t.Context(), path, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()
	var rows int
	if err := ledger.db.QueryRow("SELECT COUNT(*) FROM responses").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	file, found, err := ledger.File(t.Context(), "/w/t.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	version, err := ledger.Meta(t.Context(), "schema_version")
	if err != nil {
		t.Fatal(err)
	}
	if rows != 1 || !found || file.Offset != 4096 || file.Head != "" || version != schemaVersion {
		t.Fatalf(
			"upgrade kept %d rows, file %v (found %v), version %q; want 1 row, offset 4096, version "+
				schemaVersion,
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
	ledger, err := openLedger(ctx, filepath.Join(t.TempDir(), "ledger.sqlite"), true, true, nil)
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
	run := newRun()
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
		if got := attributionOf(t, ledger, id); got != want {
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
	if note, err := ledger.Meta(ctx, accountsUnresolved); err != nil ||
		!strings.Contains(note, rootKey("claude", "s1")) {
		t.Errorf("rows of s1 are not marked for attribution: %q, %v", note, err)
	}
}

// Rows no record of their own names take the account their login stretch's
// marks infer. An inferred account that outranked real evidence, that a
// contradicting mark could not take back, or that named a stretch whose marks
// disagree would move spend to the wrong account with no error, after the
// transcripts that could show it have been deleted.
func TestInferredAccountYieldsToEvidence(t *testing.T) {
	ctx := context.Background()
	ledger, err := openLedger(ctx, filepath.Join(t.TempDir(), "ledger.sqlite"), true, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ledger.Close() }()
	at := func(hour int) time.Time { return time.Date(2026, 9, 1, hour, 0, 0, 0, time.UTC) }
	run := newRun()
	write := func(fn func(*Tx) error) {
		t.Helper()
		if err := ledger.Transaction(ctx, run, fn); err != nil {
			t.Fatal(err)
		}
	}
	addMarks := func(marks ...AccountMark) {
		t.Helper()
		write(func(tx *Tx) error {
			for _, m := range marks {
				if err := tx.AddMark("claude", m); err != nil {
					return err
				}
			}
			return nil
		})
	}
	row := func(id string, hour int, subscription string) Usage {
		u := Usage{
			Tool: "claude", RequestID: id, Model: "claude-opus-5", Root: "s-" + id,
			Time: at(hour), Input: 1, Account: "unknown",
		}
		if subscription != "" {
			u.Subscription, u.SubscriptionEvidence = subscription, EvidenceTranscript
		}
		return u
	}
	write(func(tx *Tx) error {
		for _, u := range []Usage{
			row("inferred", 2, ""),
			row("named", 3, "claude:unseen"),
			row("contradicted", 22, ""),
		} {
			if err := tx.Upsert(u); err != nil {
				return err
			}
		}
		return nil
	})
	addMarks(
		AccountMark{Kind: MarkPair, Account: "id-d", Org: "disk"},
		AccountMark{Kind: MarkLogin, Time: at(0)},
		AccountMark{Kind: MarkAccount, Time: at(1), Account: "id-d"},
		AccountMark{Kind: MarkLogin, Time: at(20)},
		AccountMark{Kind: MarkAccount, Time: at(21), Account: "id-d"},
		AccountMark{Kind: MarkOrg, Time: at(23), Org: "disk"},
		AccountMark{Kind: MarkOrg, Time: at(24), Org: "unseen"},
	)
	onDisk := map[string][]SignIn{
		"claude": {{Account: "d@example.test", Subscription: "claude:disk"}},
	}
	check := func(want map[string][4]string) {
		t.Helper()
		if err := ledger.ResolveAccounts(ctx, run, onDisk); err != nil {
			t.Fatal(err)
		}
		for id, want := range want {
			if got := attributionOf(t, ledger, id); got != want {
				t.Errorf("%s is attributed to %v, want %v", id, got, want)
			}
		}
	}
	unknown := [4]string{"unknown", EvidenceUnknown, "unknown", EvidenceUnknown}
	check(map[string][4]string{
		"inferred": {"d@example.test", EvidenceInferred, "claude:disk", EvidenceInferred},
		"named":    {"unknown", EvidenceUnknown, "claude:unseen", EvidenceTranscript},
		// its stretch's organization marks name two organizations
		"contradicted": unknown,
	})
	// A mark found later pairs id-d with another organization too.
	addMarks(AccountMark{Kind: MarkOrg, Time: at(4), Org: "unseen"})
	check(map[string][4]string{"inferred": unknown})
}

// The ledger is the only lasting record of spend once the tools delete their
// transcripts, and from schema version 6 it also keeps the downloaded prices
// a report is priced from, which a version 5 ledger had in list files beside
// it. An upgrade that lost a row, or dropped those prices so that a total
// changed, would misstate spend silently and for good; one that failed on a
// list file that is missing or damaged would leave the ledger unread.
func TestUpgradeToSixKeepsRowsAndPrices(t *testing.T) {
	// Two tools record usage, so a ledger has two list files beside it: one
	// case puts a valid list beside it for each tool, with the other tool's
	// list missing in one and unreadable in the other.
	for _, c := range []struct {
		name, valid, other, otherList string
	}{
		{"claude's list valid, codex's missing", "claude", "codex", ""},
		{"codex's list valid, claude's unreadable", "codex", "claude", `{"rates": {`},
	} {
		t.Run(c.name, func(t *testing.T) { upgradeToSix(t, c.valid, c.other, c.otherList) })
	}
}

// upgradeToSix opens a version 5 ledger with rows, a valid list file beside it
// for the tool valid and otherList ("" for none) as the list file of the tool
// other, and checks what the upgrade kept.
func upgradeToSix(t *testing.T, valid, other, otherList string) {
	privateHome(t) // a report reads the home for the tools' own cost records
	path := oldLedger(
		t,
		versionFive,
		`INSERT INTO meta VALUES ('schema_version', '5')`,
		`INSERT INTO responses (request_id, ts, model, project, session_id, account,
		   account_source, input, output, cache_write_5m, cache_write_1h, cache_read, tool)
		 VALUES
		   ('req-1', '2026-11-01T10:00:00.000Z', 'claude-opus-5-5', '/w/a', 's1', 'a@example.test',
		    'transcript', 1000, 2000, 3000, 4000, 50000, 'claude'),
		   ('req-2', '2026-11-02T10:00:00.000Z', 'claude-opus-5-5', '/w/b', 's2', 'a@example.test',
		    'transcript', 7, 11, 13, 17, 19, 'claude'),
		   ('codex:c-1', '2026-11-03T10:00:00.000Z', 'gpt-5.5', '/w/c', 'c1', 'b@example.test',
		    'transcript', 3000, 5000, 0, 0, 70000, 'codex')`,
		`INSERT INTO files VALUES ('/w/t.jsonl', 4096, 4096, 1, '2026-11-01T10:00:00+00:00', '', 'h')`,
		`INSERT INTO subscriptions VALUES ('claude:personal', 'Max')`,
		`INSERT INTO session_accounts VALUES ('claude', 's1', '2026-11-01T09:00:00Z',
		   'a@example.test', 'claude:personal')`,
	)
	before := ledgerRows(t, path)
	// The list prices differ from the built-in ones, so a total priced from
	// anything but the file tells.
	lists := map[string]officialFile{
		"claude": {
			FetchedAt:    1.7e9,
			CheckedAt:    1.8e9,
			ETag:         `"e-claude"`,
			LastModified: "Mon, 02 Nov 2026",
			URL:          "https://example.test/claude",
			Rates: map[string]map[string]float64{
				"claude-opus-5-5": {
					"input": 7, "output": 31, "cache_write_5m": 9,
					"cache_write_1h": 13, "cache_read": 0.7,
				},
				"claude-opus": {
					"input": 6, "output": 29, "cache_write_5m": 8,
					"cache_write_1h": 12, "cache_read": 0.6,
				},
			},
		},
		"codex": {
			FetchedAt: 1.6e9, CheckedAt: 1.9e9, ETag: `"e-codex"`, LastModified: "Tue, 03 Nov 2026",
			URL: "https://example.test/codex", Rates: map[string]map[string]float64{
				"gpt-5.5": {
					"input": 6, "output": 33, "cache_write_5m": 6,
					"cache_write_1h": 6, "cache_read": 0.6,
				},
			},
		},
	}
	validTool, _ := Lookup(valid)
	otherTool, _ := Lookup(other)
	data, err := json.Marshal(lists[valid])
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, officialPath(path, validTool), string(data), 0o600)
	if otherList != "" {
		writeFile(t, officialPath(path, otherTool), otherList, 0o600)
	}

	ctx := t.Context()
	ledger, err := openLedger(ctx, path, false, true, nil)
	if err != nil {
		t.Fatalf("the upgrade failed: %v", err)
	}
	defer func() { _ = ledger.Close() }()
	if version, err := ledger.Meta(ctx, "schema_version"); err != nil || version != schemaVersion {
		t.Fatalf("the ledger is at version %q (%v), want %s", version, err, schemaVersion)
	}
	after := ledgerRows(t, path)
	for table, rows := range before {
		// meta's schema_version is the one row an upgrade changes.
		if table != "meta" && !slices.Equal(after[table], rows) {
			t.Errorf("%s holds %v after the upgrade, want %v", table, after[table], rows)
		}
	}
	assertListKept(t, ledger, valid, lists[valid])
	for _, table := range []string{"prices", "price_lists"} {
		for _, row := range after[table] {
			if strings.HasPrefix(row, other+" ") {
				t.Errorf(
					"%s holds %q for %s, whose list is missing or unreadable",
					table,
					row,
					other,
				)
			}
		}
	}
	// The totals are those the file's prices give, and the built-in ones for
	// the tool without a list, as before the upgrade.
	assertTotal(t, ledger, validTool, lists[valid].rates(), after["responses"])
	assertTotal(t, ledger, otherTool, otherTool.Source.Builtin(), after["responses"])
}

// privateHome points the home and every folder Workbench or the tools read
// from it at a new temporary home.
func privateHome(t *testing.T) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	for _, name := range []string{
		"WORKBENCH_CONFIG_DIR", "WORKBENCH_DATA_DIR", "WORKBENCH_STATE_DIR",
		"WORKBENCH_CACHE_DIR", "WORKBENCH_BIN_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
		"XDG_STATE_HOME", "XDG_CACHE_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME",
	} {
		t.Setenv(name, "")
	}
}

// assertListKept checks that the ledger keeps want as tool's official list.
func assertListKept(t *testing.T, ledger *Ledger, tool string, want officialFile) {
	t.Helper()
	kept, err := ledger.officialRates(context.Background(), tool)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept.Rates) == 0 || !maps.EqualFunc(kept.Rates, want.Rates, maps.Equal) ||
		kept.URL != want.URL || kept.FetchedAt != want.FetchedAt ||
		kept.CheckedAt != want.CheckedAt || kept.ETag != want.ETag ||
		kept.LastModified != want.LastModified {
		t.Errorf("the ledger keeps %+v for %s, want the file's %+v", kept, tool, want)
	}
}

// assertTotal checks that a report's total for tool is what rates give the
// tool's rows among responses (as ledgerRows lists them).
func assertTotal(t *testing.T, ledger *Ledger, tool Tool, rates RateCard, responses []string) {
	t.Helper()
	var want int64
	for _, row := range responses {
		var id, model, name string
		var tokens Tokens
		if _, err := fmt.Sscan(row, &id, &model, &name, &tokens.Input, &tokens.Output,
			&tokens.CacheWrite5m, &tokens.CacheWrite1h, &tokens.CacheRead); err != nil {
			t.Fatal(err)
		}
		if name != tool.Name {
			continue
		}
		rate, ok := rates.Lookup(model, tool.Tiers)
		if !ok {
			t.Fatalf("nothing prices %s", model)
		}
		want += rate.Cost(tokens)
	}
	report, err := Report(context.Background(), ledger, ReportOptions{Tool: tool.Name, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total.exact != want || !report.Total.Priced || want == 0 {
		t.Errorf("the %s total is %d picodollars (priced %v), want %d",
			tool.Name, report.Total.exact, report.Total.Priced, want)
	}
}

// versionFive is the schema of a version 5 ledger, as that version wrote it.
const versionFive = `
CREATE TABLE responses (
  request_id     TEXT PRIMARY KEY,
  ts             TEXT NOT NULL,
  model          TEXT NOT NULL,
  project        TEXT NOT NULL,
  session_id     TEXT NOT NULL,
  account        TEXT NOT NULL,
  account_source TEXT NOT NULL,
  subscription   TEXT NOT NULL DEFAULT 'unknown',
  subscription_source TEXT NOT NULL DEFAULT 'unknown',
  root           TEXT NOT NULL DEFAULT '',
  input          INTEGER NOT NULL,
  output         INTEGER NOT NULL,
  cache_write_5m INTEGER NOT NULL,
  cache_write_1h INTEGER NOT NULL,
  cache_read     INTEGER NOT NULL,
  tool           TEXT NOT NULL DEFAULT 'claude'
);
CREATE INDEX responses_ts ON responses (ts);
CREATE INDEX responses_project_model ON responses (project, model);
CREATE INDEX responses_account ON responses (account);
CREATE INDEX responses_tool ON responses (tool);
CREATE INDEX responses_root ON responses (tool, root);
CREATE TABLE files (
  path      TEXT PRIMARY KEY,
  offset    INTEGER NOT NULL,
  size      INTEGER NOT NULL,
  mtime_ns  INTEGER NOT NULL,
  last_seen TEXT NOT NULL,
  state     TEXT NOT NULL DEFAULT '',
  head      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE tier_changes (
  thread_id TEXT NOT NULL,
  ts        TEXT NOT NULL,
  tier      TEXT NOT NULL,
  PRIMARY KEY (thread_id, ts, tier)
);
CREATE TABLE tier_pending (
  request_id TEXT PRIMARY KEY,
  root       TEXT NOT NULL
);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE session_accounts (
  tool         TEXT NOT NULL,
  session      TEXT NOT NULL,
  since        TEXT NOT NULL,
  account      TEXT NOT NULL,
  subscription TEXT NOT NULL,
  PRIMARY KEY (tool, session, since)
);
CREATE TABLE sign_ins (
  tool         TEXT NOT NULL,
  at           TEXT NOT NULL,
  account      TEXT NOT NULL,
  subscription TEXT NOT NULL,
  PRIMARY KEY (tool, at)
);
CREATE TABLE subscriptions (id TEXT PRIMARY KEY, label TEXT NOT NULL);
CREATE TABLE thread_parents (
  tool   TEXT NOT NULL,
  thread TEXT NOT NULL,
  parent TEXT NOT NULL,
  PRIMARY KEY (tool, thread)
);
CREATE TABLE account_marks (
  tool    TEXT NOT NULL,
  kind    TEXT NOT NULL,
  at      TEXT NOT NULL,
  account TEXT NOT NULL,
  org     TEXT NOT NULL,
  PRIMARY KEY (tool, kind, at, account, org)
);
`

// ledgerRows reads every table of the ledger at path through a plain
// connection, each row as its values joined by spaces, sorted; a response row
// leads with its request id, model, tool and five token counts. The prices
// row of a tool leads with the tool.
func ledgerRows(t *testing.T, path string) map[string][]string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	read := func(query string) []string {
		t.Helper()
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			for i, column := range columns {
				values[i] = textValue(t, column, values[i])
			}
			out = append(out, strings.TrimSuffix(fmt.Sprintln(values...), "\n"))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		slices.Sort(out)
		return out
	}
	tables := map[string][]string{}
	names := slices.Contains(read("SELECT name FROM sqlite_master WHERE type = 'table'"), "names")
	for _, table := range read("SELECT name FROM sqlite_master WHERE type = 'table'") {
		query := "SELECT * FROM " + table
		switch {
		case table == "responses" && names:
			query = `SELECT r.request_id, m.name, t.name, input, output, cache_write_5m,
			                cache_write_1h, cache_read, ts, p.name, s.name, a.name, account_source,
			                b.name, subscription_source, o.name
			           FROM responses r JOIN names t ON t.id = r.tool JOIN names m ON m.id = r.model
			           JOIN names p ON p.id = r.project JOIN names s ON s.id = r.session
			           JOIN names o ON o.id = r.root JOIN names a ON a.id = r.account
			           JOIN names b ON b.id = r.subscription`
		case table == "responses":
			query = `SELECT request_id, model, tool, input, output, cache_write_5m, cache_write_1h,
			                cache_read, ts, project, session_id, account, account_source,
			                subscription, subscription_source, root FROM responses`
		case table == "tier_pending" && names:
			query = `SELECT p.request_id, n.name FROM tier_pending p JOIN names n ON n.id = p.root`
		}
		tables[table] = read(query)
	}
	return tables
}

// textValue is a value of a version 7 ledger's column as an earlier version
// stored it: a packed response id as its text, a response time in timeLayout
// ("" for none), an evidence rank as its word.
func textValue(t *testing.T, column string, value any) any {
	t.Helper()
	switch v := value.(type) {
	case []byte:
		if column != "request_id" {
			return v
		}
		id, err := unpackID(v)
		if err != nil {
			t.Fatal(err)
		}
		return id
	case int64:
		switch column {
		case "ts":
			return storedTime(v)
		case "account_source", "subscription_source":
			return evidenceWord(v)
		}
	case nil:
		if column == "ts" {
			return ""
		}
	}
	return value
}

// attributionOf is the account, its evidence, the plan and its evidence of
// the response id.
func attributionOf(t *testing.T, ledger *Ledger, id string) [4]string {
	t.Helper()
	var got [4]string
	var accountRank, subscriptionRank int64
	if err := ledger.db.QueryRow(
		`SELECT a.name, r.account_source, s.name, r.subscription_source FROM responses r
		   JOIN names a ON a.id = r.account JOIN names s ON s.id = r.subscription
		  WHERE r.request_id = ?`, packID(id),
	).Scan(&got[0], &accountRank, &got[2], &subscriptionRank); err != nil {
		t.Fatal(err)
	}
	got[1], got[3] = evidenceWord(accountRank), evidenceWord(subscriptionRank)
	return got
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
