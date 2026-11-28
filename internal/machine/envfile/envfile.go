// Package envfile is the format of the roles' env files, <role>.env in
// Workbench's config folder: the names work.env holds, where each file is,
// work.env as Workbench writes it from the machine answers, the strict
// reading the shell hooks and the dispatch servers' gate share, and the plain
// read-back migration 0005 compares tokens with. It imports nothing of
// Workbench's, so internal/machine, internal/migrate and internal/cli all read
// the format here.
package envfile

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// WorkNames are the names work.env holds, in order.
var WorkNames = []string{
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

// TokenAnswers are the retired machine answers that hold the work tokens,
// each with the work.env name its token goes under; migration 0005 moves them
// into work.env.
var TokenAnswers = [][2]string{
	{"jira_api_token", "JIRA_API_TOKEN"},
	{"gitlab_token", "GITLAB_TOKEN"},
}

// Path is the env file of role, <role>.env in Workbench's config folder
// config.
func Path(config, role string) string { return filepath.Join(config, role+".env") }

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
		TokenAnswers,
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

// WorkFile is work.env holding every [WorkNames] name, in order, with
// values, and the others empty.
func WorkFile(values map[string]string) []byte {
	var b strings.Builder
	for _, name := range WorkNames {
		b.WriteString(name + "=" + values[name] + "\n")
	}
	return []byte(b.String())
}

// Assignments are the NAME=value lines of an env file, each value as written:
// no line is checked and a later line for a name wins. Migration 0005 reads
// work.env back with it, so a token [WorkValues] allows that [Parse] skips (a
// quoted or padded one, or one ending in a carriage return) still matches the
// answer it came from, and a run stopped after writing work.env finishes
// instead of stopping in conflict.
func Assignments(data []byte) map[string]string {
	assigned := map[string]string{}
	for line := range strings.Lines(string(data)) {
		if name, value, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "="); ok {
			assigned[name] = value
		}
	}
	return assigned
}

// namePattern is a name an env file may set, and reservedPattern the names it may
// not: the ones the shell hooks run through, which the hook would unset
// outside the folder. That is PATH and HOME, zsh's path (tied to PATH) and
// PWD (the zsh hook's folder), bash's PROMPT_COMMAND and zsh's
// precmd_functions and chpwd_functions (what runs the hook), the regex match
// results the hooks' tests overwrite (bash's BASH_REMATCH, zsh's MATCH,
// MBEGIN, MEND, match, mbegin and mend), and the hooks' own variables. Case
// counts: Path or http_proxy loads.
var (
	namePattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	reservedPattern = regexp.MustCompile(
		`^(PATH|path|HOME|PWD|PROMPT_COMMAND|precmd_functions|chpwd_functions|` +
			`BASH_REMATCH|MATCH|MBEGIN|MEND|match|mbegin|mend|__workbench_.*|_wb_.*)$`,
	)
)

// Parse reads an env file's NAME=value lines with a value, in file
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
func Parse(data []byte) (pairs [][2]string, bad []BadLine) {
	number := 0
	for line := range strings.Lines(string(data)) {
		number++
		line = strings.TrimSuffix(line, "\n")
		if trimmed := strings.TrimSpace(line); trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if reason := badReason(name, value, ok); reason != "" {
			bad = append(bad, BadLine{number, reason})
			continue
		}
		if value != "" {
			pairs = append(pairs, [2]string{name, value})
		}
	}
	return pairs, bad
}

// BadLine is an env file line [Parse] skips: its number, from 1, and why, in
// words that never hold its value.
type BadLine struct {
	Number int
	Reason string
}

// badReason is why a NAME=value line split at its first = (ok false when
// it has none) is skipped, or "" when it loads.
func badReason(name, value string, ok bool) string {
	switch {
	case !ok || !namePattern.MatchString(name):
		return "is not NAME=value"
	case reservedPattern.MatchString(name):
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
