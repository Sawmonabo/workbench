package machine

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Caps on the diff a plan carries for one file. The view shows these lines;
// a file that changes more is cut at a whole line and marked truncated.
const (
	maxDiffLines = 200
	maxDiffBytes = 32 << 10
)

// fileTitles are the plain names of every target the machine source manages,
// by path relative to the destination. A path not listed (a folder, a removed
// old file) shows its base name.
var fileTitles = map[string]string{
	".bashrc":                     "Bash startup file",
	".zshrc":                      "Zsh startup file",
	".gitconfig":                  "Git settings",
	".gitconfig-personal":         "Git settings (personal)",
	".gitconfig-work":             "Git settings (work)",
	".terminal-font-setup.sh":     "Terminal font setup",
	".config/ruff/pyproject.toml": "Ruff settings",
	".config/ty/ty.toml":          "ty settings",
	".config/tmux/tmux.conf":      "tmux settings",
	".config/oh-my-posh/catppuccin_mocha.omp.json":          "Shell prompt theme",
	".local/bin/win-browser":                                "Windows browser opener",
	".claude/settings.json":                                 "Claude Code settings",
	".claude/CLAUDE.md":                                     "Claude Code instructions",
	".claude/scripts/statusline.py":                         "Claude Code status line",
	".codex/config.toml":                                    "Codex settings",
	".codex/hooks.json":                                     "Codex hooks",
	".codex/AGENTS.md":                                      "Codex instructions",
	".config/Code/User/settings.json":                       "VS Code settings",
	"Library/Application Support/Code/User/settings.json":   "VS Code settings",
	".local/bin/claude-costs":                               "Old claude-costs command",
	".local/share/bash-completion/completions/claude-costs": "Old claude-costs completions",
}

// fileTitle is the plain name for a target path relative to the destination.
func fileTitle(relative string) string {
	if title, ok := fileTitles[filepath.ToSlash(relative)]; ok {
		return title
	}
	return filepath.Base(relative)
}

// mergedTargets reports which of the targets native renders from a modify_
// source: native merges into those files and keeps the owner's own keys.
func (p *preparation) mergedTargets(
	ctx context.Context,
	c operation.Context,
	targets []string,
) (map[string]bool, error) {
	merged := map[string]bool{}
	if len(targets) == 0 {
		return merged, nil
	}
	output, err := p.run(ctx, c, append([]string{"source-path"}, targets...)...)
	if err != nil {
		return nil, err
	}
	sources := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(sources) != len(targets) {
		return nil, operation.Fail(
			operation.ExitFailed,
			"native",
			"Unexpected native source-path output",
		)
	}
	for i, source := range sources {
		merged[targets[i]] = strings.HasPrefix(filepath.Base(source), "modify_")
	}
	return merged, nil
}

// secretLine masks the value on a line whose key names a credential, so a
// token the owner keeps in a file Workbench merges into is never shown even
// though Workbench did not put it there. Known secret values are replaced
// first by [operation.Redact]; this and the patterns below cover the rest. A key
// named by a strong word (token, secret, password, credential, authorization,
// api key, access key, private key, passphrase) may carry a suffix; a weak word
// that also begins ordinary names (key, pat, auth, cookie, session, pass, pwd)
// must end the key, so `openai_key =`, `DB_PASS =` and `X-Auth:` are masked and
// `keybindings =` and `compass =` are not.
var secretLine = regexp.MustCompile(
	`(?i)((?:token|secret|passw(?:or)?d|passphrase|api[_-]?key|access[_-]?key|credential|authorization|` +
		`private[_-]?key)[\w."'-]*\s*[:=]\s*|` +
		`(?:^|[^a-z0-9])(?:key|pat|auth|cookie|set-cookie|session|pass|pwd)["']?\s*[:=]\s*)\S.*$|(bearer\s+)\S+`,
)

// secretFlag masks the value after a credential-named command-line flag on one
// line: `"--token", "x"`, `--api-key=x`, `--password x`.
var secretFlag = regexp.MustCompile(
	`(?i)((?:^|[\s"'\[,(])--?[\w-]*(?:token|secret|passw(?:or)?d|pass|pwd|api-?key|key|auth|pat|cookie)` +
		`["']?(?:\s*[=,]\s*|\s+)["']?)[^"'\s,\[\]]+`,
)

// secretFlagEnd matches a line that ends with such a flag, whose value is on
// the next line (a multi-line array).
var secretFlagEnd = regexp.MustCompile(
	`(?i)--?[\w-]*(?:token|secret|passw(?:or)?d|pass|pwd|api-?key|key|auth|pat|cookie)["']?\s*,?\s*$`,
)

// shortPassFlag is the one-letter password flag of mysql, mariadb, sshpass and
// mongo, `-p` or `-P`, followed by its value as a separate word. A value that
// is a path or a variable (`mkdir -p ~/x`, `-p $PW`) is left to read, since it
// is not a password typed into a file.
var shortPassFlag = regexp.MustCompile(
	`((?:^|[\s"'\[,(])-[pP](?:["']?(?:\s*=\s*|\s*,\s*|\s+)["']?))` +
		`[^"'\s,\[\]/~.$%][^"'\s,\[\]]*`,
)

// shortPassFlagEnd matches a line that ends with `-p` or `-P`, whose value is
// on the next line.
var shortPassFlagEnd = regexp.MustCompile(`(?:^|[\s"'\[,(])-[pP]["']?\s*,?\s*$`)

// attachedPassFlag is `-psecret`, the value written right after the flag, as
// mysql takes it. Only a line that names a tool taking it is masked, since `-p`
// then a word is also `-print` and `-pipe`.
var attachedPassFlag = regexp.MustCompile(`((?:^|[\s"'\[,(])-[pP])[^\s"'\[\],=/~.$%-][^"'\s,\[\]]*`)

// passwordTool names the commands that take a password after -p.
var passwordTool = regexp.MustCompile(
	`\b(?:mysql|mysqldump|mysqladmin|mysqlpump|mariadb|mariadb-dump|mariadb-admin|sshpass|mongo|mongosh|` +
		`mongodump|mongorestore|mongoexport|mongoimport)\b`,
)

// flagEnds reports whether text ends with a flag that takes a credential on
// the next line or list item, and whether it is the one-letter form.
func flagEnds(text string) (ends, short bool) {
	if secretFlagEnd.MatchString(text) {
		return true, false
	}
	if shortPassFlagEnd.MatchString(text) {
		return true, true
	}
	return false, false
}

// pathLike reports a value that is a path or a variable rather than a typed
// password, which a one-letter flag's value may be (`mkdir -p /tmp/x`).
func pathLike(value string) bool {
	value = strings.Trim(value, `"' ,`)
	return value != "" && strings.ContainsRune("/~.$%", rune(value[0]))
}

// urlUserInfo masks the password in https://user:password@host.
var urlUserInfo = regexp.MustCompile(`(://[^/\s:@]+:)[^/\s@]+(@)`)

// urlKeyUser masks a key kept as the user of a URL with no password, such as a
// Sentry DSN `https://KEY@host/1`: a name of 16 or more characters. A short
// user such as `git@` is a name and stays.
var urlKeyUser = regexp.MustCompile(`(://)[A-Za-z0-9._~-]{16,}(@)`)

// urlWebhook masks the path of a webhook address, which is the credential:
// Slack, Discord, Microsoft Teams, Zapier and any `/webhooks/` path.
var urlWebhook = regexp.MustCompile(
	`(?i)(hooks\.slack\.com/(?:services|workflows|triggers)/|` +
		`discord(?:app)?\.com/api/(?:v\d+/)?webhooks/|` +
		`office(?:365)?\.com/webhook\w*/|` +
		`hooks\.zapier\.com/hooks/catch/|` +
		`/webhooks?/)[^\s"'?#]+`,
)

// urlQuery masks the value of a URL query parameter named like a credential.
var urlQuery = regexp.MustCompile(
	`(?i)([?&](?:[\w.-]*(?:token|secret|passw(?:or)?d|credential|signature|api[_-]?key|access[_-]?key)[\w.-]*|` +
		`(?:[\w.-]*[_.-])?(?:key|pat|auth|pass|pwd|sig|cookie|session|jwt|apikey))=)[^&\s"'#]+`,
)

// secretShape masks values that look like credentials wherever they stand: a
// GitHub, GitLab, OpenAI, AWS or Slack token, a JWT, or a long hex run.
var secretShape = regexp.MustCompile(
	`\bgh[pousr]_[A-Za-z0-9]{20,}|\bgithub_pat_\w{20,}|\bglpat-[\w-]{16,}|\bsk-[\w-]{16,}|` +
		`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b|\bxox[abprs]-[\w-]{10,}|\beyJ[\w-]{8,}\.[\w-]{8,}\.[\w-]{8,}|` +
		`\b[0-9a-fA-F]{40,}\b`,
)

// secretBlob finds long base64-like runs; maskBlob decides which are secrets.
var secretBlob = regexp.MustCompile(`[A-Za-z0-9+_=-]{40,}`)

// maskBlob keeps a long run that has no digit or no letter, such as a rule of
// dashes or a long word, and masks the rest.
func maskBlob(run string) string {
	digit := strings.ContainsAny(run, "0123456789")
	letter := strings.ContainsAny(run, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	if digit && letter {
		return "[REDACTED]"
	}
	return run
}

// redactDiff masks the secrets in the text lines of a diff. A line is a diff
// marker (" ", "-", "+") and its text.
func redactDiff(lines []string, secrets []string) []string {
	redacted := make([]string, len(lines))
	valueNext, shortNext := false, false
	for i, line := range lines {
		line = operation.Redact(line, secrets)
		marker, text := "", line
		if line != "" && strings.ContainsRune(" -+", rune(line[0])) {
			marker, text = line[:1], line[1:]
		}
		switch {
		case strings.HasPrefix(line, "@@"):
			valueNext = false
		case valueNext && strings.TrimSpace(text) != "":
			if !shortNext || !pathLike(text) {
				text = "[REDACTED]"
			}
			valueNext = false
		default:
			valueNext, shortNext = flagEnds(text)
			text = secretLine.ReplaceAllStringFunc(text, func(match string) string {
				parts := secretLine.FindStringSubmatch(match)
				if parts[1] != "" {
					return parts[1] + "[REDACTED]"
				}
				return parts[2] + "[REDACTED]"
			})
			text = secretFlag.ReplaceAllString(text, "${1}[REDACTED]")
			text = shortPassFlag.ReplaceAllString(text, "${1}[REDACTED]")
			if passwordTool.MatchString(text) {
				text = attachedPassFlag.ReplaceAllString(text, "${1}[REDACTED]")
			}
			text = urlUserInfo.ReplaceAllString(text, "${1}[REDACTED]${2}")
			text = urlKeyUser.ReplaceAllString(text, "${1}[REDACTED]${2}")
			text = urlWebhook.ReplaceAllString(text, "${1}[REDACTED]")
			text = urlQuery.ReplaceAllString(text, "${1}[REDACTED]")
			text = secretShape.ReplaceAllString(text, "[REDACTED]")
			text = secretBlob.ReplaceAllStringFunc(text, maskBlob)
		}
		redacted[i] = marker + text
	}
	return redacted
}

// maxSettingValue is the longest value a settings line shows.
const maxSettingValue = 80

// keySegments is a key path as names: list indexes dropped, so `args[0]` is
// `args` and `env.MY_TOKEN` is `env`, `MY_TOKEN`.
func keySegments(path string) []string {
	var names []string
	for _, name := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '.' || r == '[' || r == ']'
	}) {
		if strings.Trim(name, "0123456789") != "" {
			names = append(names, name)
		}
	}
	return names
}

// maskKey is the key as the masking patterns read it: its last name, whatever
// the length of the path before it, since the credential word is judged on the
// key itself and a long name only over-masks.
func maskKey(path string) string {
	names := keySegments(path)
	if len(names) == 0 {
		return path
	}
	return names[len(names)-1]
}

// secretParent reports whether a name before the last in the path is itself
// credential-shaped (`auth.user`, `tokens.main`): everything under it is masked.
func secretParent(path string) bool {
	names := keySegments(path)
	for i := 0; i+1 < len(names); i++ {
		if secretLine.MatchString(names[i] + " = x") {
			return true
		}
	}
	return false
}

// settingLines renders changed settings for the plan view, one per line:
// "~ path  old → new", "+ path  value  added", "- path  value  removed". Values
// pass through the same masking as a diff: a value is masked when its key path
// or its text looks like a credential, and known secret values never show.
// Masking runs on the whole text before a long value is shortened.
func settingLines(changes []operation.SettingChange, secrets []string) []string {
	const separator = " = "
	marked := make([]string, len(changes))
	for i, change := range changes {
		key := maskKey(change.Path)
		value := change.Before + " → " + change.After
		switch {
		case change.Added:
			value = change.After
		case change.Removed:
			value = change.Before
		case change.Reordered:
			value = "reordered"
		}
		marked[i] = " " + key + separator + value
	}
	masked := redactDiff(marked, secrets)
	lines := make([]string, len(changes))
	for i, change := range changes {
		key := maskKey(change.Path)
		value := "[REDACTED]"
		// A line that lost its key was replaced whole, as the value of a
		// credential flag on the line before; keep the path and mask the value.
		if text, ok := strings.CutPrefix(masked[i], " "+key+separator); ok {
			value = text
		}
		// A list item right after a credential flag is its value.
		for _, before := range change.Follows {
			if ends, short := flagEnds(before); ends && (!short || !pathLike(item(change))) {
				value = "[REDACTED]"
			}
		}
		if secretParent(change.Path) {
			value = "[REDACTED]"
		}
		if runes := []rune(value); len(runes) > maxSettingValue {
			value = string(runes[:maxSettingValue]) + "…"
		}
		if hashKey(change.Path) {
			// A hash is not for reading, and long hex runs are masked anyway.
			lines[i] = hashLine(change)
			continue
		}
		switch {
		case change.Added:
			lines[i] = "+ " + change.Path + "  " + value + "  added"
		case change.Removed:
			lines[i] = "- " + change.Path + "  " + value + "  removed"
		default:
			lines[i] = "~ " + change.Path + "  " + value
		}
	}
	return lines
}

// item is the value of a setting as the list shows it.
func item(change operation.SettingChange) string {
	if change.Removed {
		return change.Before
	}
	return change.After
}

// hashKey reports whether a key path ends in a hash, such as a trusted_hash.
func hashKey(path string) bool {
	last := path[strings.LastIndexAny(path, ".]")+1:]
	return strings.Contains(strings.ToLower(last), "hash")
}

// hashLine is the setting line of a hash: that it changed, not its value.
func hashLine(change operation.SettingChange) string {
	switch {
	case change.Added:
		return "+ " + change.Path + "  added (hash hidden)"
	case change.Removed:
		return "- " + change.Path + "  removed (hash hidden)"
	}
	return "~ " + change.Path + "  changed (hash hidden)"
}

// capDiff joins diff lines into the text a plan carries: at most maxDiffLines
// lines and maxDiffBytes bytes, cut at a whole line.
func capDiff(lines []string) (text string, truncated bool) {
	var b strings.Builder
	for i, line := range lines {
		if i >= maxDiffLines || b.Len()+len(line)+1 > maxDiffBytes {
			return b.String(), true
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String(), false
}
