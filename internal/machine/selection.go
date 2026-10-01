package machine

import (
	"os"
	"slices"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/pelletier/go-toml/v2"
)

// Selection is which effects an apply runs: every default effect not named in
// Skip, and the optional host effects named in Select. An approved apply saves
// it in machine.toml's [effects] table, so a machine that never wants the
// Windows-side steps declines them once.
type Selection struct {
	Skip   []string `toml:"skip,omitempty"`
	Select []string `toml:"select,omitempty"`
}

func (s Selection) empty() bool { return len(s.Skip) == 0 && len(s.Select) == 0 }

// ReadSelection reads the [effects] table of config. A missing file or table
// is an empty selection: everything checked, nothing optional selected.
func ReadSelection(config string) (Selection, error) {
	if _, err := os.Lstat(config); os.IsNotExist(err) {
		return Selection{}, nil
	}
	raw, err := operation.ReadPrivateInput(config, 1<<20)
	if err != nil {
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
			"Machine answers are not valid TOML; original input retained",
		)
	}
	slices.Sort(config.Effects.Skip)
	slices.Sort(config.Effects.Select)
	return config.Effects, nil
}

// encodeMachineConfig is the one writer of machine.toml's shape: the [data]
// answers and, when any, the [effects] selection.
func encodeMachineConfig(answers Answers, selection Selection) ([]byte, error) {
	config := map[string]any{"data": answers}
	if !selection.empty() {
		config["effects"] = selection
	}
	return toml.Marshal(config)
}

// WriteSelection saves selection beside the answers already in config.
func WriteSelection(m *operation.Mutation, config string, selection Selection) error {
	raw, err := operation.ReadPrivateInput(config, 1<<20)
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
