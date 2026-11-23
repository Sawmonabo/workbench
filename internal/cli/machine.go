package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Sawmonabo/workbench/internal/agent/costs"
	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/migrate"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

func doctorCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check Workbench, your runtimes, and the agent costs and agent guard hooks, without repair",
		Long: "Show what Workbench has installed and applied, the steps you skipped or " +
			"selected, its tools and the host; then Node, Python, Rust, Go and bun against " +
			"their pins, the agent costs and agent guard hooks in Claude Code and Codex, " +
			"whether the agent guard's watcher runs, and an agent costs or guard folder still at " +
			"its old path. It reads only this machine and repairs nothing. A missing " +
			"prerequisite returns nonzero; a missing runtime or hook does not, and workbench " +
			"apply fixes it.",
		Example: "  workbench doctor\n" +
			"  workbench doctor --json",
		Args: cobra.NoArgs,
		RunE: o.action(
			machineAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				result := operation.NewResult(cmd.CommandPath())
				components, err := machine.Doctor(cmd.Context(), c)
				result.Results = append(
					[]operation.Component{{
						Name:    "workbench",
						Status:  operation.StatusComplete,
						Message: shownVersion(),
					}},
					components...,
				)
				result.Results = append(result.Results, agentHooks(c.Home)...)
				result.Results = append(result.Results, guardWatcher(cmd.Context()))
				result.Results = append(result.Results, oldAgentFolders(c)...)
				// A check that found nothing wrong reads ok, not the complete
				// or unchanged of a step that ran.
				for i, line := range result.Results {
					if line.Status == operation.StatusComplete ||
						line.Status == operation.StatusUnchanged {
						result.Results[i].Status = operation.StatusOK
					}
				}
				if err == nil {
					result.Summary = doctorSummary(result.Results)
				}
				return result, err
			},
		),
	}
	o.localBuildFlag(cmd)
	return cmd
}

// doctorSummary is "Every check passed", or, when lines that block nothing
// still need a look, names them.
func doctorSummary(components []operation.Component) string {
	var look []string
	for _, component := range components {
		switch component.Status {
		case operation.StatusOK, operation.StatusSkipped:
		default:
			look = append(look, component.Name)
		}
	}
	if len(look) == 0 {
		return "[WorkBench] Every check passed"
	}
	return "[WorkBench] Nothing blocks Workbench; look at " + strings.Join(look, ", ")
}

// agentHooks is one doctor line per agent: whether Claude Code and Codex run
// the agent costs and agent guard hooks Workbench installs, read the way
// workbench agent costs status reads them. A missing hook is absent, and one
// the agent does not run (Codex's untrusted or disabled) is a conflict;
// neither blocks Workbench, and workbench apply installs them and, in Codex,
// trusts them.
func agentHooks(home string) []operation.Component {
	var lines []operation.Component
	for _, tool := range costs.Tools {
		if tool.Source == nil {
			continue
		}
		groups := []struct {
			command string
			hooks   []costs.HookCommand
		}{
			{"agent costs", costs.IngestHooks},
			{"agent guard", guard.Hooks(tool.Name)},
		}
		var absent, idling []string
		none, untrusted := true, false
		for _, group := range groups {
			var missing, idle []string
			for i, state := range tool.Source.Hooks(home, group.hooks) {
				switch state {
				case costs.HookOK:
				case costs.HookMissing:
					missing = append(missing, group.hooks[i].Event)
				default:
					untrusted = untrusted || state == costs.HookUntrusted
					idle = append(idle, group.hooks[i].Event+" "+state)
				}
			}
			none = none && len(missing) == len(group.hooks)
			if len(missing) > 0 {
				absent = append(absent, fmt.Sprintf(
					"%d of %d %s hooks (%s)",
					len(missing), len(group.hooks), group.command, strings.Join(missing, ", "),
				))
			}
			if len(idle) > 0 {
				idling = append(idling, fmt.Sprintf(
					"%d of %d %s hooks (%s)",
					len(idle), len(group.hooks), group.command, strings.Join(idle, ", "),
				))
			}
		}
		line := operation.Component{
			Name:    "agent-hooks-" + tool.Name,
			Status:  operation.StatusComplete,
			Message: "agent costs and agent guard hooks are installed",
		}
		var problems []string
		if len(absent) > 0 {
			line.Status = operation.StatusAbsent
			problems = append(problems, "is missing "+strings.Join(absent, " and "))
		}
		if len(idling) > 0 {
			if line.Status == operation.StatusComplete {
				line.Status = operation.StatusConflict
			}
			problems = append(problems, "does not run "+strings.Join(idling, " and "))
		}
		switch {
		case none:
			line.Message = tool.Title + " has no agent costs or agent guard hooks"
		case len(problems) > 0:
			line.Message = tool.Title + " " + strings.Join(problems, " and ")
		}
		// Codex runs a hook only once it is recorded as trusted, which the
		// apply does for the hooks it installs.
		var fixes []string
		if len(absent) > 0 {
			fixes = append(fixes, "installs")
		}
		if untrusted || (len(absent) > 0 && tool.Name == "codex") {
			fixes = append(fixes, "trusts")
		}
		// Workbench installs nothing in a folder a variable moved, which
		// doctor's own line for the move explains.
		moved := slices.ContainsFunc(costs.ConfigMoves(home), func(m costs.ConfigMove) bool {
			return m.Tool == tool.Title && m.Source != ""
		})
		if len(fixes) > 0 && !moved {
			line.Message += "; workbench apply " + strings.Join(fixes, " and ") + " them"
		}
		lines = append(lines, line)
	}
	return lines
}

// guardWatcher is the doctor line for the guard's watcher: whether one runs,
// told from its lock, as every entry tells it (see guard.WatcherRuns), and,
// when one does, whether it answers on its socket. The watcher starts with
// an agent session and exits when idle, so one that is not running is no
// problem. A lock held with no answer is said as the view says it (see
// guard.WatcherSilent).
func guardWatcher(ctx context.Context) operation.Component {
	line := operation.Component{
		Name:    "agent-guard-watcher",
		Status:  operation.StatusComplete,
		Message: "running",
	}
	runs, err := guard.WatcherRuns()
	switch {
	case err != nil:
		line.Status = operation.StatusConflict
		line.Message = "cannot tell whether the watcher runs: " + err.Error()
		return line
	case !runs:
		line.Status = operation.StatusSkipped
		line.Message = "not running; the next agent session starts it, and it exits when idle"
		return line
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	client, err := guard.Dial(ctx)
	if err != nil {
		line.Status = operation.StatusConflict
		line.Message = guard.WatcherSilent
		if !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ECONNREFUSED) {
			line.Message += " (" + err.Error() + ")"
		}
		return line
	}
	_ = client.Close()
	return line
}

// oldAgentFolders is the doctor line for an agent costs or guard folder still
// at its old path, which migration 0007 moves under agent/: it names each and
// why it stays (see [migrate.AgentFoldersLeft]). There is no line once every
// folder has moved.
func oldAgentFolders(c operation.Context) []operation.Component {
	status, text := migrate.AgentFoldersLeft(migrate.Locations{Home: c.Home, Runtime: c.Paths})
	if text == "" {
		return nil
	}
	if status == operation.StatusPending {
		text += "; workbench migrate, or the next apply, does it, or says why a folder stays"
	}
	return []operation.Component{{Name: "agent-folders", Status: status, Message: text}}
}

// applyCommand makes this machine match the installed release, or the local
// checkout, after a checklist of what would change.
func applyCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Show what would change on this machine as a checklist, then apply what is checked",
		Long: "Install Workbench's tools when missing, ask the machine questions your saved " +
			"answers lack, then plan every file change and setup step for this machine. The " +
			"first apply, and any apply that finds something new to decide or a file Workbench " +
			"owns edited outside it, shows the checklist: space unchecks a step, a applies. " +
			"Your choices are remembered, so a later apply with nothing new prints the plan and " +
			"applies them without asking.\n\n" +
			"--choose shows the checklist anyway, --reset forgets your choices and asks again " +
			"with the defaults, and --dry-run only shows the plan. --yes never asks, taking your " +
			"choice or the default for anything new; it cannot be combined with --choose, " +
			"--reset or --approve-plan. Without a terminal, apply runs only with --approve-plan " +
			"DIGEST or --yes. A coding agent (Claude Code, Codex) is refused --yes and --choose " +
			"and must pass --approve-plan DIGEST from a --dry-run --json plan.\n\n" +
			"On a Mac, apply may ask once for your password, for Homebrew and the touch-id-sudo " +
			"step, and keeps it in memory for that apply only; where Touch ID for sudo is on, " +
			"sudo asks by Touch ID instead. docs/usage.md says when it asks and what an apply " +
			"without a terminal needs.",
		Example: "  workbench apply --dry-run\n" +
			"  workbench apply\n" +
			"  workbench apply --choose\n" +
			"  workbench apply --dry-run --json\n" +
			"  workbench apply --approve-plan DIGEST",
		Args: cobra.NoArgs,
		RunE: o.action(
			nativeAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
					return machinePlan(cmd, c, o)
				}
				if err := checkApply(o); err != nil {
					return operation.NewResult(cmd.CommandPath()), err
				}
				c.ReadOnly = false
				result, plan, err := applyMachine(cmd, c, o)
				if err == nil {
					result.Summary = appliedSummary(result, plan)
					// This shell started before the apply wrote the shell setup that
					// finds workbench and the tools; only a new terminal reads it.
					switch shell := changedShell(c, plan); {
					case c.RecordsHome() && !c.BinOnPath():
						result.Summary += "; open a new terminal so your shell finds workbench and the other tools"
					case shell != "":
						// A running shell read its startup file when it started.
						result.Summary += "; open a new terminal, or run exec " + shell +
							", to use the updated shell setup"
					}
				}
				return result, err
			},
		),
	}
	cmd.Flags().Bool("dry-run", false, "Show the checklist without installing, asking or applying")
	cmd.Flags().BoolVarP(
		&o.yes,
		"yes",
		"y",
		false,
		"Apply the saved selection without asking (refused for coding agents)",
	)
	cmd.Flags().
		BoolVar(&o.choose, "choose", false, "Show the checklist with your saved choices, even when nothing is new")
	cmd.Flags().
		BoolVar(&o.reset, "reset", false, "Forget your saved choices and ask again with the defaults")
	o.localBuildFlag(cmd)
	o.destinationFlag(cmd)
	o.approveFlag(cmd)
	return cmd
}

// checkApply is apply's one consent gate. It runs before anything else
// happens, so a run that is certain to be refused downloads no tools and
// touches no machine.toml. Flags that contradict each other are an invalid
// invocation (exit 2), and [checkConsent] then refuses a run nobody approved.
// --reset forgets the saved choices, so --yes would turn every step the owner
// skipped back on and apply it unasked.
func checkApply(o *options) error {
	switch {
	case o.reset && o.yes:
		return operation.Fail(
			operation.ExitInvalid,
			"reset",
			"--reset asks and --yes does not; pick one",
		)
	case o.choose && o.yes:
		return operation.Fail(
			operation.ExitInvalid,
			"choose",
			"--choose asks and --yes does not; pick one",
		)
	case o.choose && o.approvePlan != "":
		return operation.Fail(
			operation.ExitInvalid,
			"choose",
			"--choose asks and --approve-plan does not; pick one",
		)
	case o.choose && !o.interactive():
		return operation.Fail(
			operation.ExitInvalid,
			"choose",
			"--choose asks at a terminal; drop --json and --non-interactive",
		)
	case o.choose && !operation.AgentSession() && !hasTerminal():
		return operation.Fail(operation.ExitBlocked, "choose", "--choose needs a terminal")
	}
	return checkConsent(o, "apply")
}

// needsChoice reports whether apply must show the checklist rather than apply
// the saved selection: the owner has never decided anything (selection has no
// decided list), a non-fixed effect the owner has not decided yet has something
// to do (one the probe found nothing to change cannot be chosen, so it waits
// until it has something), or a file Workbench owns whole was edited outside
// it. Merged files never count, since Claude Code and Codex rewrite theirs
// constantly.
func needsChoice(plan operation.Plan, selection machine.Selection) bool {
	return selection.NeverDecided() ||
		slices.ContainsFunc(plan.Effects, func(effect operation.Effect) bool {
			return effect.New && !effect.Fixed && !plan.AlreadySet(effect)
		}) || slices.ContainsFunc(plan.Edits, func(edit operation.Edit) bool {
		return edit.EditedOutside && !edit.Merged
	})
}

// machineSelection is the saved selection, or an empty one with --reset.
func machineSelection(c operation.Context, o *options) (machine.Selection, error) {
	if o.reset {
		return machine.Selection{Forget: true}, nil
	}
	return machine.ReadSelection(c.Native.Config)
}

// machinePlan shows the checklist without applying it.
func machinePlan(cmd *cobra.Command, c operation.Context, o *options) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	selection, err := machineSelection(c, o)
	if err != nil {
		return result, err
	}
	plan, err := machine.Plan(cmd.Context(), c, selection)
	if operation.ExitCode(err) == operation.ExitInterrupted {
		return result, err // an interrupted plan is incomplete; show only the error
	}
	status := operation.StatusComplete
	if err != nil {
		status = operation.StatusBlocked
	}
	result.Results = append(
		result.Results,
		operation.Component{Name: "machine-plan", Status: status, Details: plan},
	)
	if err != nil {
		return result, err
	}
	result.PlanDigest = plan.Digest()
	return result, nil
}

// applyMachine installs Workbench's tools when missing and asks the machine
// questions the saved answers lack, refreshes Homebrew's package list unless a
// plan digest was approved, plans, gets the selection approved and applies it.
// A local checkout installs the tools its own versions.toml pins when missing
// and uses the saved answers as they are.
func applyMachine(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
) (operation.Result, operation.Plan, error) {
	result := operation.NewResult(cmd.CommandPath())
	terminal, progress, closeConsole := nativeConsole(o, cmd.ErrOrStderr())
	defer closeConsole()
	var err error
	if c.Native.Developer {
		// A checkout uses the saved answers as they are, but installs the tools
		// its versions.toml pins when they are missing.
		c, err = setUp(cmd, c, o, nil, &result, false, nil)
	} else {
		c, err = setUp(cmd, c, o, terminal, &result, true, nil)
	}
	if err != nil {
		return result, operation.Plan{}, err
	}
	selection, err := machineSelection(c, o)
	if err != nil {
		return result, operation.Plan{}, err
	}
	refreshed, err := refreshHomebrew(cmd.Context(), c, o, &result)
	if err != nil {
		return result, operation.Plan{}, err
	}
	plan, err := machine.Plan(cmd.Context(), c, selection)
	if err != nil {
		return result, plan, err
	}
	consent := consentFor(o, o.approvePlan)
	// checkApply already refused every run that is neither approved by digest,
	// --yes nor at a person's terminal, so the cases below that ask, or apply
	// the saved selection unasked, have a person to read the plan.
	switch {
	case o.approvePlan != "":
	case o.yes:
		// --yes never asks: undecided effects take the saved choice or the
		// default and are recorded as decided by the apply.
		consent.ApprovedDigest = plan.Digest()
		if !o.json {
			// Undecided effects take their defaults: show the whole plan so
			// they are visible, not the compact saved-choices view.
			if err = writePlanView(
				cmd.OutOrStdout(),
				plan,
				o.verbose,
				!needsChoice(plan, selection),
			); err != nil {
				return result, plan, err
			}
		}
	case !o.choose && !o.reset && !needsChoice(plan, selection):
		// Nothing new to decide: show the plan and apply the saved selection.
		if err = writePlanView(cmd.OutOrStdout(), plan, o.verbose, true); err != nil {
			return result, plan, err
		}
		consent.ApprovedDigest = plan.Digest()
	default:
		// Tag saved skips from machine.toml even under --reset: Apply's recheck
		// reads the file the same way, and SavedSkip is part of the digest the
		// approval must equal.
		saved, savedErr := machine.ReadSelection(c.Native.Config)
		if savedErr != nil {
			return result, plan, savedErr
		}
		checked, approved, chooseErr := choosePlan(cmd.Context(), plan, o.verbose)
		if chooseErr != nil {
			return result, plan, chooseErr
		}
		if !approved {
			return result, plan, operation.Fail(
				operation.ExitBlocked,
				"consent",
				"Plan was not approved; no changes made",
			)
		}
		for i := range plan.Effects {
			if !plan.Effects[i].Fixed {
				plan.Effects[i].Checked = slices.Contains(checked, plan.Effects[i].Name)
			}
		}
		selection = machine.SelectionOf(plan.Effects)
		selection.Forget = o.reset
		plan = machine.Reselect(plan, selection, saved)
		consent.ApprovedDigest = plan.Digest()
	}
	result.PlanDigest = plan.Digest()
	applied, err := machine.Apply(
		cmd.Context(), c, selection, plan, consent, terminal, progress, refreshed,
	)
	result.Results = append(result.Results, applied.Results...)
	result.OperationID = applied.OperationID
	return result, plan, err
}

// refreshHomebrew refreshes Homebrew's package list before apply plans, so the
// updates the plan lists are current, and reports whether it did. A run that
// approves a plan digest never refreshes: the digest was computed from the list
// as it was, so a refresh would change the plan under the approval, and the
// brew-maintenance step refreshes at the end of such a run instead. A refresh
// that fails only warns, and apply plans from the list as it is; an interrupt
// stops apply.
func refreshHomebrew(
	ctx context.Context,
	c operation.Context,
	o *options,
	result *operation.Result,
) (bool, error) {
	if o.approvePlan != "" {
		return false, nil
	}
	refreshed, err := machine.RefreshHomebrew(ctx, c)
	if operation.ExitCode(err) == operation.ExitInterrupted {
		return false, err
	}
	if err != nil {
		result.Warnings = append(
			result.Warnings,
			"Homebrew's package list was not refreshed, so the updates listed may be out of date; "+
				"run brew update to see why",
		)
	}
	return refreshed, nil
}

// changedShell is the shell whose startup file in the real home the plan
// changed ("zsh", "bash"), "" when none.
func changedShell(c operation.Context, plan operation.Plan) string {
	if !c.RecordsHome() {
		return ""
	}
	for _, edit := range plan.Edits {
		for file, shell := range map[string]string{".zshrc": "zsh", ".bashrc": "bash"} {
			if edit.Path == file || edit.Path == filepath.Join(c.Home, file) {
				return shell
			}
		}
	}
	return ""
}

// appliedSummary is the branded result line. Apply's early return, with no
// file to write and no selection to save, reports itself by its message.
func appliedSummary(result operation.Result, plan operation.Plan) string {
	for _, component := range result.Results {
		if component.Message == machine.NothingToApply {
			return "[WorkBench] Nothing to apply; this machine already matches"
		}
	}
	// The steps that ran, counted as the plan header counts the steps that
	// will run, so the two agree: an already-set step is in neither.
	_, effects, _ := planCounts(plan)
	skipped := 0
	for _, effect := range plan.Effects {
		if !effect.Fixed && !effect.Checked && !plan.AlreadySet(effect) {
			skipped++
		}
	}
	line := fmt.Sprintf(
		"[WorkBench] Applied: %d file%s, %d step%s",
		plan.Files(), plural(plan.Files()), effects, plural(effects),
	)
	if skipped > 0 {
		line += fmt.Sprintf("; %d skipped", skipped)
	}
	return line
}
