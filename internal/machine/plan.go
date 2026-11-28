package machine

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
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
	prepared, err := prepare(ctx, c, selection, nil)
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
	// dormant names the effects whose scripts chezmoi would not run; see
	// activeEffects.
	dormant map[string]bool
	// appUpdates and toolUpdates name the casks and formulae the plan's
	// Homebrew update effects cover, and agentCasks the Codex and Claude Code
	// casks among the casks Homebrew is asked about.
	appUpdates, toolUpdates, agentCasks []string
	// agentInstalls are the copies of Codex and Claude Code the shell runs,
	// in agents order (see findAgents), and agentUpdates the updates the
	// plan's other update effects cover.
	agentInstalls []agentInstall
	agentUpdates  []agentUpdate
	// brewRefreshed is, for Apply, whether Homebrew's index was refreshed
	// before planning; the apply scripts then leave it be.
	brewRefreshed bool
	// askpass is, for Apply, the SUDO_ASKPASS helper that answers Homebrew's and
	// the Touch ID step's sudo requests during an apply at a terminal on a Mac,
	// or "" for none (also where sudo asks for Touch ID itself).
	askpass string
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

// prepare plans the machine configuration. shown is nil for a plan, which
// probes the scripts, and for apply's recheck the plan shown for approval,
// whose probe findings stand in for a probe.
func prepare(
	ctx context.Context,
	c operation.Context,
	selection Selection,
	shown *operation.Plan,
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
	// The runtime folders the scripts and migrations write into are bound
	// too, as the diff does not show them.
	folders := strings.Join(c.Paths.All(), "\n")
	plan.Inputs = append(
		plan.Inputs,
		operation.Input{Name: "native-diff", Digest: operation.SHA256Hex([]byte(diff))},
		operation.Input{Name: "folders", Digest: operation.SHA256Hex([]byte(folders))},
	)
	effects := []operation.Effect{aiSecurityEffect}
	effects = append(effects, provisioningEffects(c, answers, requirements)...)
	prepared.active, err = prepared.activeSources(ctx, c)
	if err != nil {
		return prepared, err
	}
	plan.Effects = append(plan.Effects, prepared.activeEffects(effects)...)
	plan.Effects = append(plan.Effects, hostOptionalEffects()...)
	markHomebrewInstall(plan.Effects)
	if err = prepared.buildChanges(ctx, c); err != nil {
		return prepared, err
	}
	// An isolated destination provisions nothing, so it has no updates to
	// look up, and no agent reads its settings from it.
	if c.Native.Destination == c.Home {
		prepared.planAgentUpdates(ctx, c, files, answers["versions_mode"] == "latest")
		prepared.planUpdates(ctx, c, files)
	}
	if err = prepared.selectEffects(ctx, c, selection, shown); err != nil {
		return prepared, err
	}
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

// selectEffects probes the scripts for their deltas, unless shown has them
// already, drops the dormant effects with nothing to do (see [listDue]), lists
// the take-overs they call for, then checks each effect as
// the selection and the saved skips say. On WSL the writer record the scripts
// follow is an input. An isolated destination gets files only: scripts
// provision the real home.
func (p *preparation) selectEffects(
	ctx context.Context,
	c operation.Context,
	selection Selection,
	shown *operation.Plan,
) error {
	if platform.WSL() && c.Native.Destination == c.Home {
		writers := windowsWriters(ctx, c)
		p.Plan.Inputs = append(p.Plan.Inputs, writers)
		if writers.Digest == "unknown" {
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				operation.Warn("Couldn't read the Windows writer record"),
			)
		}
	}
	if shown == nil {
		if err := p.probeEffects(ctx, c); err != nil {
			return err
		}
		shown = &p.Plan
	}
	// A ticked step left unlisted for having nothing to do is still offered,
	// so its saved tick is not stale.
	offered := slices.Clone(p.Plan.Effects)
	p.Plan.Effects = listDue(p.Plan.Effects, p.dormant, shown.Effects)
	p.Plan.Unlisted = nil
	for _, effect := range offered {
		if !slices.ContainsFunc(p.Plan.Effects, func(listed operation.Effect) bool {
			return listed.Name == effect.Name
		}) {
			p.Plan.Unlisted = append(p.Plan.Unlisted, effect.Name)
		}
	}
	p.Plan.Effects = append(p.Plan.Effects, takeOverEffects(shown.Effects)...)
	saved, err := ReadSelection(c.Native.Config)
	if err != nil {
		return err
	}
	if !selection.Forget {
		p.Plan.Warnings = append(p.Plan.Warnings, staleSelectionWarnings(saved, offered)...)
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
			operation.Warn(
				"Isolated destination: provisioning effects are unchecked and only files apply",
			),
		)
	}
	return nil
}

func recoveryLimits() []string {
	limits := []string{
		"Revert puts back the saved files only, not packages, extensions, the registry, services or script writes",
		fmt.Sprintf(
			"At %d checkpoints the plan lists the oldest finished one for removal",
			operation.MaxForwardCheckpoints,
		),
	}
	if platform.WSL() {
		limits = append(
			limits,
			"Revert leaves the Windows Terminal settings, PowerShell profile, .wslconfig and default distribution",
			"Revert leaves "+writerRecord+", which is safe to delete",
		)
	}
	return limits
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
	dependencies, results, refusal := resolveDependencies(
		ctx,
		c,
		requirements,
		recorded,
		os.Getenv("PATH"),
	)
	plan.Dependencies = dependencies
	for _, result := range results {
		if result.Status != operation.StatusComplete {
			plan.Prerequisites = append(plan.Prerequisites, operation.Warning{
				Message: result.Name + ": " + result.Message,
				Body:    result.Body,
			})
		}
	}
	if refusal != nil {
		return nil, operation.FailAs(operation.ExitBlocked, "prerequisites", refusal)
	}
	if len(plan.Prerequisites) > 0 {
		missing := "Workbench's tools are missing"
		if c.Native.Developer {
			missing = "This checkout's pinned tools are not installed"
		}
		return nil, operation.Fail(
			operation.ExitBlocked,
			"prerequisites",
			missing,
			InstallFix(c)...)
	}
	if platform := checkPlatform(ctx, c); platform.Status != operation.StatusComplete {
		plan.Prerequisites = append(
			plan.Prerequisites,
			operation.Warning{Message: platform.Message, Body: platform.Body},
		)
		return nil, operation.FailWith(
			operation.ExitBlocked,
			"platform",
			platform.Message,
			platform.Body,
		)
	}
	// A machine.toml that is there but refused is said as its refusal, with
	// the exit code and category of missing answers: only a missing one is
	// missing answers.
	answersRaw, found, err := lookupMachineConfig(c.Native.Config)
	if err != nil {
		return nil, operation.FailAs(operation.ExitBlocked, "answers", err)
	}
	if !found {
		command := c.WorkbenchCommand()
		fixes := []operation.Part{
			operation.FixText("run " + command + " apply at a terminal"),
			operation.FixText("or " + command + " answers import FILE"),
		}
		if c.Native.Developer {
			// apply --local-build never asks the questions.
			fixes = []operation.Part{operation.FixText(command + " answers import FILE")}
		}
		return nil, operation.Fail(
			operation.ExitBlocked,
			"answers",
			"Machine answers are missing",
			fixes...)
	}
	answers, err := parseAnswers(c, answersRaw)
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
