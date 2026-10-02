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
// api key, access key, private key) may carry a suffix; a weak word that also
// begins ordinary names (key, pat, auth, cookie, session) must end the key, so
// `openai_key =` and `X-Auth:` are masked and `keybindings =` is not.
var secretLine = regexp.MustCompile(
	`(?i)((?:token|secret|passw(?:or)?d|api[_-]?key|access[_-]?key|credential|authorization|` +
		`private[_-]?key)[\w."'-]*\s*[:=]\s*|` +
		`(?:^|[^a-z0-9])(?:key|pat|auth|cookie|set-cookie|session)["']?\s*[:=]\s*)\S.*$|(bearer\s+)\S+`,
)

// secretFlag masks the value after a credential-named command-line flag on one
// line: `"--token", "x"`, `--api-key=x`, `--password x`.
var secretFlag = regexp.MustCompile(
	`(?i)((?:^|[\s"'\[,(])--?[\w-]*(?:token|secret|passw(?:or)?d|api-?key|key|auth|pat|cookie)` +
		`["']?(?:\s*[=,]\s*|\s+)["']?)[^"'\s,\[\]]+`,
)

// secretFlagEnd matches a line that ends with such a flag, whose value is on
// the next line (a multi-line array).
var secretFlagEnd = regexp.MustCompile(
	`(?i)--?[\w-]*(?:token|secret|passw(?:or)?d|api-?key|key|auth|pat|cookie)["']?\s*,?\s*$`,
)

// urlUserInfo masks the password in https://user:password@host.
var urlUserInfo = regexp.MustCompile(`(://[^/\s:@]+:)[^/\s@]+(@)`)

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
	valueNext := false
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
			text, valueNext = "[REDACTED]", false
		default:
			valueNext = secretFlagEnd.MatchString(text)
			text = secretLine.ReplaceAllStringFunc(text, func(match string) string {
				parts := secretLine.FindStringSubmatch(match)
				if parts[1] != "" {
					return parts[1] + "[REDACTED]"
				}
				return parts[2] + "[REDACTED]"
			})
			text = secretFlag.ReplaceAllString(text, "${1}[REDACTED]")
			text = urlUserInfo.ReplaceAllString(text, "${1}[REDACTED]${2}")
			text = secretShape.ReplaceAllString(text, "[REDACTED]")
			text = secretBlob.ReplaceAllStringFunc(text, maskBlob)
		}
		redacted[i] = marker + text
	}
	return redacted
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
