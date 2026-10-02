package machine

import (
	"strings"
	"testing"
)

// The plan view shows each file's diff, and Codex and Claude Code configs hold
// tokens. Risk: a credential reaches the terminal or a log. A known secret
// value and the value of any credential-named key must never survive.
func TestDiffNeverShowsCredentials(t *testing.T) {
	lines := redactDiff([]string{
		`+JIRA_API_TOKEN = "hunter2"`,
		` "note": "hunter2 is in a comment"`,
		`-  "gitlab_token": "glpat-abc",`,
		`+Authorization: Bearer abc.def`,
		` timeout = 5`,
	}, []string{"hunter2"})
	text := strings.Join(lines, "\n")
	for _, leak := range []string{"hunter2", "glpat-abc", "abc.def"} {
		if strings.Contains(text, leak) {
			t.Errorf("diff still shows %q:\n%s", leak, text)
		}
	}
	if !strings.Contains(text, "timeout = 5") {
		t.Errorf("diff lost an ordinary line:\n%s", text)
	}
}
