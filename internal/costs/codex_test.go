package costs

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// A fork's first token_count may repeat its parent's response under another
// key; it is dropped once the parent's rollout is read, so the response is
// not counted twice. Dropping it anywhere else silently loses a real
// response from the ledger, the only lasting record of Codex usage: a fork
// that copied no history carries its parent's total into its own first
// response, and while the parent's rollout is not read the copy is the only
// trace of the parent's response.
func TestCodexForkCountsEveryResponseOnce(t *testing.T) {
	const parent, fork = "parent-thread", "fork-thread"
	meta := fmt.Sprintf(`{"timestamp":"2026-07-01T10:00:00.000Z","type":"session_meta","payload":`+
		`{"id":%q,"forked_from_id":%q,"cwd":"/w","cli_version":"0.149.1"}}`, fork, parent)
	settings := fmt.Sprintf(`{"timestamp":"2026-07-01T10:00:00.001Z","type":"event_msg","payload":`+
		`{"type":"thread_settings_applied","thread_id":%q,"thread_settings":{"model":"gpt-5.5"}}}`, fork)
	copied := `{"timestamp":"2026-07-01T10:00:00.001Z","type":"response_item","payload":` +
		`{"type":"message","role":"user","content":[]}}`
	// A response whose running total includes the parent's 9,000 tokens.
	count := `{"timestamp":"2026-07-01T10:00:01.000Z","type":"event_msg","payload":{"type":"token_count",` +
		`"info":{"total_token_usage":{"input_tokens":10000,"output_tokens":100},` +
		`"last_token_usage":{"input_tokens":1000,"output_tokens":10}},"rate_limits":{"primary":{"used_percent":3}}}}`
	for _, c := range []struct {
		name       string
		lines      []string
		parentHeld bool
		wantStored int
	}{
		{"copied fork, parent's rollout read: the copy is the parent's", []string{meta, copied, count}, true, 0},
		{"copied fork, parent's rollout not read: the copy is the only trace", []string{meta, copied, count}, false, 1},
		{"fork that copied nothing: its own first response", []string{meta, settings, count}, true, 1},
	} {
		ledger, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.sqlite"), true)
		if err != nil {
			t.Fatal(err)
		}
		file := &FileState{Path: "/w/fork.jsonl"}
		err = ledger.Transaction(context.Background(), nil, func(tx *Tx) error {
			for _, line := range c.lines {
				for _, u := range (codex{}).Parse([]byte(line), file) {
					u.Tool, u.Account = "codex", "unknown"
					if err := tx.Upsert(u, "sweep"); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err == nil {
			err = ledger.DropHeldCopies(context.Background(), "codex", func(thread, _ string) bool {
				return c.parentHeld && thread == parent
			})
		}
		var stored int
		if err == nil {
			err = ledger.db.QueryRow("SELECT COUNT(*) FROM responses").Scan(&stored)
		}
		_ = ledger.Close()
		if err != nil {
			t.Fatal(err)
		}
		if stored != c.wantStored {
			t.Errorf("%s: %d rows stored, want %d", c.name, stored, c.wantStored)
		}
	}
}
