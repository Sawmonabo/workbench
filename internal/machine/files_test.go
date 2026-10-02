package machine

import (
	"strings"
	"testing"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// The plan view shows each file's diff, and Codex and Claude Code configs hold
// tokens. Risk: a credential reaches the terminal or a log. A known secret
// value, the value of any credential-named key (including a bare key, pat, auth
// or cookie), a value on the line after a credential flag, a password in a URL
// and anything shaped like a token must never survive.
func TestDiffNeverShowsCredentials(t *testing.T) {
	leaks := []string{
		"hunter2",
		"glpat-abc123def456ghi789",
		"abc.def",
		"ghp_" + strings.Repeat("a1", 18),
		"sk-" + strings.Repeat("Zq9", 8),
		"AKIA" + strings.Repeat("7", 16),
		"flagvalue1",
		"hunter3",
		"cookievalue",
		"authvalue",
		"nextline-secret",
		"arrayvalue1",
		"dbpassval",
		"pwdval",
		"passwdval",
		"sshpassval",
		"mysqlattached",
		"nextp-secret",
		"sentrykey0123456789abcdef",
		"slackhooksecret",
		"discordtokenval",
		"teamswebhookval",
		"querysecretval",
		"sigval",
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
		`+DB_PASS = "dbpassval"`,
		`+MYSQL_PWD = "pwdval"`,
		`+passwd: passwdval`,
		`+command = "sshpass -p sshpassval ssh host"`,
		`+command = "mysql -uroot -pmysqlattached"`,
		`+  "-p",`,
		`+  "nextp-secret",`,
		`+dsn = "https://sentrykey0123456789abcdef@o1.ingest.sentry.io/5"`,
		`+hook = "https://hooks.slack.com/services/T000/B000/slackhooksecret"`,
		`+hook = "https://discord.com/api/webhooks/123456/discordtokenval"`,
		`+hook = "https://example.webhook.office.com/webhookb2/teamswebhookval@x/IncomingWebhook/y"`,
		`+url = "https://example.com/x?id=1&client_secret=querysecretval&sig=sigval"`,
		` timeout = 5`,
		` keybindings = "vim"`,
		` compass = "north"`,
		` command = "mkdir -p ~/.cache/x"`,
		` remote = "git@github.com:me/repo.git"`,
	}, []string{"hunter2"})
	text := strings.Join(lines, "\n")
	for _, leak := range leaks {
		if strings.Contains(text, leak) {
			t.Errorf("diff still shows %q:\n%s", leak, text)
		}
	}
	for _, kept := range []string{
		"timeout = 5", `keybindings = "vim"`, `compass = "north"`, "mkdir -p ~/.cache/x", "git@github.com",
	} {
		if !strings.Contains(text, kept) {
			t.Errorf("diff lost an ordinary line %q:\n%s", kept, text)
		}
	}
}

// A merged file's changes are listed as settings, not lines, so the line
// masking does not see them in place. Risk: a credential the owner keeps in
// Claude Code settings or Codex config reaches the terminal through that list.
// A value under a credential-named key, a token-shaped value, a known secret
// and the item after a credential flag in a list, plain or mixed with
// objects, must all stay masked, however long the key name is and whatever
// credential-named table it sits under.
func TestSettingListNeverShowsCredentials(t *testing.T) {
	before := `{"env": {"JIRA_API_TOKEN": "hunter2", "NOTE": "ghp_` + strings.Repeat("a1", 18) +
		`"}, "args": ["--token", "flagvalue1"], "mixed": [{"a": 1}, "--api-key", "mixedvalue1"], "timeout": 10}`
	after := `{"env": {"JIRA_API_TOKEN": "hunter3", "NOTE": "ghp_` + strings.Repeat("b2", 18) +
		`"}, "args": ["--token", "flagvalue2"], "mixed": [{"a": 1}, "--api-key", "mixedvalue2"], ` +
		`"timeout": 5, "known": "hunter4", ` +
		`"env": {"SECRET_KEY_FOR_THE_PRODUCTION_DATABASE_SERVICE": "longkeysecret05", ` +
		`"API_TOKEN_USED_BY_THE_NIGHTLY_INTEGRATION_JOBS": "longtoken06"}, ` +
		`"auth": {"user": "parentsecret07"}}`
	changes, _, ok := operation.SettingsDiff("json", []byte(before), []byte(after))
	if !ok {
		t.Fatal("synthetic settings did not parse")
	}
	text := strings.Join(settingLines(changes, []string{"hunter4"}), "\n")
	for _, leak := range []string{
		"hunter2", "hunter3", "hunter4", "flagvalue1", "flagvalue2", "mixedvalue1", "mixedvalue2",
		"longkeysecret05", "longtoken06", "parentsecret07", strings.Repeat("a1", 18),
		strings.Repeat("b2", 18),
	} {
		if strings.Contains(text, leak) {
			t.Errorf("setting list still shows %q:\n%s", leak, text)
		}
	}
	if !strings.Contains(text, "timeout  10 → 5") {
		t.Errorf("setting list lost an ordinary change:\n%s", text)
	}
}
