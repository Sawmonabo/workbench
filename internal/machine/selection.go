package machine

import (
	"slices"

	"github.com/Sawmonabo/workbench/internal/migrate"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/pelletier/go-toml/v2"
)

// Selection is which effects an apply runs: every default effect not named in
// Skip, and the optional host effects named in Select. An approved apply saves
// it in machine.toml's [effects] table, so a machine that never wants the
// Windows-side steps declines them once. Decided names every effect the plan
// view listed at the last approved apply, so a later apply asks only about an
// effect that is not in it. A nil Decided means the owner has never decided
// anything: no step is marked new then, and the first apply asks once. An empty
// non-nil Decided is a saved list that happens to name nothing.
type Selection struct {
	Skip    []string `toml:"skip,omitempty"`
	Select  []string `toml:"select,omitempty"`
	Decided []string `toml:"decided,omitempty"`
	// Forget is set by --reset: what the saved decided list says is ignored when
	// the apply records the new one. It is never saved.
	Forget bool `toml:"-"`
}

// NeverDecided reports that no approved apply has saved a decided list yet.
func (s Selection) NeverDecided() bool { return s.Decided == nil }

func (s Selection) empty() bool {
	return len(s.Skip) == 0 && len(s.Select) == 0 && s.Decided == nil
}

// equal reports whether two sorted selections name the same effects.
func (s Selection) equal(other Selection) bool {
	return slices.Equal(s.Skip, other.Skip) && slices.Equal(s.Select, other.Select) &&
		slices.Equal(s.Decided, other.Decided) && s.NeverDecided() == other.NeverDecided()
}

// table is the [effects] table as saved. An empty decided list is written
// (`decided = []`) so it reads back as saved, not as never decided, which
// struct omitempty would lose.
func (s Selection) table() map[string]any {
	table := map[string]any{}
	if len(s.Skip) > 0 {
		table["skip"] = s.Skip
	}
	if len(s.Select) > 0 {
		table["select"] = s.Select
	}
	if s.Decided != nil {
		table["decided"] = s.Decided
	}
	return table
}

// ReadSelection reads the [effects] table of config. A missing file or table
// is an empty selection: everything checked, nothing optional selected.
func ReadSelection(config string) (Selection, error) {
	raw, found, err := lookupMachineConfig(config)
	if !found || err != nil {
		return Selection{}, err
	}
	return readSelection(raw)
}

func readSelection(raw []byte) (Selection, error) {
	var config struct {
		Effects Selection `toml:"effects"`
	}
	if err := toml.Unmarshal(raw, &config); err != nil {
		return Selection{}, operation.Fail(
			operation.ExitInvalid,
			"answers",
			"Machine answers are not valid TOML",
			operation.Kept("the original input"),
		)
	}
	// Until migration 0006 has renamed them in the file, the saved choices
	// read under the names the steps have, so a step turned off stays off.
	config.Effects.Skip = migrate.RenameSteps(config.Effects.Skip)
	config.Effects.Select = migrate.RenameSteps(config.Effects.Select)
	config.Effects.Decided = migrate.RenameSteps(config.Effects.Decided)
	slices.Sort(config.Effects.Skip)
	slices.Sort(config.Effects.Select)
	slices.Sort(config.Effects.Decided)
	return config.Effects, nil
}

// savedChoices names machine.toml where a read of it is refused.
const savedChoices = "the saved answers and choices"

// ReadMachineConfig reads the machine.toml at path (see
// [operation.Paths.MachineConfig]) as [operation.ReadPrivateInput] reads a
// private input of at most 1 MiB: a missing file is an error.
func ReadMachineConfig(path string) ([]byte, error) {
	return operation.ReadPrivateInput(path, operation.MachineConfigLimit, savedChoices)
}

// lookupMachineConfig is [ReadMachineConfig] for a machine.toml that may not
// be saved yet: found is false, with no data and no error, when nothing is at
// path.
func lookupMachineConfig(path string) (raw []byte, found bool, err error) {
	return operation.ReadPrivateRecord(path, operation.MachineConfigLimit, savedChoices)
}

// encodeMachineConfig is the one writer of machine.toml's shape: the [data]
// answers and, when any, the [effects] selection.
func encodeMachineConfig(answers Answers, selection Selection) ([]byte, error) {
	config := map[string]any{"data": answers}
	if !selection.empty() {
		config["effects"] = selection.table()
	}
	return toml.Marshal(config)
}

// writeSelection saves selection beside the answers already in config.
func writeSelection(m *operation.Mutation, config string, selection Selection) error {
	raw, err := ReadMachineConfig(config)
	if err != nil {
		return err
	}
	answers, err := readAnswers(raw)
	if err != nil {
		return err
	}
	encoded, err := encodeMachineConfig(answers, selection)
	if err != nil {
		return err
	}
	return m.WritePrivate(config, encoded)
}
