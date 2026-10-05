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
	prepared, err := prepare(ctx, c, selection, true)
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
	// active names the files and scripts this apply would change or run; see
	// activeSources.
	active map[string]bool
	// appUpdates and toolUpdates name the casks and formulae the plan's
	// update effects cover.
	appUpdates, toolUpdates []string
	// desired is native's rendered image of every target, by relative path.
	desired map[string]nativeEntry
	// blocked is, after Apply, the reason a script gave for each effect it
	// could not do while it carried on with the rest, by effect name.
	blocked map[string]string
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
	probe bool,
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
	answers, err := prepared.checkPrerequisites(ctx, c, requirements)
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
	if err = prepared.readDesired(ctx, c); err != nil {
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
	effects := []operation.Effect{aiSecurityEffect}
	effects = append(effects, provisioningEffects(answers)...)
	prepared.active, err = prepared.activeSources(ctx, c)
	if err != nil {
		return prepared, err
	}
	plan.Effects = append(plan.Effects, activeEffects(effects, prepared.active)...)
	plan.Effects = append(plan.Effects, hostOptionalEffects()...)
	if err = prepared.buildChanges(ctx, c); err != nil {
		return prepared, err
	}
	prepared.planUpdates(ctx, c, files)
	if err = prepared.selectEffects(ctx, c, selection, probe); err != nil {
		return prepared, err
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

// selectEffects probes the active scripts for their deltas, then checks each
// effect as the selection and the saved skips say. An isolated destination
// gets files only: scripts provision the real home.
func (p *preparation) selectEffects(
	ctx context.Context,
	c operation.Context,
	selection Selection,
	probe bool,
) error {
	if probe {
		if err := p.probeEffects(ctx, c); err != nil {
			return err
		}
	}
	saved, err := ReadSelection(c.Native.Config)
	if err != nil {
		return err
	}
	if !selection.Forget {
		p.Plan.Warnings = append(p.Plan.Warnings, staleSelectionWarnings(saved)...)
	}
	p.Plan.Effects = applySelection(p.Plan.Effects, selection, saved)
	if c.Native.Destination != c.Home {
		for i := range p.Plan.Effects {
			if !p.Plan.Effects[i].Fixed {
				p.Plan.Effects[i].Checked = false
			}
		}
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			"Isolated destination: provisioning effects are unchecked and only files apply",
		)
	}
	return nil
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
) (Answers, error) {
	plan := &p.Plan
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return nil, err
	}
	var recorded []operation.Dependency
	if state != nil {
		recorded = state.Dependencies
	}
	dependencies, results := ResolveDependencies(ctx, c, requirements, recorded, os.Getenv("PATH"))
	plan.Dependencies = dependencies
	for _, result := range results {
		if result.Status != operation.StatusComplete {
			plan.Prerequisites = append(plan.Prerequisites, result.Name+": "+result.Message)
		}
	}
	if len(plan.Prerequisites) > 0 {
		message := "Workbench's tools are missing; run workbench update, or workbench apply at a terminal, to install them"
		if c.Native.Developer {
			message = "This checkout's pinned tools are not installed; run workbench apply --local-build at a terminal to install them"
		}
		return nil, operation.Fail(operation.ExitBlocked, "prerequisites", message)
	}
	if platform := checkPlatform(ctx, c); platform.Status != operation.StatusComplete {
		plan.Prerequisites = append(plan.Prerequisites, platform.Message)
		return nil, operation.Fail(operation.ExitBlocked, "platform", platform.Message)
	}
	answersRaw, err := operation.ReadPrivateInput(c.Native.Config, 1<<20)
	if err != nil {
		message := "Machine answers are missing; run workbench apply at a terminal to answer them, or workbench init --answers-from FILE"
		if c.Native.Developer {
			// apply --local-build never asks the questions.
			message = "Machine answers are missing; save them with workbench init --answers-from FILE"
		}
		return nil, operation.Fail(operation.ExitBlocked, "answers", message)
	}
	answers, err := parseAnswers(answersRaw)
	if err != nil {
		return nil, err
	}
	plan.Inputs = append(
		plan.Inputs,
		operation.Input{Name: "machine-answers", Digest: operation.SHA256Hex(answersRaw)},
	)
	p.executable, p.secrets = dependency(dependencies, "chezmoi"), answers.secrets()
	return answers, nil
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
	// A Windows-side script renders the distribution name into the PowerShell
	// profile it writes. The apply renders with it (see scriptEnvironment), so
	// the preview must as well, or the plan compares a different profile and
	// never finds the one on disk already matching.
	if name := os.Getenv("WSL_DISTRO_NAME"); isWSL() && name != "" {
		p.environment = append(p.environment, "WSL_DISTRO_NAME="+name)
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
			if err = p.folderMode(c, target, line[1]); err != nil {
				return err
			}
			continue
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
	p.countUnchanged(c, targets, containers)
	return nil
}

// countUnchanged counts the managed files and links the plan leaves as they
// are. Native status lists only entries that differ, so this counts them from
// the managed list instead of from status lines.
func (p *preparation) countUnchanged(
	c operation.Context,
	targets []string,
	containers map[string]bool,
) {
	changing := map[string]bool{}
	for _, edit := range p.Plan.Edits {
		changing[edit.Path] = true
	}
	for _, target := range targets {
		relative, err := filepath.Rel(c.Native.Destination, target)
		if err != nil || changing[target] || containers[target] {
			continue
		}
		if kind := p.desired[relative].Type; kind == "file" || kind == "symlink" {
			p.Plan.UnchangedTargets++
		}
	}
}

// activeSources returns what this apply would actually do: the files the plan
// changes, relative to the destination, and for a full apply the scripts native
// chezmoi would run, by name without chezmoi's prefixes and .sh suffix. A
// once-only script that already ran, or an on-change script whose content is
// unchanged, is not among them. Rendering scripts only reads source files.
func (p *preparation) activeSources(
	ctx context.Context,
	c operation.Context,
) (map[string]bool, error) {
	active := map[string]bool{}
	for _, edit := range p.Plan.Edits {
		if relative, err := filepath.Rel(c.Native.Destination, edit.Path); err == nil {
			active[relative] = true
		}
	}
	status, err := p.run(ctx, c, "status", "--include=scripts")
	if err != nil {
		return nil, err
	}
	for line := range strings.SplitSeq(status, "\n") {
		if len(line) > 3 && line[1] == 'R' {
			active[strings.TrimSuffix(filepath.Base(line[3:]), ".sh")] = true
		}
	}
	return active, nil
}

// readDesired reads the image native renders for every target.
func (p *preparation) readDesired(ctx context.Context, c operation.Context) error {
	rendered, err := p.run(ctx, c, "dump", "--exclude=scripts", "--format=json")
	if err != nil {
		return err
	}
	if json.Unmarshal([]byte(rendered), &p.desired) != nil {
		return operation.Fail(
			operation.ExitFailed,
			"native",
			"Unexpected native target-image output",
		)
	}
	return nil
}

// folderMode allows native to change only the mode of a directory that holds
// Workbench's own files, such as ~/.config on Linux, and only to one without
// group or other write. It is an ordinary checkpointed edit, so revert
// restores the mode; any other change there is refused.
func (p *preparation) folderMode(c operation.Context, target string, action byte) error {
	relative, err := filepath.Rel(c.Native.Destination, target)
	if err != nil {
		return err
	}
	desired, ok := p.desired[relative]
	info, statErr := os.Lstat(target)
	if action != 'M' || !ok || desired.Type != "dir" || desired.Perm&0o022 != 0 ||
		statErr != nil || !info.IsDir() {
		return operation.Fail(
			operation.ExitBlocked,
			"scope",
			"Native plan would replace or loosen "+target+", which holds Workbench's own files",
		)
	}
	// macOS keeps flags on ~/Library that a checkpoint cannot record; a real
	// Mac already has these folders at the configuration's modes.
	if _, err = operation.ReadImage(c, target); err != nil {
		return operation.Fail(
			operation.ExitBlocked,
			"scope",
			fmt.Sprintf(
				"%s holds Workbench's own files, and revert could not restore its mode (%v); run chmod %04o %q to match the configuration, then retry",
				target,
				err,
				desired.Perm,
				target,
			),
		)
	}
	p.Plan.Edits = append(p.Plan.Edits, operation.Edit{
		Path:        target,
		Action:      "modify",
		Description: "Folder that holds Workbench's own files; mode only",
	})
	return nil
}

// buildChanges records each edit's exact before image and the after image
// native renders, refusing metadata the checkpoint owner cannot preserve. It
// also gives each edit what the plan view shows: its plain title, whether it
// is merged into or owned, whether an owned file was edited outside Workbench,
// and the capped, redacted unified diff with its line counts.
func (p *preparation) buildChanges(ctx context.Context, c operation.Context) error {
	desired := p.desired
	last, err := operation.LastApplied(c)
	if err != nil {
		return err
	}
	written, err := p.nativeWritten(ctx, c)
	if err != nil {
		return err
	}
	var candidates []string
	for _, edit := range p.Plan.Edits {
		if edit.Action != "remove" {
			candidates = append(candidates, edit.Path)
		}
	}
	merged, err := p.mergedTargets(ctx, c, candidates)
	if err != nil {
		return err
	}
	for i, edit := range p.Plan.Edits {
		before, err := operation.ReadImage(c, edit.Path)
		if err != nil {
			return err
		}
		after := operation.Image{Kind: operation.ImageAbsent}
		relative, err := filepath.Rel(c.Native.Destination, edit.Path)
		if err != nil {
			return err
		}
		if edit.Action != "remove" {
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
		} else if before.Kind == operation.ImageFile &&
			written[edit.Path] == operation.SHA256Hex(before.Data) {
			// Retention may have removed the checkpoint that wrote this file.
			// Native's own state still says what it last wrote, and the file
			// still is that, so it was not edited outside Workbench.
			previous = &before
		}
		edited := operation.EditedOutside(before, previous)
		if merged[edit.Path] {
			// Native merges into this file and keeps the owner's own keys, so
			// the owner's edits are expected, not replaced.
			previous, edited = &before, false
		}
		planned := &p.Plan.Edits[i]
		planned.Summary = operation.ChangeSummary(before, after, previous)
		planned.Merged, planned.EditedOutside = merged[edit.Path], edited
		planned.Folder = before.Kind == operation.ImageDirectory ||
			after.Kind == operation.ImageDirectory
		planned.Title = fileTitle(relative)
		if added, removed, lines, ok := operation.FileDiff(before, after); ok {
			planned.Added, planned.Removed = added, removed
			planned.Diff, planned.DiffTruncated = capDiff(redactDiff(lines, p.secrets))
			p.describeSettings(planned, before, after)
		}
		p.Changes = append(
			p.Changes,
			operation.TargetChange{Path: edit.Path, Before: before, After: after},
		)
	}
	return nil
}

// nativeWritten returns the SHA-256 of the contents native chezmoi last wrote
// to each file target, by absolute path. A target native never wrote has no
// entry.
func (p *preparation) nativeWritten(
	ctx context.Context,
	c operation.Context,
) (map[string]string, error) {
	dump, err := p.run(ctx, c, "state", "dump", "--format=json")
	if err != nil {
		return nil, err
	}
	var state struct {
		EntryState map[string]struct {
			Type           string `json:"type"`
			ContentsSHA256 string `json:"contentsSHA256"`
		} `json:"entryState"`
	}
	if err = json.Unmarshal([]byte(dump), &state); err != nil {
		return nil, operation.Fail(operation.ExitFailed, "native", "Unexpected native state output")
	}
	written := map[string]string{}
	for target, entry := range state.EntryState {
		if entry.Type == "file" && entry.ContentsSHA256 != "" {
			written[target] = entry.ContentsSHA256
		}
	}
	return written, nil
}

// describeSettings replaces the line diff of a merged JSON or TOML file with
// the list of settings that change, when both sides parse: the merge
// re-serializes the file, so its line diff is mostly reordering. Any other file
// keeps its line diff.
func (p *preparation) describeSettings(
	planned *operation.Edit,
	before, after operation.Image,
) {
	format := strings.TrimPrefix(filepath.Ext(planned.Path), ".")
	if !planned.Merged || before.Kind != operation.ImageFile ||
		after.Kind != operation.ImageFile || (format != "json" && format != "toml") {
		return
	}
	changes, rewritten, ok := operation.SettingsDiff(format, before.Data, after.Data)
	if !ok {
		return
	}
	planned.Settings, planned.Semantic, planned.Rewritten = len(changes), true, rewritten
	planned.Diff, planned.DiffTruncated = capDiff(settingLines(changes, p.secrets))
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
	report := ""
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
	if c.Native.Destination != c.Home {
		// An isolated destination gets files only, rendered under the same
		// environment as the preview; its scripts provision the real home.
		args = append(args, "--exclude=scripts")
	} else {
		if err = p.selectScripts(); err != nil {
			return err
		}
		environment = scriptEnvironment(c, p.Plan.Dependencies)
		// A script that cannot do one effect says so here and goes on with the
		// others, so one part's failure does not stop the rest.
		report = filepath.Join(p.scratch, "effect-report")
		environment = append(environment, "WORKBENCH_EFFECT_REPORT="+report)
		// Every effect is gated on its own: a shared script runs only the
		// sections whose effect is checked, and the optional ones keep their
		// existing == 1 checks.
		for _, effect := range p.Plan.Effects {
			if effect.Fixed {
				continue
			}
			value := "0"
			if effect.Checked {
				value = "1"
			}
			environment = append(environment, effectVariable(effect.Name)+"="+value)
		}
		if updates := p.checkedUpdates(p.appUpdates); len(updates) > 0 {
			environment = append(environment, "WORKBENCH_APP_UPDATES="+strings.Join(updates, " "))
		}
		if updates := p.checkedUpdates(p.toolUpdates); len(updates) > 0 {
			environment = append(environment, "WORKBENCH_TOOL_UPDATES="+strings.Join(updates, " "))
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
	if report != "" {
		p.blocked = readBlocked(report, p.Plan.Effects)
	}
	return err
}

// readBlocked reads the file in which a script names each effect it could not
// do, one "effect, tab, reason" line each, keeping the effects of this plan.
func readBlocked(path string, effects []operation.Effect) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	blocked := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		name, reason, ok := strings.Cut(line, "\t")
		if ok && slices.ContainsFunc(effects, func(effect operation.Effect) bool {
			return effect.Name == name
		}) {
			blocked[name] = reason
		}
	}
	return blocked
}

// selectScripts applies the selection to the private source copy. A script
// whose listed effects are all unchecked is deleted, so native neither runs it
// nor records it as run; a later apply with an effect checked runs it as if
// new. A script that stays but has an unchecked owner gets a comment naming the
// unchecked effects, so its content, which chezmoi hashes to decide whether a
// run-once or on-change script ran, differs from the fully checked one: the
// effect's section runs when it is checked later. A checked update effect
// owns the script that runs updates (see [updatesScript]) like a listed effect.
// The plan's source identity and digest are computed from the unmodified source
// before this runs.
func (p *preparation) selectScripts() error {
	checked, listed := map[string]bool{}, map[string]bool{}
	for _, effect := range p.Plan.Effects {
		checked[effect.Name], listed[effect.Name] = effect.Checked, true
	}
	root := filepath.Join(p.native.Source, "home", ".chezmoiscripts")
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		base := entry.Name()
		if !strings.HasPrefix(base, "run_") || !strings.HasSuffix(base, ".sh.tmpl") {
			return nil
		}
		script := strings.TrimSuffix(base[strings.LastIndex(base, "_")+1:], ".sh.tmpl")
		owners := scriptEffects(script)
		if script == updatesScript {
			// A checked update keeps the script that runs it, even with the
			// apps and the work extension unchecked.
			for _, effect := range p.Plan.Effects {
				if strings.HasPrefix(effect.Name, updateEffectPrefix) {
					owners = append(owners, effect.Name)
				}
			}
		}
		if len(owners) == 0 {
			return nil
		}
		var skipped []string
		for _, name := range owners {
			if listed[name] && !checked[name] {
				skipped = append(skipped, name)
			}
		}
		if !slices.ContainsFunc(owners, func(name string) bool { return checked[name] }) {
			return os.Remove(path)
		}
		if len(skipped) == 0 {
			return nil
		}
		return markSkipped(path, skipped)
	})
}

// markSkipped inserts a comment naming the skipped effects directly after the
// script's shebang line. A line at the end would render on every platform,
// since the templates wrap their text in a platform condition.
func markSkipped(path string, skipped []string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	start := strings.Index(text, "#!")
	if start < 0 || !strings.Contains(text[start:], "\n") {
		return operation.Fail(
			operation.ExitFailed,
			"native",
			"A provisioning script has no shebang line",
		)
	}
	end := start + strings.Index(text[start:], "\n")
	note := "\n# workbench: skipped " + strings.Join(skipped, ", ")
	return os.WriteFile(path, []byte(text[:end]+note+text[end:]), info.Mode().Perm())
}

// checkedUpdates keeps the Homebrew updates whose update-<name> effect is
// checked.
func (p *preparation) checkedUpdates(names []string) []string {
	var kept []string
	for _, name := range names {
		for _, effect := range p.Plan.Effects {
			if effect.Name == updateEffectPrefix+name && effect.Checked {
				kept = append(kept, name)
			}
		}
	}
	return kept
}
