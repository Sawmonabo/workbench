package envfile

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// WorkEnvNames are the names work.env holds, in order.
var WorkEnvNames = []string{
	"JIRA_BASE_URL",
	"JIRA_EMAIL",
	"JIRA_API_TOKEN",
	"CONFLUENCE_BASE_URL",
	"CONFLUENCE_EMAIL",
	"CONFLUENCE_API_TOKEN",
	"TEMPO_API_TOKEN",
	"GITLAB_URL",
	"GITLAB_TOKEN",
}

// TokenKeys are the retired machine answers 0005 moves, each with the
// work.env name it moves to.
var TokenKeys = [][2]string{{"jira_api_token", "JIRA_API_TOKEN"}, {"gitlab_token", "GITLAB_TOKEN"}}

// WorkEnvPath is work.env in Workbench's config folder config.
func WorkEnvPath(config string) string { return filepath.Join(config, "work.env") }

// WorkValues are the work.env values the answers in data give: the tokens
// they hold, the work email (work_email on both roles) as JIRA_EMAIL, and the
// jira_url and gitlab_url addresses. An answer data lacks gives no value. bad
// is the first answer that isn't one line of text.
func WorkValues(data map[string]any) (values map[string]string, bad string) {
	email := "email"
	if data["machine_role"] == "both" {
		email = "work_email"
	}
	values = map[string]string{}
	for _, key := range append(
		TokenKeys,
		[2]string{email, "JIRA_EMAIL"},
		[2]string{"jira_url", "JIRA_BASE_URL"},
		[2]string{"gitlab_url", "GITLAB_URL"},
	) {
		value, present := data[key[0]]
		if !present {
			continue
		}
		text, ok := value.(string)
		if !ok || strings.ContainsFunc(text, unicode.IsControl) {
			return nil, key[0]
		}
		values[key[1]] = text
	}
	return values, ""
}

// WorkEnv is work.env holding every [WorkEnvNames] name, in order, with
// values, and the others empty.
func WorkEnv(values map[string]string) []byte {
	var b strings.Builder
	for _, name := range WorkEnvNames {
		b.WriteString(name + "=" + values[name] + "\n")
	}
	return []byte(b.String())
}

// EnvAssignments are the NAME=value lines of an env file; a later line for a
// name wins.
func EnvAssignments(data []byte) map[string]string {
	assigned := map[string]string{}
	for line := range strings.Lines(string(data)) {
		if name, value, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "="); ok {
			assigned[name] = value
		}
	}
	return assigned
}

// envName is a name an env file may set, and envReserved the names it may
// not: the ones the shell hooks run through, which the hook would unset
// outside the folder. That is PATH and HOME, zsh's path (tied to PATH) and
// PWD (the zsh hook's folder), bash's PROMPT_COMMAND and zsh's
// precmd_functions and chpwd_functions (what runs the hook), the regex match
// results the hooks' tests overwrite (bash's BASH_REMATCH, zsh's MATCH,
// MBEGIN, MEND, match, mbegin and mend), and the hooks' own variables. Case counts: Path or http_proxy loads.
var (
	envName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envReserved = regexp.MustCompile(
		`^(PATH|path|HOME|PWD|PROMPT_COMMAND|precmd_functions|chpwd_functions|` +
			`BASH_REMATCH|MATCH|MBEGIN|MEND|match|mbegin|mend|__workbench_.*|_wb_.*)$`,
	)
)

// ParseEnvFile reads an env file's NAME=value lines with a value, in file
// order: the names to set. Blank lines, # comments and a NAME= line with an
// empty value are skipped, so a name the owner has not filled in never hides
// a tool's own setting. bad holds every other line, by number from 1 and why
// it is skipped: one that is not NAME=value with a valid name that is not
// reserved, and a value that is unquoted, has no space or tab at either end
// and no carriage return at its end. The file is never sourced, so `export
// NAME=value`, a quoted or padded value or a Windows line ending would load
// wrong, and a reserved name would break the shell or the hook. A value is
// everything after the first =, as written, and never part of a reason. The
// shell hooks in home/private_dot_bashrc.tmpl and home/dot_zshrc.tmpl read the
// same way.
func ParseEnvFile(data []byte) (pairs [][2]string, bad []BadEnvLine) {
	number := 0
	for line := range strings.Lines(string(data)) {
		number++
		line = strings.TrimSuffix(line, "\n")
		if trimmed := strings.TrimSpace(line); trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if reason := badEnvReason(name, value, ok); reason != "" {
			bad = append(bad, BadEnvLine{number, reason})
			continue
		}
		if value != "" {
			pairs = append(pairs, [2]string{name, value})
		}
	}
	return pairs, bad
}

// BadEnvLine is an env file line ParseEnvFile skips: its number, from 1, and
// why, in words that never hold its value.
type BadEnvLine struct {
	Number int
	Reason string
}

// badEnvReason is why a NAME=value line split at its first = (ok false when
// it has none) is skipped, or "" when it loads.
func badEnvReason(name, value string, ok bool) string {
	switch {
	case !ok || !envName.MatchString(name):
		return "is not NAME=value"
	case envReserved.MatchString(name):
		return "sets " + name + ", which the shell hook needs"
	case strings.HasPrefix(value, `"`) || strings.HasPrefix(value, "'"):
		return "has a quoted value; write it without quotes"
	case strings.HasSuffix(value, "\r"):
		return "ends in a Windows line ending"
	case strings.Trim(value, " \t") != value:
		return "has a space or tab at the start or end of its value"
	}
	return ""
}
