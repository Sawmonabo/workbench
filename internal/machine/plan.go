package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Selection is what an apply covers: configuration only, or full provisioning
// plus any optional effects named in Effects.
type Selection struct {
	ConfigOnly bool
	Effects    []string
}

// Plan runs only reviewed target enumeration/status/diff against copied native
// state. It never initializes source Git, saves answers or runs provisioning.
func Plan(
	ctx context.Context,
	c operation.Context,
	selection Selection,
) (_ operation.Plan, err error) {
	defer operation.Annotate(&err, "plan machine configuration")
	defer c.ShowProgress("Planning")()
	// prepare cleans up after itself when it fails.
	prepared, err := prepare(ctx, c, selection)
	if err == nil {
		prepared.Close()
	}
	return prepared.Plan, err
}

type preparation struct {
	Plan                 operation.Plan
	Changes              []operation.TargetChange `json:"-"`
	native               operation.NativeContext
	scratch, executable  string
	environment, secrets []string
	selection            Selection
	// appUpdates and toolUpdates name the casks and formulae the plan's
	// update effects cover.
	appUpdates, toolUpdates []string
}

func (p *preparation) Close() {
	if p.scratch != "" {
		_ = os.RemoveAll(p.scratch)
		p.scratch = ""
	}
}

func prepare(
	ctx context.Context,
	c operation.Context,
	selection Selection,
) (prepared *preparation, err error) {
	prepared = &preparation{selection: selection}
	plan := &prepared.Plan
	*plan = operation.Plan{Scope: c.Scope, RecoveryLimits: recoveryLimits()}
	defer func() {
		if err != nil {
			prepared.Close()
		}
	}()
	// Probes whose failure only warns, such as Homebrew's update check, also
	// fail when interrupted; the interrupt must still end the plan.
	defer func() {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	c.Step("reading the machine source")
	files, identity, err := SourceSnapshot(c.Native.Source, c.Native.Developer)
	plan.Source = identity
	if err != nil {
		return prepared, err
	}
	requirements, err := SourceRequirements(files)
	if err != nil {
		return prepared, err
	}
	c.Step("checking chezmoi, uv and Python")
	answers, optional, err := prepared.checkPrerequisites(ctx, c, requirements)
	if err != nil {
		return prepared, err
	}
	c.Step("rendering your configuration")
	if err = prepared.stageNative(c, files, answers); err != nil {
		return prepared, err
	}
	targets, containers, err := prepared.readTargets(ctx, c)
	if err != nil {
		return prepared, err
	}
	if err = prepared.planEdits(ctx, c, targets, containers, answers.label()); err != nil {
		return prepared, err
	}
	// Use native built-in diff as the render/merge gate. Never publish its secret-
	// bearing bytes; paths/actions above are the public review surface.
	diff, err := prepared.run(ctx, c, "diff", "--exclude=scripts")
	if err != nil {
		return prepared, err
	}
	plan.Inputs = append(
		plan.Inputs,
		operation.Input{Name: "native-diff", Digest: operation.SHA256Hex([]byte(diff))},
	)
	plan.Effects = append(
		plan.Effects,
		operation.Effect{
			Name:        "ai-security-settings",
			Description: "Managed AI trust roots, approval/sandbox policy, enabled plugins and work hooks; review policy before apply",
			Privilege:   "user",
			Recovery:    "configuration files only",
		},
	)
	if !selection.ConfigOnly {
		plan.Effects = append(plan.Effects, provisioningEffects(answers)...)
		plan.Effects = append(plan.Effects, optional...)
	}
	if err = prepared.buildChanges(ctx, c); err != nil {
		return prepared, err
	}
	if !selection.ConfigOnly {
		prepared.planUpdates(ctx, c, files)
	}
	plan.Inputs = append(
		plan.Inputs,
		operation.Input{
			Name:   "checkpoint-images",
			Digest: operation.ChangesDigest(prepared.Changes),
		},
	)
	if len(prepared.Changes) > 0 {
		retention, retentionErr := operation.RetentionEffect(c)
		if retentionErr != nil {
			return prepared, retentionErr
		}
		if retention != nil {
			plan.Effects = append(plan.Effects, *retention)
		}
	}
	plan.Complete = true
	return prepared, nil
}

func recoveryLimits() []string {
	return []string{
		"Restores exact checkpointed files, modes and links only; packages, extensions, registry, services and uncheckpointed script writes are not reverted",
		fmt.Sprintf(
			"Limits: %d MiB/file, %d MiB image pairs, %d targets, %d forward plus paired recovery checkpoints and %d GiB per scope; at the checkpoint limit the plan lists removal of the oldest settled one",
			operation.MaxImageBytes>>20,
			operation.MaxCheckpointImageBytes>>20,
			operation.MaxCheckpointTargets,
			operation.MaxForwardCheckpoints,
			operation.MaxScopeCheckpointBytes>>30,
		),
	}
}

// checkPrerequisites resolves management tools, the platform and answers, and
// validates the selection. Missing prerequisites are listed in the plan.
func (p *preparation) checkPrerequisites(
	ctx context.Context,
	c operation.Context,
	requirements Requirements,
) (Answers, []operation.Effect, error) {
	plan := &p.Plan
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return nil, nil, err
	}
	var recorded []operation.Dependency
	if state != nil {
		recorded = state.Dependencies
	}
	dependencies, results := ResolveDependencies(ctx, c, requirements, recorded, os.Getenv("PATH"))
	plan.Dependencies = dependencies
	for _, result := range results {
		if result.Status != operation.StatusComplete &&
			(result.Name != "uv" || !p.selection.ConfigOnly) {
			plan.Prerequisites = append(plan.Prerequisites, result.Name+": "+result.Message)
		}
	}
	if len(plan.Prerequisites) > 0 {
		return nil, nil, operation.Fail(
			operation.ExitBlocked,
			"prerequisites",
			"Qualified rendering/provisioning dependencies are missing; approve setup separately",
		)
	}
	if platform := checkPlatform(ctx, c); platform.Status != operation.StatusComplete {
		plan.Prerequisites = append(plan.Prerequisites, platform.Message)
		return nil, nil, operation.Fail(operation.ExitBlocked, "platform", platform.Message)
	}
	answersRaw, err := operation.ReadPrivateInput(c.Native.Config, 1<<20)
	if err != nil {
		return nil, nil, operation.Fail(
			operation.ExitBlocked,
			"answers",
			"Provide a complete private [data] answer file through --machine-config; preview does not initialize answers",
		)
	}
	answers, err := parseAnswers(answersRaw)
	if err != nil {
		return nil, nil, err
	}
	plan.Inputs = append(
		plan.Inputs,
		operation.Input{Name: "machine-answers", Digest: operation.SHA256Hex(answersRaw)},
	)
	if !p.selection.ConfigOnly && c.Native.Destination != c.Home {
		return nil, nil, operation.Fail(
			operation.ExitBlocked,
			"scope",
			"Full provisioning requires the actual user's home; use --config-only for an isolated destination",
		)
	}
	optional, err := selectedEffects(p.selection)
	if err != nil {
		return nil, nil, err
	}
	p.executable, p.secrets = dependency(dependencies, "chezmoi"), answers.secrets()
	return answers, optional, nil
}

// stageNative copies the role-selected sources, answers and native state into
// a private scratch context, with a tool directory for modify scripts.
func (p *preparation) stageNative(
	c operation.Context,
	files map[string][]byte,
	answers Answers,
) error {
	scratch, err := os.MkdirTemp("", "workbench-preview-")
	if err != nil {
		return err
	}
	p.scratch = scratch
	scratch, err = filepath.EvalSymlinks(scratch)
	if err != nil {
		return err
	}
	native := c.Native
	native.Source = filepath.Join(scratch, "source")
	native.Config = filepath.Join(scratch, "answers.json")
	native.PersistentState = filepath.Join(scratch, "state.boltdb")
	native.Cache = filepath.Join(scratch, "cache")
	p.native = native
	if err = writeRoleSources(native.Source, files, answers); err != nil {
		return err
	}
	nativeAnswers := map[string]any{}
	maps.Copy(nativeAnswers, answers)
	nativeAnswers["workbench_managed"] = true
	// Setup's single private uv owner must also be usable by new user shells.
	// Existing native shell targets expose its directory without replacing
	// any unrelated ~/.local/bin command or introducing another installer.
	if uv := dependency(p.Plan.Dependencies, "uv"); operation.Within(c.Paths.Data, uv) {
		nativeAnswers["workbench_uv_dir"] = filepath.Dir(uv)
	}
	config, _ := json.Marshal(map[string]any{"data": nativeAnswers})
	if err = os.WriteFile(native.Config, config, 0o600); err != nil {
		return err
	}
	if err = p.copyNativeState(c); err != nil {
		return err
	}
	// A private alias gives modify scripts the qualified interpreter even when
	// the installation exposes only a versioned basename. No global links.
	bin := filepath.Join(scratch, "bin")
	if err = os.Mkdir(bin, 0o700); err != nil {
		return err
	}
	python := dependency(p.Plan.Dependencies, "python3")
	if err = os.Symlink(python, filepath.Join(bin, "python3")); err != nil {
		return err
	}
	// gh only affects a native lookPath branch; do not execute it in preview.
	search, excluded := os.Getenv("PATH"), []string{c.Native.Source}
	if gh, findErr := operation.FindExecutable("gh", search, excluded); findErr == nil {
		if err = os.Symlink(gh, filepath.Join(bin, "gh")); err != nil {
			return err
		}
		p.Plan.Inputs = append(
			p.Plan.Inputs,
			operation.Input{Name: "gh-location", Digest: operation.SHA256Hex([]byte(gh))},
		)
	}
	p.environment = []string{
		"HOME=" + c.Native.Destination,
		"PATH=" + bin + ":/usr/bin:/bin",
		"LANG=C.UTF-8",
		"PYTHONNOUSERSITE=1",
		"PYTHONDONTWRITEBYTECODE=1",
	}
	return nil
}

// writeRoleSources writes the source files the machine role owns.
//
// Native v2.70.3 ignores removals for ignored files and rejects a target
// simultaneously owned by an ordinary source and .chezmoiremove. Select the
// canonical role-owned sources in this private copy; native still owns every
// target, removal, diff and apply operation.
func writeRoleSources(source string, files map[string][]byte, answers Answers) error {
	var roles struct {
		Targets []struct {
			Source, Target string
			Roles          []string
		} `json:"role_targets"`
	}
	if err := json.Unmarshal(files["home/.chezmoidata/role-targets.json"], &roles); err != nil {
		return err
	}
	role, _ := answers["machine_role"].(string) // validateAnswers requires a string
	omitted := map[string]bool{}
	for _, target := range roles.Targets {
		if !slices.Contains(target.Roles, role) {
			omitted["home/"+target.Source] = true
		}
	}
	for name, data := range files {
		if omitted[name] {
			continue
		}
		path := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// copyNativeState copies Workbench's native chezmoi state into the scratch
// context and binds its digest, or records that none exists yet.
func (p *preparation) copyNativeState(c operation.Context) error {
	_, err := os.Lstat(c.Native.PersistentState)
	if os.IsNotExist(err) {
		p.Plan.Inputs = append(
			p.Plan.Inputs,
			operation.Input{Name: "native-state", Digest: "absent"},
		)
		return nil
	}
	if err != nil {
		return err
	}
	data, err := operation.ReadPrivateInput(c.Native.PersistentState, 16<<20)
	if err != nil {
		return err
	}
	p.Plan.Inputs = append(
		p.Plan.Inputs,
		operation.Input{Name: "native-state", Digest: operation.SHA256Hex(data)},
	)
	return os.WriteFile(p.native.PersistentState, data, 0o600)
}

// run executes one reviewed read-only native chezmoi command in the scratch
// context and returns its exact private stdout.
func (p *preparation) run(
	ctx context.Context,
	c operation.Context,
	args ...string,
) (string, error) {
	prefix, err := p.native.Args()
	if err != nil {
		return "", err
	}
	prefix = append(
		prefix,
		"--config-format=json",
		"--no-tty",
		"--color=false",
		"--use-builtin-git=true",
	)
	output, err := operation.Run(
		ctx,
		c,
		nil,
		operation.Process{
			Executable:    p.executable,
			Args:          append(prefix, args...),
			Directory:     p.scratch,
			Environment:   p.environment,
			Secrets:       p.secrets,
			PrivateOutput: true,
			OutputLimit:   16 << 20,
		},
	)
	return output.Stdout, err
}

// readTargets binds the current image of every managed target. Protected
// ancestor directories are returned as containers and bound by type and mode.
func (p *preparation) readTargets(
	ctx context.Context,
	c operation.Context,
) ([]string, map[string]bool, error) {
	managed, err := p.run(
		ctx,
		c,
		"managed",
		"--exclude=scripts",
		"--nul-path-separator",
		"--path-style=absolute",
	)
	if err != nil {
		return nil, nil, err
	}
	targets := strings.Split(strings.TrimSuffix(managed, "\x00"), "\x00")
	slices.Sort(targets)
	containers := map[string]bool{}
	for _, target := range targets {
		if err = c.ValidateTarget(target); err != nil {
			if containerErr := c.ValidateContainer(target); containerErr != nil {
				return nil, nil, err
			}
			containers[target] = true
		}
		image, readErr := operation.ReadImage(c, target)
		if containers[target] {
			info, statErr := os.Lstat(target)
			readErr = statErr
			if statErr == nil {
				image = operation.Image{
					Kind: operation.ImageDirectory,
					Mode: uint32(info.Mode().Perm()),
				}
			}
		}
		if readErr != nil {
			return nil, nil, readErr
		}
		p.Plan.Inputs = append(
			p.Plan.Inputs,
			operation.Input{Name: target, Digest: operation.ImageDigest(image)},
		)
	}
	return targets, containers, nil
}

// planEdits turns native status into plan edits, refusing changes to protected
// ancestors and binding any target native reports outside the managed list.
func (p *preparation) planEdits(
	ctx context.Context,
	c operation.Context,
	targets []string,
	containers map[string]bool,
	label string,
) error {
	status, err := p.run(ctx, c, "status", "--exclude=scripts", "--path-style=absolute")
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(status, "\n") {
		if line == "" {
			continue
		}
		if len(line) < 4 || line[2] != ' ' {
			return operation.Fail(operation.ExitFailed, "native", "Unexpected native status output")
		}
		target := line[3:]
		if containers[target] && line[1] != ' ' {
			return operation.Fail(
				operation.ExitBlocked,
				"scope",
				"Native plan would change a protected ancestor directory; review its mode/type before applying",
			)
		}
		if !containers[target] {
			if err = c.ValidateTarget(target); err != nil {
				return err
			}
		}
		if !slices.Contains(targets, target) {
			image, readErr := operation.ReadImage(c, target)
			if readErr != nil {
				return readErr
			}
			p.Plan.Inputs = append(
				p.Plan.Inputs,
				operation.Input{Name: target, Digest: operation.ImageDigest(image)},
			)
		}
		actions := map[byte]string{'A': "create", 'M': "modify", 'D': "remove", ' ': "unchanged"}
		action := actions[line[1]]
		if action == "" {
			return operation.Fail(
				operation.ExitFailed,
				"native",
				"Unsupported native target action",
			)
		}
		if action != "unchanged" {
			p.Plan.Edits = append(
				p.Plan.Edits,
				operation.Edit{
					Path:        target,
					Action:      action,
					Description: "Native chezmoi configuration change (" + label + ")",
				},
			)
		}
	}
	return nil
}

// buildChanges records each edit's exact before image and the after image
// native renders, refusing metadata the checkpoint owner cannot preserve.
func (p *preparation) buildChanges(ctx context.Context, c operation.Context) error {
	rendered, err := p.run(ctx, c, "dump", "--exclude=scripts", "--format=json")
	if err != nil {
		return err
	}
	var desired map[string]nativeEntry
	if json.Unmarshal([]byte(rendered), &desired) != nil {
		return operation.Fail(
			operation.ExitFailed,
			"native",
			"Unexpected native target-image output",
		)
	}
	last, err := operation.LastApplied(c)
	if err != nil {
		return err
	}
	for i, edit := range p.Plan.Edits {
		before, err := operation.ReadImage(c, edit.Path)
		if err != nil {
			return err
		}
		after := operation.Image{Kind: operation.ImageAbsent}
		if edit.Action != "remove" {
			relative, err := filepath.Rel(c.Native.Destination, edit.Path)
			if err != nil {
				return err
			}
			entry, ok := desired[relative]
			if !ok {
				return operation.Fail(
					operation.ExitFailed,
					"native",
					"Native target missing from image enumeration",
				)
			}
			if after, err = entry.image(); err != nil {
				return err
			}
		}
		after, err = operation.ImageWithGroup(c, edit.Path, after)
		if err != nil {
			return err
		}
		if err = checkPreservable(c, edit, before, after); err != nil {
			return err
		}
		var previous *operation.Image
		if image, ok := last[edit.Path]; ok {
			previous = &image
		}
		p.Plan.Edits[i].Summary = operation.ChangeSummary(before, after, previous)
		p.Changes = append(
			p.Changes,
			operation.TargetChange{Path: edit.Path, Before: before, After: after},
		)
	}
	return nil
}

// nativeEntry is one target in native chezmoi's dump output.
type nativeEntry struct {
	Type     string `json:"type"`
	Perm     uint32 `json:"perm"`
	Contents string `json:"contents"`
	Target   string `json:"target"`
}

func (e nativeEntry) image() (operation.Image, error) {
	switch e.Type {
	case "file":
		return operation.Image{
			Kind: operation.ImageFile,
			Mode: e.Perm,
			Data: []byte(e.Contents),
		}, nil
	case "dir":
		return operation.Image{Kind: operation.ImageDirectory, Mode: e.Perm}, nil
	case "symlink":
		return operation.Image{Kind: operation.ImageSymlink, Mode: 0o777, Link: e.Target}, nil
	}
	return operation.Image{}, operation.Fail(
		operation.ExitBlocked,
		"native",
		"Native target type is unsupported for recovery",
	)
}

// checkPreservable refuses group-exclusive modes and replacements that would
// change a target's group.
func checkPreservable(
	c operation.Context,
	edit operation.Edit,
	before, after operation.Image,
) error {
	// Native atomic writes can initially inherit the scratch directory's
	// group. Until corrected under the checkpoint owner's exact-image gate,
	// never expose permissions available only to that transient group.
	if ((after.Mode>>3)&7) & ^(after.Mode&7) != 0 {
		return operation.Fail(
			operation.ExitBlocked,
			"metadata",
			"Native group-exclusive permissions require separately qualified atomic ownership handling",
		)
	}
	if before.Kind != operation.ImageFile && before.Kind != operation.ImageSymlink {
		return nil
	}
	group, err := operation.CreationGroup(c, edit.Path)
	if err != nil {
		return err
	}
	if edit.Action != "remove" && before.Group != nil && *before.Group != group {
		return operation.Fail(
			operation.ExitBlocked,
			"metadata",
			"Native replacement cannot preserve this target's group ownership",
		)
	}
	return nil
}

// Apply executes the prepared configuration through the same native owner.
// Callers must hold mutation authority and a durable checkpoint of any target changes.
// An interactive run owns terminal; otherwise redacted output goes to progress.
func (p *preparation) Apply(
	ctx context.Context,
	c operation.Context,
	m *operation.Mutation,
	terminal *os.File,
	progress io.Writer,
) error {
	if err := m.Check(); err != nil {
		return err
	}
	if !p.Plan.Complete || p.scratch == "" || p.Plan.Scope != c.Scope {
		return operation.Fail(
			operation.ExitBlocked,
			"plan",
			"Native apply requires an open complete prepared plan",
		)
	}
	if c.Native.PersistentState != filepath.Join(c.Paths.State, "chezmoi", "chezmoi.boltdb") {
		return operation.Fail(operation.ExitInvalid, "state", "Unexpected native state target")
	}
	if err := os.MkdirAll(filepath.Dir(c.Native.PersistentState), 0o700); err != nil {
		return err
	}
	native := p.native
	native.PersistentState = c.Native.PersistentState
	args, err := native.Args()
	if err != nil {
		return err
	}
	args = append(args, "--config-format=json", "--color=false", "--use-builtin-git=true")
	if terminal == nil {
		args = append(args, "--no-tty")
	}
	args = append(args, "apply", "--force")
	environment := p.environment
	if p.selection.ConfigOnly {
		args = append(args, "--exclude=scripts")
	} else {
		environment = scriptEnvironment(c, p.Plan.Dependencies)
		for _, name := range p.selection.Effects {
			environment = append(environment, effectVariable(name)+"=1")
		}
		if len(p.appUpdates) > 0 {
			environment = append(
				environment,
				"WORKBENCH_APP_UPDATES="+strings.Join(p.appUpdates, " "),
			)
		}
		if len(p.toolUpdates) > 0 {
			environment = append(
				environment,
				"WORKBENCH_TOOL_UPDATES="+strings.Join(p.toolUpdates, " "),
			)
		}
		for i, value := range environment {
			if rest, ok := strings.CutPrefix(value, "PATH="); ok {
				environment[i] = "PATH=" + filepath.Join(p.scratch, "bin") + ":" + rest
			}
		}
	}
	request := operation.Process{
		Executable:  p.executable,
		Args:        args,
		Directory:   p.scratch,
		Environment: environment,
		Secrets:     p.secrets,
		Mutates:     true,
		Terminal:    terminal,
	}
	if terminal == nil {
		request.Progress = progress
	}
	_, err = operation.Run(ctx, c, m, request)
	return err
}
