package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

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
				if err := checkApply(cmd, c, o); err != nil {
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
						result.Body = operation.Explain(
							operation.Line{
								Label: "shell",
								Text:  "a new terminal's shell finds workbench and the other tools",
							},
							operation.NextText("open a new terminal"),
						)
					case shell != "":
						// A running shell read its startup file when it started.
						result.Body = operation.Explain(
							operation.Line{
								Label: "shell",
								Text:  "a new shell or a new terminal uses the updated shell setup",
							},
							operation.NextText("exec "+shell),
						)
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
// invocation (exit 2), whose fix is the run that asks, and [checkConsent]
// then refuses a run nobody approved. --reset forgets the saved choices, so
// --yes would turn every step the owner skipped back on and apply it
// unasked.
func checkApply(cmd *cobra.Command, c operation.Context, o *options) error {
	args := cmd.Flags().Args()
	switch {
	case o.reset && o.yes:
		return operation.Fail(
			operation.ExitInvalid,
			"reset",
			"--reset asks and --yes does not",
			runAgain(cmd, c, args, "yes"),
		)
	case o.choose && o.yes:
		return operation.Fail(
			operation.ExitInvalid,
			"choose",
			"--choose asks and --yes does not",
			runAgain(cmd, c, args, "yes"),
		)
	case o.choose && o.approvePlan != "":
		return operation.Fail(
			operation.ExitInvalid,
			"choose",
			"--choose asks and --approve-plan does not",
			runAgain(cmd, c, args, "approve-plan"),
		)
	case o.choose && !o.interactive():
		return operation.Fail(
			operation.ExitInvalid,
			"choose",
			"--choose asks at a terminal",
			runAgain(cmd, c, args, "json", "non-interactive"),
		)
	case o.choose && !operation.AgentSession() && !hasTerminal():
		return operation.Fail(operation.ExitBlocked, "choose", "--choose needs a terminal")
	}
	return checkConsent(cmd, c, o)
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
// and uses the saved answers as they are. An approved digest is held first
// against apply --dry-run's plan (see approvedPlan), so one that does not
// match is refused before setup.
func applyMachine(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
) (operation.Result, operation.Plan, error) {
	result := operation.NewResult(cmd.CommandPath())
	plan, err := approvedPlan(cmd, c, o)
	if err != nil {
		if plan.Complete {
			result.PlanDigest = plan.Digest()
		}
		return result, plan, err
	}
	if err = machine.RefuseUnfinished(c); err != nil {
		return result, plan, err
	}
	terminal, progress, closeConsole := nativeConsole(o, cmd.ErrOrStderr())
	defer closeConsole()
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
	if o.approvePlan == "" {
		if plan, err = machine.Plan(cmd.Context(), c, selection); err != nil {
			return result, plan, err
		}
	}
	consent := consentFor(cmd, c, o, o.approvePlan)
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
		checked, approved, chooseErr := choosePlan(cmd.Context(), plan, o.verbose, consent.Replan)
		if chooseErr != nil {
			return result, plan, chooseErr
		}
		if !approved {
			return result, plan, operation.Fail(
				operation.ExitBlocked,
				"consent",
				"Plan was not approved",
				operation.Changed("nothing"),
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

// approvedPlan is, for an apply given --approve-plan DIGEST, the plan as apply
// --dry-run makes it, from the machine as it is, and an error unless DIGEST
// is that plan's: setup has not run, so one that does not match is refused
// before anything is downloaded, installed, saved, locked or given a mode, but
// for a folder of Workbench's own files its owner may not read or search,
// which the plan reads and so gives 0700, and a tool the plan needs that is
// missing gets the dry run's refusal. The plan that matches is the one
// applied, which the apply checks again under its locks. Without
// --approve-plan it plans nothing and gives an empty plan.
func approvedPlan(cmd *cobra.Command, c operation.Context, o *options) (operation.Plan, error) {
	if o.approvePlan == "" {
		return operation.Plan{}, nil
	}
	c.ReadOnly = true
	selection, err := machineSelection(c, o)
	if err != nil {
		return operation.Plan{}, err
	}
	plan, err := machine.Plan(cmd.Context(), c, selection)
	if err != nil {
		return plan, err
	}
	return plan, operation.CheckApproval(o.approvePlan, plan.Digest(), replan(cmd, c)...)
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
			operation.Warn(
				"Homebrew's package list was not refreshed",
				operation.Outcome("the updates listed may be out of date"),
				operation.Fix("brew", "update"),
			),
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
			return brandTag + " Nothing to apply; this machine already matches"
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
	line := brandTag + " Applied: " + display.Count(plan.Files(), "file") + ", " +
		display.Count(effects, "step")
	if skipped > 0 {
		line += fmt.Sprintf("; %d skipped", skipped)
	}
	return line
}
