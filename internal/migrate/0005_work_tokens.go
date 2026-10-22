package migrate

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"

	"github.com/Sawmonabo/workbench/internal/operation"
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

// The Jira and GitLab addresses the Codex settings rendered for the dispatch
// servers before they read work.env.
const (
	jiraBaseURL = "https://fortressinfosec.atlassian.net"
	gitlabURL   = "https://gitlab.com/fortressinfosec"
)

// tokenKeys are the retired machine answers 0005 moves, each with the
// work.env name it moves to.
var tokenKeys = [][2]string{{"jira_api_token", "JIRA_API_TOKEN"}, {"gitlab_token", "GITLAB_TOKEN"}}

// workTokens moves the work tokens earlier questionnaires saved as machine
// answers into work.env, which the work tools read by name. It writes
// work.env only where none exists, as a new file, and deletes the two answers
// only once work.env holds the same tokens, so a run stopped between the two
// writes loses nothing and the next run only deletes them. A work.env that
// does not hold those same tokens is the owner's, and both files stay as they
// are. It reads machine.toml itself: this package can't import
// internal/machine.
var workTokens = Migration{
	ID:    "0005-work-tokens",
	Title: "Move the saved work tokens into work.env",
	Doing: "moving the saved work tokens into work.env",
	What: "Creates work.env (mode 600) in Workbench's config folder with the Jira and " +
		"GitLab tokens saved in machine.toml beside it, the work email as JIRA_EMAIL, the " +
		"Jira and GitLab addresses the Codex settings held, and the other work names empty, " +
		"then deletes jira_api_token and gitlab_token from machine.toml. When work.env " +
		"already holds the same tokens it only deletes them. A work.env that does not hold " +
		"those same tokens, or a token that isn't one line of text, leaves both files as " +
		"they are. Without the work role it changes nothing.",
	Recovery: "nothing is lost: work.env holds the tokens, so copying them back into " +
		"machine.toml by hand restores the old answers",
	Check: func(l Locations) (Status, string) {
		found := findWorkTokens(l)
		return found.status, found.text
	},
	Run: func(_ context.Context, l Locations) ([]string, error) {
		operation.RemoveStaleWrites(l.Runtime.Config)
		found := findWorkTokens(l)
		switch found.status {
		case Done:
			return nil, nil
		case Pending:
			if found.err != nil {
				return nil, found.err
			}
			if err := operation.CreatePrivateFile(
				workEnvPath(l),
				workEnv(found.values),
			); err != nil {
				return nil, err
			}
		case Unfinished:
		default:
			return nil, operation.Fail(operation.ExitConflict, "conflict", found.text)
		}
		for _, key := range tokenKeys {
			delete(found.data, key[0])
		}
		encoded, err := toml.Marshal(found.config)
		if err != nil {
			return nil, err
		}
		return nil, operation.ReplacePrivateFile(machinePath(l), encoded)
	},
}

func machinePath(l Locations) string { return filepath.Join(l.Runtime.Config, "machine.toml") }

func workEnvPath(l Locations) string { return filepath.Join(l.Runtime.Config, "work.env") }

// tokensFound is where 0005 stands: its status and text, the whole of
// machine.toml as read with its [data] table, and the work.env values to
// write.
type tokensFound struct {
	status Status
	text   string
	err    error
	config map[string]any
	data   map[string]any
	values map[string]string
}

// findWorkTokens reads machine.toml and work.env and says where 0005 stands.
// Its text names files and keys, never a value.
func findWorkTokens(l Locations) tokensFound {
	machine := operation.ShowPath(l.Home, machinePath(l))
	env := operation.ShowPath(l.Home, workEnvPath(l))
	config, err := readMachine(machinePath(l))
	if err != nil {
		return tokensFound{status: Pending, text: "can't read " + machine, err: err}
	}
	data, _ := config["data"].(map[string]any)
	var keys []string
	for _, key := range tokenKeys {
		if _, ok := data[key[0]]; ok {
			keys = append(keys, key[0])
		}
	}
	if len(keys) == 0 {
		return tokensFound{status: Done}
	}
	named := strings.Join(keys, " and ")
	if role := data["machine_role"]; role != "work" && role != "both" {
		return tokensFound{
			status: Done,
			text:   machine + " holds " + named + " without the work role: delete them",
		}
	}
	byHand := ", so copy " + named + " from " + machine + " into " + env +
		" by hand and delete them from " + machine
	values, bad := workValues(data)
	if bad != "" {
		return tokensFound{
			status: Conflict,
			text:   bad + " in " + machine + " isn't one line of text" + byHand,
		}
	}
	found := tokensFound{config: config, data: data, values: values}
	if _, err = os.Lstat(workEnvPath(l)); errors.Is(err, fs.ErrNotExist) {
		found.status, found.text = Pending, "moves "+named+" from "+machine+" into a new "+env
		return found
	}
	held, err := operation.ReadPrivateInput(workEnvPath(l), 1<<20)
	if err != nil {
		return tokensFound{status: Conflict, text: "can't read " + env + byHand}
	}
	saved := envAssignments(held)
	for _, key := range tokenKeys {
		if _, ok := data[key[0]]; ok && saved[key[1]] != values[key[1]] {
			return tokensFound{status: Conflict, text: env + " already holds other values" + byHand}
		}
	}
	found.status, found.text = Unfinished, "deletes "+named+" from "+machine+", which "+env+" already holds"
	return found
}

// readMachine decodes machine.toml at path; a missing file is an empty one.
func readMachine(path string) (map[string]any, error) {
	config := map[string]any{}
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return config, nil
	}
	raw, err := operation.ReadPrivateInput(path, 1<<20)
	if err != nil {
		return nil, err
	}
	return config, toml.Unmarshal(raw, &config)
}

// workValues are the work.env values the answers in data give: the tokens
// they hold, the work email (work_email on both roles) as JIRA_EMAIL and the
// two addresses. bad is the first answer that isn't one line of text.
func workValues(data map[string]any) (values map[string]string, bad string) {
	email := "email"
	if data["machine_role"] == "both" {
		email = "work_email"
	}
	values = map[string]string{"JIRA_BASE_URL": jiraBaseURL, "GITLAB_URL": gitlabURL}
	for _, key := range append(tokenKeys, [2]string{email, "JIRA_EMAIL"}) {
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

// workEnv is work.env holding every [WorkEnvNames] name, in order, with
// values, and the others empty.
func workEnv(values map[string]string) []byte {
	var b strings.Builder
	for _, name := range WorkEnvNames {
		b.WriteString(name + "=" + values[name] + "\n")
	}
	return []byte(b.String())
}

// envAssignments are the NAME=value lines of an env file; a later line for a
// name wins.
func envAssignments(data []byte) map[string]string {
	assigned := map[string]string{}
	for line := range strings.Lines(string(data)) {
		if name, value, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "="); ok {
			assigned[name] = value
		}
	}
	return assigned
}
