package machine

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/pelletier/go-toml/v2"
)

// selectPrivateContext is an internal verified application-context selector.
// The public developer --source resolver never grants this runtime exception.
func selectPrivateContext(c operation.Context, source string) (operation.Context, error) {
	path, err := operation.ExistingDirectory(source)
	if err != nil {
		return c, err
	}
	root := filepath.Join(c.Paths.Data, "application-contexts")
	if !operation.Within(root, path) || path == root {
		return c, operation.Fail(
			operation.ExitInvalid,
			"source",
			"Native setup requires a private application context beneath Workbench data",
		)
	}
	for current := path; operation.Within(c.Paths.Data, current); current = filepath.Dir(current) {
		info, statErr := os.Lstat(current)
		if statErr != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
			return c, operation.Fail(
				operation.ExitInvalid,
				"permissions",
				"Application contexts and their parents must be private directories",
			)
		}
	}
	if _, _, err = SourceSnapshot(path, false); err != nil {
		return c, err
	}
	c.Native.Source = path
	return c, nil
}

// initialize reuses the native questionnaire and built-in Git in a verified
// private context. Acquiring/copying the verified payload belongs to setup.
// Approval here saves answers only; it does not approve configuration apply.
func initialize(
	ctx context.Context,
	c operation.Context,
	m *operation.Mutation,
	terminal *os.File,
	ask []string,
) (Answers, error) {
	if err := m.Check(); err != nil {
		return nil, err
	}
	var err error
	c, err = selectPrivateContext(c, c.Native.Source)
	if err != nil {
		return nil, err
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return nil, err
	}
	var recorded []operation.Dependency
	if state != nil {
		recorded = state.Dependencies
	}
	requirements, err := ManagementRequirements(c)
	if err != nil {
		return nil, err
	}
	dependencies, _ := ResolveDependencies(ctx, c, requirements, recorded, os.Getenv("PATH"))
	if dependency(dependencies, "chezmoi") == "" || dependency(dependencies, "python3") == "" {
		return nil, operation.Fail(
			operation.ExitBlocked,
			"prerequisites",
			"Native setup requires qualified chezmoi and Python before initialization",
		)
	}
	seed, err := initSeed(c.Native.Config, terminal, ask)
	if err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "workbench-init-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	scratch, err = filepath.EvalSymlinks(scratch)
	if err != nil {
		return nil, err
	}
	native := c.Native
	native.Config = filepath.Join(scratch, "seed.toml")
	native.PersistentState = filepath.Join(scratch, "state.boltdb")
	native.Cache = filepath.Join(scratch, "cache")
	if err = os.WriteFile(native.Config, seed, 0o600); err != nil {
		return nil, err
	}
	generated := filepath.Join(scratch, "generated.toml")
	args, err := native.Args()
	if err != nil {
		return nil, err
	}
	args = append(args, "--use-builtin-git=true")
	if terminal == nil {
		args = append(args, "--no-tty")
	}
	args = append(args, "init", "--config-path", generated)
	environment := scriptEnvironment(c, dependencies)
	_, err = operation.Run(
		ctx,
		c,
		m,
		operation.Process{
			Executable:  dependency(dependencies, "chezmoi"),
			Args:        args,
			Directory:   scratch,
			Environment: environment,
			Mutates:     true,
			Terminal:    terminal,
			Timeout:     30 * time.Minute,
		},
	)
	if err != nil {
		return nil, err
	}
	answers, err := generatedAnswers(generated)
	if err != nil {
		return nil, err
	}
	if _, _, err = SourceSnapshot(c.Native.Source, false); err != nil {
		return nil, err
	}
	if err = saveAnswers(m, c, answers); err != nil {
		return nil, err
	}
	return answers, nil
}

// saveAnswers writes answers to machine.toml beside any saved selection.
func saveAnswers(m *operation.Mutation, c operation.Context, answers Answers) error {
	config := filepath.Join(c.Paths.Config, "machine.toml")
	previous, err := ReadSelection(config)
	if err != nil {
		return err
	}
	encoded, err := encodeMachineConfig(answers, previous)
	if err != nil {
		return err
	}
	return m.WritePrivate(config, encoded)
}

// initSeed returns the saved answers to seed native init, complete or not, or
// an empty [data] table when a terminal can answer the questionnaire. Every
// question asks once, so init asks only what the seed lacks, and the answers
// named in ask are left out to be asked again.
func initSeed(config string, terminal *os.File, ask []string) ([]byte, error) {
	_, err := os.Lstat(config)
	if os.IsNotExist(err) {
		if terminal == nil {
			return nil, operation.Fail(
				operation.ExitBlocked,
				"answers",
				"Unattended native setup requires complete private answers",
			)
		}
		return []byte("[data]\n"), nil
	}
	if err != nil {
		return nil, err
	}
	seed, err := operation.ReadPrivateInput(config, 1<<20)
	if err != nil {
		return nil, err
	}
	answers, err := readAnswers(seed)
	if err != nil || len(ask) == 0 {
		return seed, err
	}
	for _, key := range ask {
		delete(answers, key)
	}
	return toml.Marshal(map[string]any{"data": answers})
}

// generatedAnswers reads the config native init wrote, allowing only [data]
// and sourceDir, and validates the answers.
func generatedAnswers(path string) (Answers, error) {
	// Esc or ctrl+c at a question makes native init exit successfully
	// without writing its configuration.
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, operation.Fail(
			operation.ExitBlocked,
			"answers",
			"The machine questions were cancelled, so no answers were saved; run workbench apply to answer them",
		)
	}
	output, err := operation.ReadPrivateInput(path, 1<<20)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if toml.Unmarshal(output, &document) != nil {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"answers",
			"Native init produced invalid configuration",
		)
	}
	for key := range document {
		if key != "data" && key != "sourceDir" {
			return nil, operation.Fail(
				operation.ExitInvalid,
				"answers",
				"Native init produced unexpected configuration controls",
			)
		}
	}
	data, ok := document["data"].(map[string]any)
	if !ok {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"answers",
			"Native init did not produce machine answers",
		)
	}
	answers := Answers(data)
	return answers, validateAnswers(answers)
}
