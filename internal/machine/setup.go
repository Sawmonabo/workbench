package machine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
)

// SetupPlan is offline. It lists the tools setup would install and the native
// questionnaire; neither approves target configuration or provisioning.
func SetupPlan(ctx context.Context, c operation.Context) (operation.Plan, error) {
	return setupPlan(ctx, c, true)
}

// ToolsPlan is [SetupPlan] without the questionnaire: only the pinned tools
// Workbench runs that are missing.
func ToolsPlan(ctx context.Context, c operation.Context) (operation.Plan, error) {
	return setupPlan(ctx, c, false)
}

func setupPlan(
	ctx context.Context,
	c operation.Context,
	questions bool,
) (_ operation.Plan, err error) {
	defer operation.Annotate(&err, "plan management setup")
	plan := operation.Plan{
		Scope:    c.Scope,
		Complete: true,
		RecoveryLimits: []string{
			"Revert does not undo installing the tools or saving the answers",
			"Tools Workbench found installed stay as they are",
		},
	}
	files, identity, err := SourceSnapshot(c.Native.Source, c.Native.Developer)
	plan.Source = identity
	if err != nil {
		return plan, err
	}
	requirements, err := SourceRequirements(files)
	if err != nil {
		return plan, err
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return plan, err
	}
	var recorded []operation.Dependency
	if state != nil {
		recorded = state.Dependencies
	}
	plan.Dependencies, _ = ResolveDependencies(ctx, c, requirements, recorded, os.Getenv("PATH"))
	for _, name := range []string{"chezmoi", "uv", "python3"} {
		if dependency(plan.Dependencies, name) == "" {
			plan.Effects = append(
				plan.Effects,
				operation.Effect{
					Name:        toolEffect + name,
					Description: "Acquire a pinned private management dependency; existing unqualified installations are retained",
					Privilege:   "user",
					Recovery:    "Private installation retained; no global tool replacement",
				},
			)
		}
	}
	if _, err = TomlkitPath(c, requirements); err != nil {
		plan.Effects = append(
			plan.Effects,
			operation.Effect{
				Name:        toolEffect + "tomlkit",
				Description: "Acquire the pinned, SHA-256 verified TOML Kit wheel in Workbench's private library directory",
				Privilege:   "user",
				Recovery:    "Private library retained",
			},
		)
	}
	if !questions {
		return plan, nil
	}
	plan.Effects = append(
		plan.Effects,
		operation.Effect{
			Name:        "native-initialization",
			Description: "Copy verified source into a private application context and run the canonical native questionnaire; save private answers only",
			Privilege:   "user",
			Recovery:    "Previously saved answers are retained until successful validation",
		},
	)
	raw, found, err := lookupMachineConfig(c.Native.Config)
	if !found || err != nil {
		return plan, err
	}
	answers, err := readAnswers(raw)
	if err != nil {
		return plan, err
	}
	input := operation.Input{Name: "answers", Digest: operation.SHA256Hex(raw)}
	if validateAnswers(c, answers) != nil {
		// The questionnaire asks for what this release needs and they lack.
		input.Name = "incomplete-answers"
	}
	plan.Inputs = append(plan.Inputs, input)
	return plan, nil
}

// keepIdentical accepts the regular file at target when it holds exactly
// data, and gives it mode when its permission or special bits are others: the
// bytes are the ones Workbench installs, so the mode it installs them with is
// theirs, and a tool whose mode lets other users change it, which Workbench
// does not run, runs again after an update. The file is read and changed
// through one descriptor, so the file compared is the file changed. Any other
// content is a conflict and is retained.
func keepIdentical(target string, data []byte, mode os.FileMode) error {
	differs := operation.Fail(
		operation.ExitConflict,
		"dependency_conflict",
		display.Path(target)+" differs from the tool Workbench installs",
		operation.Kept("the file as it is"),
	)
	file, err := operation.OpenRegularFile(target)
	if err != nil {
		return differs
	}
	defer func() { _ = file.Close() }()
	existing, err := io.ReadAll(io.LimitReader(file, int64(len(data))+1))
	if err != nil || !bytes.Equal(existing, data) {
		return differs
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	const bits = fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky
	if info.Mode()&bits == mode&bits {
		return nil
	}
	return file.Chmod(mode)
}

// writeNewOrIdentical installs data at target, or accepts a target that already
// holds exactly data, giving it mode when it has another (see keepIdentical);
// any other existing target is a conflict and is retained.
//
// New content is written and flushed to a temporary file beside target, then
// linked into place. A crash or a full disk therefore leaves at most the
// temporary file, never a truncated target that every later setup would report
// as a different tool. The link fails if target appeared meanwhile, where a
// rename would replace it. The temporary file is disposable: each call removes
// a leftover one first, so an interrupted attempt never blocks the next.
func writeNewOrIdentical(target string, data []byte, mode os.FileMode) error {
	partial := partialName(target)
	if info, err := os.Lstat(partial); err == nil {
		if !info.Mode().IsRegular() {
			return operation.Fail(
				operation.ExitConflict,
				"dependency_conflict",
				"Unexpected "+display.Path(partial)+" beside a tool Workbench installs",
				operation.Kept("the entry as it is"),
			)
		}
		if err = os.Remove(partial); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return operation.Fail(
				operation.ExitConflict,
				"dependency_conflict",
				"Existing private tool has unsupported file type",
			)
		}
		return keepIdentical(target, data, mode)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Link(partial, target)
	}
	// Whatever happened, the temporary name goes. After a good link the data
	// stays under target; a removal that fails there only leaves a file the
	// next call deletes.
	_ = os.Remove(partial)
	if errors.Is(err, fs.ErrExist) {
		return operation.Fail(
			operation.ExitConflict,
			"dependency_conflict",
			display.Path(target)+" appeared while Workbench installed it",
			operation.Kept("the file that appeared"),
		)
	}
	if err != nil {
		return err
	}
	return operation.SyncDirectory(filepath.Dir(target))
}

// partialName is the temporary file writeNewOrIdentical fills before linking
// it to target.
func partialName(target string) string {
	return filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".partial")
}

// Setup calls native initialization exactly once, preserving the canonical
// questionnaire. A terminal enables genuine native prompts, never simulations.
// The saved answers named in ask are asked again.
func Setup(
	ctx context.Context,
	c operation.Context,
	m *operation.Mutation,
	terminal *os.File,
	ask []string,
) (_ operation.Context, err error) {
	defer operation.Annotate(&err, "set up management tools")
	// The questionnaire below prompts; InstallTools stops its progress first.
	if err = InstallTools(ctx, c, m); err != nil {
		return c, err
	}
	files, identity, err := SourceSnapshot(c.Native.Source, false)
	if err != nil {
		return c, err
	}
	contextDirectory := filepath.Join(
		c.Paths.Data,
		"application-contexts",
		identity.Release+"-"+identity.ContentDigest,
	)
	if err = operation.PrivateTree(c.Paths.Data, contextDirectory, true); err != nil {
		return c, err
	}
	for name, data := range files {
		target := filepath.Join(contextDirectory, name)
		if err = operation.PrivateTree(c.Paths.Data, filepath.Dir(target), true); err != nil {
			return c, err
		}
		if err = writeNewOrIdentical(target, data, 0o600); err != nil {
			return c, err
		}
	}
	metadata, readErr := operation.ReadRegularFile(
		filepath.Join(c.Native.Source, "release.json"),
		1<<20,
	)
	switch {
	case errors.Is(readErr, fs.ErrNotExist):
	case errors.Is(readErr, operation.ErrNotRegular) || errors.Is(readErr, operation.ErrTooLarge):
		return c, operation.Fail(
			operation.ExitInvalid,
			"release",
			"Invalid native context release metadata",
		)
	case readErr != nil:
		return c, readErr
	default:
		if err = writeNewOrIdentical(
			filepath.Join(contextDirectory, "release.json"),
			metadata,
			0o600,
		); err != nil {
			return c, err
		}
	}
	c, err = selectPrivateContext(c, contextDirectory)
	if err != nil {
		return c, err
	}
	if _, err = askAnswers(ctx, c, m, terminal, ask); err != nil {
		return c, err
	}
	c.Native.Config = c.Paths.MachineConfig()
	return c, nil
}

// StaleSetup lists setup output that no kept release needs as removal edits:
// application contexts of other sources, and private tool versions that no
// kept release pins and state does not record in use.
func StaleSetup(c operation.Context, kept []release.Metadata) ([]operation.Edit, error) {
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return nil, err
	}
	var stale []operation.Edit
	contexts := filepath.Join(c.Paths.Data, "application-contexts")
	names, err := privateEntries(c, contexts)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		used := slices.ContainsFunc(kept, func(metadata release.Metadata) bool {
			return name == metadata.Release+"-"+metadata.SourceDigest
		})
		if !used {
			stale = append(stale, operation.Edit{
				Path:        filepath.Join(contexts, name),
				Action:      "remove",
				Description: "Remove the setup context of a source no kept release uses",
			})
		}
	}
	tools, err := staleTools(c, state, kept)
	return append(stale, tools...), err
}

// privateEntries returns the names in directory, a private runtime directory
// that may not exist yet.
func privateEntries(c operation.Context, directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = operation.PrivateTree(c.Paths.Data, directory, false); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}
