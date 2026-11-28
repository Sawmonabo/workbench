package costs

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// The upgrade to schema version 7 rewrites every response of the ledger, the
// only lasting record of spend, into names, numbers and packed IDs. A row
// that came back with another ID, time, name or evidence, or not at all,
// would misstate or lose spend for good, as the transcripts it came from are
// gone. Each row below is a shape the conversion treats apart: every kind of
// ID (and the edges of the packed ones), a copy key of each tool, an undated
// row, every evidence word, a root that is its session and one that is not,
// and both kinds of a waiting row's TierFrom.
func TestUpgradeToSevenKeepsEveryRow(t *testing.T) {
	ids := []string{
		"req_011CVx3mFq9TbYp2Lw8sKd4H",  // a Claude Code ID
		"req_011C00000000000000000001",  // leading zeros
		"req_011Czzzzzzzzzzzzzzzzzzzz",  // the largest base-62 value
		"req_011C0000000000000000000",   // 19 characters
		"req_011C000000000000000000000", // 21 characters
		"codex:resp_00a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
		"codex:resp_ABCDEF", // uppercase hex
		"codex:resp_abc",    // odd hex
		"codex:resp_mock",   // not hex
		"codex:tc:0f" + strings.Repeat("e1", 31),
		"copy:0a1b2c3d-4e5f-7a6b-8c7d-9e0f1a2b3c4d@2026-07-01T10:00:00.000Z:req_011CVx3mFq9TbYp2Lw8sKd4H",
		"codex:copy:0a1b2c3d-4e5f-7a6b-8c7d-9e0f1a2b3c4d@2026-07-01T10:00:00.000Z:resp_00a1",
		"req_probe_1700000000000",
	}
	words := []string{
		EvidenceTranscript, EvidenceSession, EvidenceObserved, EvidenceInferred, EvidenceUnknown,
	}
	statements := []string{
		versionFive,
		priceSchema,
		`INSERT INTO meta VALUES ('schema_version', '6')`,
	}
	for i, id := range ids {
		tool, root, ts := "claude", "s1", fmt.Sprintf("2026-11-%02dT10:00:00.%03dZ", i+1, i)
		if strings.HasPrefix(id, "codex:") {
			tool, root = "codex", "thread-root" // a subagent's row under its root thread
		}
		if i == 3 {
			ts = "" // a response with no time
		}
		statements = append(statements, fmt.Sprintf(`INSERT INTO responses (request_id, ts,
		  model, project, session_id, account, account_source, subscription,
		  subscription_source, root, input, output, cache_write_5m, cache_write_1h,
		  cache_read, tool)
		  VALUES ('%s', '%s', 'model-%d', '/w/p%d', 's1', 'a%d@example.test', '%s',
		    'claude:org-%d', '%s', '%s', %d, %d, %d, %d, %d, '%s')`,
			id, ts, i%3, i%4, i%2, words[i%5], i%3, words[(i+2)%5], root,
			i, 10*i, 100*i, 1000*i, 10000*i+1<<40, tool))
	}
	statements = append(statements,
		`INSERT INTO responses (request_id, ts, model, project, session_id, account,
		   account_source, input, output, cache_write_5m, cache_write_1h, cache_read, tool)
		 VALUES ('req-empty', '2026-11-30T23:59:59.999Z', '', '', '', '', 'unknown',
		   0, 0, 0, 0, 0, 'claude')`,
		`INSERT INTO tier_pending VALUES ('codex:resp_00a1b2c3d4e5f60718293a4b5c6d7e8f9012345678',
		   'thread-root')`,
		`INSERT INTO tier_pending VALUES ('codex:resp_mock',
		   'turn:0a1b2c3d-4e5f-7a6b-8c7d-9e0f1a2b3c4d@2026-07-01T10:00:00.000Z')`,
	)
	path := oldLedger(t, statements...)
	before := ledgerRows(t, path)
	if len(before["responses"]) != len(ids)+1 || len(before["tier_pending"]) != 2 {
		t.Fatalf("the version 6 ledger holds %d responses and %d waiting rows",
			len(before["responses"]), len(before["tier_pending"]))
	}
	ledger, err := openLedger(path, false, true, nil)
	if err != nil {
		t.Fatalf("the upgrade failed: %v", err)
	}
	if version, err := ledger.Meta("schema_version"); err != nil || version != schemaVersion {
		t.Fatalf("the ledger is at version %q (%v), want %s", version, err, schemaVersion)
	}
	if due, err := ledger.Meta(compactDue); err != nil || due != "" || !ledger.compacted ||
		ledger.compactErr != nil {
		t.Errorf("the compaction is still due (%q, %v, %v)", due, err, ledger.compactErr)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	after := ledgerRows(t, path)
	for table, rows := range before {
		if table != "meta" && !slices.Equal(after[table], rows) {
			t.Errorf("%s holds\n%s\nafter the upgrade, want\n%s", table,
				strings.Join(after[table], "\n"), strings.Join(rows, "\n"))
		}
	}
}

// An upgrade that met a value it cannot store as version 7 does, and stored
// something else or nothing, would alter spend for good. It refuses instead,
// naming the value, and the ledger stays exactly as it was, so the release
// before keeps reading it.
func TestUpgradeToSevenRefusesWhatItCannotConvert(t *testing.T) {
	for _, c := range []struct{ name, evidence, ts, want string }{
		{"an evidence word of an earlier build", "sweep", "2026-11-01T10:00:00.000Z", `"sweep"`},
		{
			"a time not in the ledger's form", EvidenceUnknown, "2026-11-01T10:00:00Z",
			`"2026-11-01T10:00:00Z"`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := oldLedger(t, versionFive, priceSchema,
				`INSERT INTO meta VALUES ('schema_version', '6')`,
				fmt.Sprintf(`INSERT INTO responses (request_id, ts, model, project, session_id,
				   account, account_source, input, output, cache_write_5m, cache_write_1h,
				   cache_read, tool)
				 VALUES ('req-1', '%s', 'm', '/w/a', 's1', 'a@example.test', '%s',
				   1, 2, 3, 4, 5, 'claude')`, c.ts, c.evidence))
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			ledger, err := openLedger(path, false, true, nil)
			if err == nil {
				_ = ledger.Close()
				t.Fatal("the upgrade converted a value it cannot hold")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the refusal %q does not name %s", err, c.want)
			}
			kept, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(kept, original) {
				t.Error("the refused upgrade changed the ledger")
			}
		})
	}
}
