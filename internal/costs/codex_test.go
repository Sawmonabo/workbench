package costs

import (
	"fmt"
	"testing"
)

// A fork's first token_count is skipped when it repeats its parent's
// response, so the response is not counted twice. Skipping it anywhere else
// silently drops a real response from the ledger, the only lasting record of
// Codex usage: a fork that copied no history carries its parent's total into
// its own first response, and a fork whose parent's rollout is gone holds the
// only trace of the parent's last response.
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
	rows := func(holds bool, lines ...string) int {
		file := &FileState{Path: "/w/fork.jsonl", Holds: func(thread, _ string) bool {
			return holds && thread == parent
		}}
		n := 0
		for _, line := range lines {
			n += len(codex{}.Parse([]byte(line), file))
		}
		return n
	}
	for _, c := range []struct {
		name  string
		holds bool
		lines []string
		want  int
	}{
		{"copied fork, parent's rollout read: the copy is the parent's", true, []string{meta, copied, count}, 0},
		{"copied fork, parent's rollout gone: the copy is the only trace", false, []string{meta, copied, count}, 1},
		{"fork that copied nothing: its own first response", true, []string{meta, settings, count}, 1},
	} {
		if got := rows(c.holds, c.lines...); got != c.want {
			t.Errorf("%s: %d rows, want %d", c.name, got, c.want)
		}
	}
}
