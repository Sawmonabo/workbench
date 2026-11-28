package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/machine/envfile"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// WorkTokens moves the work tokens earlier questionnaires saved as machine
// answers into work.env, which the work tools read by name. It writes
// work.env only where none exists, as a new file, and deletes the two answers
// only once work.env holds the same tokens, so a run stopped between the two
// writes loses nothing and the next run only deletes them. A work.env that
// does not hold those same tokens is the owner's, and both files stay as they
// are. It reads machine.toml itself: this package can't import
// internal/machine.
var WorkTokens = Migration{
	ID:    "0005-work-tokens",
	Title: "Move the saved work tokens into work.env",
	Doing: "moving the saved work tokens into work.env",
	What: "Creates work.env (mode 600) in Workbench's config folder with the Jira and " +
		"GitLab tokens saved in machine.toml beside it, the work email as JIRA_EMAIL, the " +
		"Jira and GitLab addresses from the jira_url and gitlab_url answers, and the other " +
		"work names empty, then deletes jira_api_token and gitlab_token from machine.toml. When work.env " +
		"already holds the same tokens it only deletes them. A work.env that does not hold " +
		"those same tokens, or a token that isn't one line of text, leaves both files as " +
		"they are. Without the work role it changes nothing.",
	Recovery: "nothing is lost: work.env holds the tokens, so copying them back into " +
		"machine.toml by hand restores the old answers",
	Check: func(l Locations) (Status, string, error) {
		found := findWorkTokens(l)
		if found.status == Pending && found.err != nil {
			return Pending, "", found.err
		}
		return found.status, found.text, nil
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
				envfile.Path(l.Runtime.Config, "work"),
				envfile.WorkFile(found.values),
			); err != nil {
				return nil, err
			}
		case Unfinished:
		default:
			return nil, operation.Fail(operation.ExitConflict, "conflict", found.text)
		}
		for _, key := range envfile.TokenAnswers {
			delete(found.data, key[0])
		}
		encoded, err := toml.Marshal(found.config)
		if err != nil {
			return nil, err
		}
		return nil, operation.ReplacePrivateFile(l.Runtime.MachineConfig(), encoded)
	},
}

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
	machine := display.PathFrom(l.Home, l.Runtime.MachineConfig())
	env := display.PathFrom(l.Home, envfile.Path(l.Runtime.Config, "work"))
	config, err := readMachine(l)
	if err != nil {
		return tokensFound{status: Pending, text: err.Error(), err: err}
	}
	data, _ := config["data"].(map[string]any)
	var keys []string
	for _, key := range envfile.TokenAnswers {
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
	values, bad := envfile.WorkValues(data)
	if bad != "" {
		return tokensFound{
			status: Conflict,
			text:   bad + " in " + machine + " isn't one line of text" + byHand,
		}
	}
	found := tokensFound{config: config, data: data, values: values}
	held, there, err := operation.ReadPrivateRecord(
		envfile.Path(l.Runtime.Config, "work"),
		1<<20,
		"work.env",
	)
	if !there {
		found.status, found.text = Pending, "moves "+named+" from "+machine+" into a new "+env
		return found
	}
	if err != nil {
		// The read's refusal names work.env and what is wrong with it, never
		// what it holds; once work.env can be read, the next run goes on.
		return tokensFound{status: Conflict, text: err.Error()}
	}
	saved := envfile.Assignments(held)
	for _, key := range envfile.TokenAnswers {
		if _, ok := data[key[0]]; ok && saved[key[1]] != values[key[1]] {
			return tokensFound{status: Conflict, text: env + " already holds other values" + byHand}
		}
	}
	found.status, found.text = Unfinished, "deletes "+named+" from "+machine+", which "+env+" already holds"
	return found
}

// readMachine decodes machine.toml; a missing file is an empty one. Its error
// never quotes the file, which holds the work tokens until 0005 has moved
// them: a refused read names the file and what is wrong with it, and a file
// that does not decode is named with the line and column, since the decoder's
// own words can quote what the file holds.
func readMachine(l Locations) (map[string]any, error) {
	config := map[string]any{}
	path := l.Runtime.MachineConfig()
	raw, found, err := operation.ReadPrivateRecord(
		path,
		operation.MachineConfigLimit,
		"the saved answers and choices",
	)
	if err != nil {
		return nil, err
	}
	if !found {
		return config, nil
	}
	if err = toml.Unmarshal(raw, &config); err != nil {
		shown := display.PathFrom(l.Home, path) + " isn't valid TOML"
		if decode, ok := errors.AsType[*toml.DecodeError](err); ok {
			row, column := decode.Position()
			shown += fmt.Sprintf(" (line %d, column %d)", row, column)
		}
		return nil, errors.New(shown)
	}
	return config, nil
}
