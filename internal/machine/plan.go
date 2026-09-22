package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
)

// Selection is what an apply covers: configuration only, or full provisioning
// plus any optional effects named in Effects.
type Selection struct {
	ConfigOnly bool
	Effects    []string
}

// Plan runs only reviewed target enumeration/status/diff against copied native
// state. It never initializes source Git, saves answers or runs provisioning.
func Plan(ctx context.Context, c operation.Context, selection Selection) (operation.Plan, error) {
	prepared, err := Prepare(ctx, c, selection)
	if prepared == nil {
		return operation.Plan{}, err
	}
	if err != nil {
		return prepared.Plan, err
	}
	defer prepared.Close()
	return prepared.Plan, err
}

type Prepared struct {
	Plan                 operation.Plan
	Changes              []operation.TargetChange `json:"-"`
	native               operation.NativeContext
	scratch, executable  string
	environment, secrets []string
	selection            Selection
}

func (p *Prepared) Close() {
	if p.scratch != "" {
		_ = os.RemoveAll(p.scratch)
		p.scratch = ""
	}
}

func Prepare(
	ctx context.Context,
	c operation.Context,
	selection Selection,
) (prepared *Prepared, err error) {
	prepared = &Prepared{selection: selection}
	plan := &prepared.Plan
	*plan = operation.Plan{Scope: c.Scope, RecoveryLimits: []string{
		"Restores exact checkpointed files, modes and links only; packages, extensions, registry, services and uncheckpointed script writes are not reverted",
		fmt.Sprintf(
			"Limits: %d MiB/file, %d MiB image pairs, %d targets, %d forward plus paired recovery checkpoints and %d GiB per scope; at the checkpoint limit the plan lists removal of the oldest settled one",
			operation.MaxImageBytes>>20,
			operation.MaxCheckpointImageBytes>>20,
			operation.MaxCheckpointTargets,
			operation.MaxForwardCheckpoints,
			operation.MaxScopeCheckpointBytes>>30,
		),
	}}
	defer func() {
		if err != nil {
			prepared.Close()
		}
	}()
	files, identity, err := SourceSnapshot(c.Native.Source)
	plan.Source = identity
	if err != nil {
		return prepared, err
	}
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return prepared, err
	}
	var recorded []operation.Dependency
	if state != nil {
		recorded = state.Dependencies
	}
	dependencies, results := ResolveDependencies(ctx, c, recorded, os.Getenv("PATH"))
	plan.Dependencies = dependencies
	for _, result := range results {
		if result.Status != "complete" && (result.Name != "uv" || !selection.ConfigOnly) {
			plan.Prerequisites = append(plan.Prerequisites, result.Name+": "+result.Message)
		}
	}
	if len(plan.Prerequisites) > 0 {
		return prepared, operation.Fail(
			3,
			"prerequisites",
			"Qualified rendering/provisioning dependencies are missing; approve setup separately",
		)
	}
	if platform := Platform(ctx, c); platform.Status != "complete" {
		plan.Prerequisites = append(plan.Prerequisites, platform.Message)
		return prepared, operation.Fail(3, "platform", platform.Message)
	}
	answersRaw, err := operation.ReadPrivateInput(c.Native.Config, 1<<20)
	if err != nil {
		return prepared, operation.Fail(
			3,
			"answers",
			"Provide a complete private [data] answer file through --machine-config; preview does not initialize answers",
		)
	}
	answers, err := ParseAnswers(answersRaw)
	if err != nil {
		return prepared, err
	}
	plan.Inputs = append(
		plan.Inputs,
		operation.Input{Name: "machine-answers", Digest: digest(answersRaw)},
	)
	if !selection.ConfigOnly && c.Native.Destination != c.Home {
		return prepared, operation.Fail(
			3,
			"scope",
			"Full provisioning requires the actual user's home; use --config-only for an isolated destination",
		)
	}
	optional, err := selectedEffects(selection)
	if err != nil {
		return prepared, err
	}
	scratch, err := os.MkdirTemp("", "workbench-preview-")
	if err != nil {
		return prepared, err
	}
	prepared.scratch = scratch
	scratch, err = filepath.EvalSymlinks(scratch)
	if err != nil {
		return prepared, err
	}
	native := c.Native
	native.Source = filepath.Join(scratch, "source")
	native.Config = filepath.Join(scratch, "answers.json")
	native.PersistentState = filepath.Join(scratch, "state.boltdb")
	native.Cache = filepath.Join(scratch, "cache")
	// Native v2.70.3 ignores removals for ignored files and rejects a target
	// simultaneously owned by an ordinary source and .chezmoiremove. Select the
	// canonical role-owned sources in this private copy; native still owns every
	// target, removal, diff and apply operation.
	var roles struct {
		Targets []struct {
			Source, Target string
			Roles          []string
		} `json:"role_targets"`
	}
	if err = json.Unmarshal(files["home/.chezmoidata/role-targets.json"], &roles); err != nil {
		return prepared, err
	}
	omitted := map[string]bool{}
	for _, target := range roles.Targets {
		if !slices.Contains(target.Roles, answers["machine_role"].(string)) {
			omitted["home/"+target.Source] = true
		}
	}
	for name, data := range files {
		if omitted[name] {
			continue
		}
		path := filepath.Join(native.Source, name)
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return prepared, err
		}
		if err = os.WriteFile(path, data, 0o600); err != nil {
			return prepared, err
		}
	}
	nativeAnswers := map[string]any{}
	for key, value := range answers {
		nativeAnswers[key] = value
	}
	nativeAnswers["workbench_managed"] = true
	// Setup's single private uv owner must also be usable by new user shells.
	// Existing native shell targets expose its directory without replacing
	// any unrelated ~/.local/bin command or introducing another installer.
	if uv := dependency(dependencies, "uv"); operation.Within(c.Paths.Data, uv) {
		nativeAnswers["workbench_uv_dir"] = filepath.Dir(uv)
	}
	config, _ := json.Marshal(map[string]any{"data": nativeAnswers})
	if err = os.WriteFile(native.Config, config, 0o600); err != nil {
		return prepared, err
	}
	if _, err = os.Lstat(c.Native.PersistentState); err == nil {
		data, readErr := operation.ReadPrivateInput(c.Native.PersistentState, 16<<20)
		if readErr != nil {
			return prepared, readErr
		}
		plan.Inputs = append(
			plan.Inputs,
			operation.Input{Name: "native-state", Digest: digest(data)},
		)
		if err = os.WriteFile(native.PersistentState, data, 0o600); err != nil {
			return prepared, err
		}
	} else if os.IsNotExist(err) {
		plan.Inputs = append(plan.Inputs, operation.Input{Name: "native-state", Digest: "absent"})
	} else {
		return prepared, err
	}
	// A private alias gives modify scripts the qualified interpreter even when
	// the installation exposes only a versioned basename. No global links.
	bin := filepath.Join(scratch, "bin")
	if err = os.Mkdir(bin, 0o700); err != nil {
		return prepared, err
	}
	if err = os.Symlink(
		dependency(dependencies, "python3"),
		filepath.Join(bin, "python3"),
	); err != nil {
		return prepared, err
	}
	// gh only affects a native lookPath branch; do not execute it in preview.
	if gh, findErr := operation.FindExecutable(
		"gh",
		os.Getenv("PATH"),
		[]string{c.Native.Source},
	); findErr == nil {
		if err = os.Symlink(gh, filepath.Join(bin, "gh")); err != nil {
			return prepared, err
		}
		plan.Inputs = append(
			plan.Inputs,
			operation.Input{Name: "gh-location", Digest: digest([]byte(gh))},
		)
	}
	environment := []string{
		"HOME=" + c.Native.Destination,
		"PATH=" + bin + ":/usr/bin:/bin",
		"LANG=C.UTF-8",
		"PYTHONNOUSERSITE=1",
		"PYTHONDONTWRITEBYTECODE=1",
	}
	prefix, err := native.Args()
	if err != nil {
		return prepared, err
	}
	prefix = append(
		prefix,
		"--config-format=json",
		"--no-tty",
		"--color=false",
		"--use-builtin-git=true",
	)
	run := func(args ...string) (string, error) {
		output, runErr := operation.Run(
			ctx,
			c,
			nil,
			operation.Process{
				Executable:    dependency(dependencies, "chezmoi"),
				Args:          append(slices.Clone(prefix), args...),
				Directory:     scratch,
				Environment:   environment,
				Secrets:       answers.secrets(),
				PrivateOutput: true,
				OutputLimit:   16 << 20,
			},
		)
		return output.Stdout, runErr
	}
	managed, err := run(
		"managed",
		"--exclude=scripts",
		"--nul-path-separator",
		"--path-style=absolute",
	)
	if err != nil {
		return prepared, err
	}
	targets := strings.Split(strings.TrimSuffix(managed, "\x00"), "\x00")
	slices.Sort(targets)
	containers := map[string]bool{}
	for _, target := range targets {
		if err = c.ValidateTarget(target); err != nil {
			if containerErr := c.ValidateContainer(target); containerErr != nil {
				return prepared, err
			}
			containers[target] = true
		}
		image, readErr := operation.ReadImage(c, target)
		if containers[target] {
			info, statErr := os.Lstat(target)
			readErr = statErr
			if statErr == nil {
				image = operation.Image{Kind: "directory", Mode: uint32(info.Mode().Perm())}
			}
		}
		if readErr != nil {
			return prepared, readErr
		}
		plan.Inputs = append(
			plan.Inputs,
			operation.Input{Name: target, Digest: operation.ImageDigest(image)},
		)
	}
	status, err := run("status", "--exclude=scripts", "--path-style=absolute")
	if err != nil {
		return prepared, err
	}
	for line := range strings.SplitSeq(status, "\n") {
		if line == "" {
			continue
		}
		if len(line) < 4 || line[2] != ' ' {
			return prepared, operation.Fail(1, "native", "Unexpected native status output")
		}
		target := line[3:]
		if containers[target] && line[1] != ' ' {
			return prepared, operation.Fail(
				3,
				"scope",
				"Native plan would change a protected ancestor directory; review its mode/type before applying",
			)
		}
		if !containers[target] {
			if err = c.ValidateTarget(target); err != nil {
				return prepared, err
			}
		}
		if !slices.Contains(targets, target) {
			image, readErr := operation.ReadImage(c, target)
			if readErr != nil {
				return prepared, readErr
			}
			plan.Inputs = append(
				plan.Inputs,
				operation.Input{Name: target, Digest: operation.ImageDigest(image)},
			)
		}
		action := map[byte]string{'A': "create", 'M': "modify", 'D': "remove", ' ': "unchanged"}[line[1]]
		if action == "" {
			return prepared, operation.Fail(1, "native", "Unsupported native target action")
		}
		if action != "unchanged" {
			plan.Edits = append(
				plan.Edits,
				operation.Edit{
					Path:        target,
					Action:      action,
					Description: "Native chezmoi configuration change (" + answers.label() + ")",
				},
			)
		}
	}
	// Use native built-in diff as the render/merge gate. Never publish its secret-
	// bearing bytes; paths/actions above are the public review surface.
	diff, err := run("diff", "--exclude=scripts")
	if err != nil {
		return prepared, err
	}
	plan.Inputs = append(
		plan.Inputs,
		operation.Input{Name: "native-diff", Digest: digest([]byte(diff))},
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
		plan.Effects = append(plan.Effects, ProvisioningEffects(answers)...)
		plan.Effects = append(plan.Effects, optional...)
	}
	rendered, renderErr := run("dump", "--exclude=scripts", "--format=json")
	if renderErr != nil {
		return prepared, renderErr
	}
	var desired map[string]struct {
		Type     string `json:"type"`
		Perm     uint32 `json:"perm"`
		Contents string `json:"contents"`
		Target   string `json:"target"`
	}
	if json.Unmarshal([]byte(rendered), &desired) != nil {
		return prepared, operation.Fail(1, "native", "Unexpected native target-image output")
	}
	for _, edit := range plan.Edits {
		before, readErr := operation.ReadImage(c, edit.Path)
		if readErr != nil {
			return prepared, readErr
		}
		after := operation.Image{Kind: "absent"}
		if edit.Action != "remove" {
			relative, relErr := filepath.Rel(c.Native.Destination, edit.Path)
			if relErr != nil {
				return prepared, relErr
			}
			entry, ok := desired[relative]
			if !ok {
				return prepared, operation.Fail(
					1,
					"native",
					"Native target missing from image enumeration",
				)
			}
			switch entry.Type {
			case "file":
				after = operation.Image{
					Kind: "file",
					Mode: entry.Perm,
					Data: []byte(entry.Contents),
				}
			case "dir":
				after = operation.Image{Kind: "directory", Mode: entry.Perm}
			case "symlink":
				after = operation.Image{Kind: "symlink", Mode: 0o777, Link: entry.Target}
			default:
				return prepared, operation.Fail(
					3,
					"native",
					"Native target type is unsupported for recovery",
				)
			}
		}
		after, err = operation.ImageWithGroup(c, edit.Path, after)
		if err != nil {
			return prepared, err
		}
		// Native atomic writes can initially inherit the scratch directory's
		// group. Until corrected under the checkpoint owner's exact-image gate,
		// never expose permissions available only to that transient group.
		if ((after.Mode>>3)&7) & ^(after.Mode&7) != 0 {
			return prepared, operation.Fail(
				3,
				"metadata",
				"Native group-exclusive permissions require separately qualified atomic ownership handling",
			)
		}
		if before.Kind == "file" || before.Kind == "symlink" {
			group, groupErr := operation.CreationGroup(c, edit.Path)
			if groupErr != nil {
				return prepared, groupErr
			}
			if edit.Action != "remove" && before.Group != nil && *before.Group != group {
				return prepared, operation.Fail(
					3,
					"metadata",
					"Native replacement cannot preserve this target's group ownership",
				)
			}
		}
		prepared.Changes = append(
			prepared.Changes,
			operation.TargetChange{Path: edit.Path, Before: before, After: after},
		)
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
	prepared.native, prepared.executable = native, dependency(dependencies, "chezmoi")
	prepared.environment, prepared.secrets = environment, answers.secrets()

	if err = release.PlanCandidate(c, plan); err != nil {
		return prepared, err
	}
	plan.Complete = true
	return prepared, nil
}

// Apply executes the prepared configuration through the same native owner.
// Callers must hold mutation authority and a durable checkpoint of any target changes.
// An interactive run owns terminal; otherwise redacted output goes to progress.
func (p *Prepared) Apply(
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
		return operation.Fail(3, "plan", "Native apply requires an open complete prepared plan")
	}
	if c.Native.PersistentState != filepath.Join(c.Paths.State, "chezmoi", "chezmoi.boltdb") {
		return operation.Fail(2, "state", "Unexpected native state target")
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
		environment = ScriptEnvironment(c, p.Plan.Dependencies)
		for _, name := range p.selection.Effects {
			environment = append(environment, effectVariable(name)+"=1")
		}
		for i, value := range environment {
			if strings.HasPrefix(value, "PATH=") {
				environment[i] = "PATH=" + filepath.Join(
					p.scratch,
					"bin",
				) + ":" + strings.TrimPrefix(
					value,
					"PATH=",
				)
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
