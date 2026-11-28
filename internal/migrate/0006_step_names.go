package migrate

import (
	"context"
	"slices"

	"github.com/pelletier/go-toml/v2"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// renamedSteps are the step names 0006 renames in the saved choices, each with
// the name the step has.
var renamedSteps = [][2]string{{"costs-ingest", "agent-costs-ingest"}}

// choiceLists are the lists of the [effects] table that hold step names.
var choiceLists = []string{"skip", "select", "decided"}

// stepNames renames the saved choices of machine.toml's [effects] table to
// the names the steps have, so a step the owner turned off stays off. Until
// it has run, [RenameSteps] gives every reader the same names without
// writing.
var stepNames = Migration{
	ID:    "0006-step-names",
	Title: "Rename the saved choice for Record AI usage",
	Doing: "renaming the saved choice for Record AI usage",
	What: "Renames costs-ingest to agent-costs-ingest in the skip, select and decided lists " +
		"of the [effects] table in machine.toml, so the choice saved for the Record AI usage " +
		"step still applies to it. A list that already names agent-costs-ingest only loses " +
		"costs-ingest. Nothing else in the file changes.",
	Recovery: "nothing is lost: the choice is the same one under the step's name",
	Check: func(l Locations) (Status, string) {
		config, err := readMachine(l)
		if err != nil {
			return Pending, err.Error()
		}
		if !renameChoices(config) {
			return Done, ""
		}
		return Pending, "renames costs-ingest to agent-costs-ingest in the saved choices of " +
			display.PathFrom(l.Home, l.Runtime.MachineConfig())
	},
	Run: func(_ context.Context, l Locations) ([]string, error) {
		operation.RemoveStaleWrites(l.Runtime.Config)
		config, err := readMachine(l)
		if err != nil {
			return nil, err
		}
		if !renameChoices(config) {
			return nil, nil
		}
		encoded, err := toml.Marshal(config)
		if err != nil {
			return nil, err
		}
		return nil, operation.ReplacePrivateFile(l.Runtime.MachineConfig(), encoded)
	},
}

// renameChoices renames the step names in config's [effects] lists in place
// and reports whether any list changed.
func renameChoices(config map[string]any) bool {
	effects, _ := config["effects"].(map[string]any)
	changed := false
	for _, list := range choiceLists {
		values, ok := effects[list].([]any)
		if !ok {
			continue
		}
		names := make([]string, 0, len(values))
		for _, value := range values {
			name, isText := value.(string)
			if !isText {
				names = nil
				break
			}
			names = append(names, name)
		}
		if renamed := RenameSteps(names); !slices.Equal(renamed, names) {
			effects[list], changed = renamed, true
		}
	}
	return changed
}

// RenameSteps gives names with each name 0006 renames replaced by the name
// the step has, once; names that need no change come back as they are, so a
// nil list stays nil.
func RenameSteps(names []string) []string {
	if !slices.ContainsFunc(names, func(name string) bool {
		return slices.ContainsFunc(
			renamedSteps,
			func(pair [2]string) bool { return pair[0] == name },
		)
	}) {
		return names
	}
	renamed := make([]string, 0, len(names))
	for _, name := range names {
		for _, pair := range renamedSteps {
			if name == pair[0] {
				name = pair[1]
			}
		}
		if !slices.Contains(renamed, name) {
			renamed = append(renamed, name)
		}
	}
	return renamed
}
