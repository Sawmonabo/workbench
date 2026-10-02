package machine

import (
	"strings"
	"testing"
)

// The plan view shows each file's diff, and Codex and Claude Code configs hold
// tokens. Risk: a credential reaches the terminal or a log. A known secret
// value, the value of any credential-named key (including a bare key, pat, auth
// or cookie), a value on the line after a credential flag, a password in a URL
// and anything shaped like a token must never survive.
func TestDiffNeverShowsCredentials(t *testing.T) {
	leaks := []string{
		"hunter2", "glpat-abc123def456ghi789", "abc.def", "ghp_" + strings.Repeat("a1", 18),
		"sk-" + strings.Repeat("Zq9", 8), "AKIA" + strings.Repeat("7", 16), "flagvalue1", "hunter3",
		"cookievalue", "authvalue", "nextline-secret", "arrayvalue1",
	}
	lines := redactDiff([]string{
		`+JIRA_API_TOKEN = "hunter2"`,
		` "note": "hunter2 is in a comment"`,
		`-  "gitlab_token": "glpat-abc123def456ghi789",`,
		`+Authorization: Bearer abc.def`,
		`+my_pat = "` + leaks[3] + `"`,
		`+openai_key = "` + leaks[4] + `"`,
		`+AWS_ACCESS_KEY_ID = "` + leaks[5] + `"`,
		`+args = ["--token", "arrayvalue1"]`,
		`+command = "tool --api-key=flagvalue1"`,
		`+url = "https://user:hunter3@example.com/repo"`,
		`+Cookie: cookievalue`,
		`+X-Auth: authvalue`,
		`+  "--password",`,
		`+  "nextline-secret",`,
		` timeout = 5`,
		` keybindings = "vim"`,
	}, []string{"hunter2"})
	text := strings.Join(lines, "\n")
	for _, leak := range leaks {
		if strings.Contains(text, leak) {
			t.Errorf("diff still shows %q:\n%s", leak, text)
		}
	}
	for _, kept := range []string{"timeout = 5", `keybindings = "vim"`} {
		if !strings.Contains(text, kept) {
			t.Errorf("diff lost an ordinary line %q:\n%s", kept, text)
		}
	}
}
