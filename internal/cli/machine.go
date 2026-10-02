package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

func doctorCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check what is installed and applied, tool versions and the host, without repair",
		Args:  cobra.NoArgs,
		RunE: o.action(
			machineAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				result := operation.NewResult(cmd.CommandPath())
				components, err := machine.Doctor(cmd.Context(), c)
				result.Results = append(
					[]operation.Component{{
						Name:    "workbench",
						Status:  operation.StatusComplete,
						Message: version(),
					}},
					components...,
				)
				if err == nil {
					result.Summary = "[WorkBench] Every check passed"
				}
				return result, err
			},
		),
	}
	o.localBuildFlag(cmd)
	return cmd
}

// initCommand adopts machine answers from an existing chezmoi config once, so a
// dotfiles machine switches without re-answering the questionnaire, or asks
// saved answers again.
func initCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Adopt answers from an existing chezmoi config, or ask a saved answer again",
		Args:  cobra.NoArgs,
	}
	o.approveFlag(cmd)
	cmd.Flags().
		String("answers-from", "", "Existing chezmoi config, for example ~/.config/chezmoi/chezmoi.toml")
	cmd.Flags().StringSlice(
		"ask",
		nil,
		"Ask these saved machine answers again, for example --ask machine_role",
	)
	cmd.Flags().Bool("dry-run", false, "Show the plan without saving answers")
	cmd.RunE = o.action(
		machineAction,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			ask, _ := cmd.Flags().GetStringSlice("ask")
			from, _ := cmd.Flags().GetString("answers-from")
			if (from == "") == (len(ask) == 0) {
				return result, operation.Fail(
					operation.ExitInvalid,
					"init",
					"init takes --answers-from FILE or --ask KEY",
				)
			}
			if len(ask) > 0 {
				return askAnswers(cmd, c, o, result, ask)
			}
			var encoded []byte
			planner := func(_ context.Context, current operation.Context) (operation.Plan, error) {
				plan, data, err := machine.AdoptionPlan(current, from)
				encoded = data
				return plan, err
			}
			plan, err := planner(cmd.Context(), c)
			result.Results = append(
				result.Results,
				operation.Component{
					Name:    "answers-plan",
					Status:  operation.StatusComplete,
					Details: plan,
				},
			)
			if err != nil {
				return result, err
			}
			result.PlanDigest = plan.Digest()
			if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun || len(plan.Edits) == 0 {
				return result, nil
			}
			c.ReadOnly = false
			err = operation.WithMutation(
				cmd.Context(),
				c,
				plan,
				consentFor(o, o.approvePlan),
				planner,
				func(m *operation.Mutation) error {
					return m.WritePrivate(filepath.Join(c.Paths.Config, "machine.toml"), encoded)
				},
			)
			if err == nil {
				shownAtPrompt(o, &result.Results[0])
				result.Results = append(
					result.Results,
					operation.Component{
						Name:    "answers",
						Status:  operation.StatusComplete,
						Message: "Saved; apply now uses them",
					},
				)
				result.Summary = "[WorkBench] Saved your machine answers; run workbench apply"
			}
			return result, err
		},
	)
	return cmd
}

// askAnswers asks the named saved machine answers again, at a terminal.
func askAnswers(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	result operation.Result,
	ask []string,
) (operation.Result, error) {
	if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
		return result, operation.Fail(
			operation.ExitInvalid,
			"ask",
			"--ask saves the answers it asks; drop --dry-run",
		)
	}
	if !o.interactive() {
		return result, operation.Fail(operation.ExitInvalid, "ask", "--ask needs a terminal")
	}
	if err := selectSource(&c); err != nil {
		return result, err
	}
	if err := machine.CheckAsk(c.Native.Config, ask); err != nil {
		return result, err
	}
	terminal, _, closeConsole := nativeConsole(o, cmd.ErrOrStderr())
	defer closeConsole()
	c.ReadOnly = false
	if _, err := setUp(cmd, c, o, terminal, &result, true, ask); err != nil {
		return result, err
	}
	result.Summary = "[WorkBench] Saved your machine answers; run workbench apply"
	return result, nil
}

// applyCommand makes this machine match the installed release, or the local
// checkout, after a checklist of what would change.
func applyCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Show what would change on this machine as a checklist, then apply what is checked",
		Long: "Install Workbench's tools when missing and ask the machine questions your saved " +
			"answers lack, then plan every file change and provisioning effect for this machine. " +
			"The first apply, and any apply that finds something new to decide or a file Workbench " +
			"owns edited outside it, shows the checklist: space unchecks a step, a applies. Your " +
			"choices are remembered, so a later apply with nothing new prints the plan and applies " +
			"them without asking. --choose shows the checklist anyway; --reset forgets the " +
			"remembered choices and asks again with the defaults; --yes never asks, taking the " +
			"remembered choice or the default for what is new; --dry-run only shows the plan.",
		Args: cobra.NoArgs,
		RunE: o.action(
			nativeAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
					return machinePlan(cmd, c, o)
				}
				if err := checkChoose(o); err != nil {
					return operation.NewResult(cmd.CommandPath()), err
				}
				c.ReadOnly = false
				result, plan, err := applyMachine(cmd, c, o, nil)
				if err == nil {
					result.Summary = appliedSummary(result, plan)
				}
				return result, err
			},
		),
	}
	cmd.Flags().Bool("dry-run", false, "Show the checklist without installing, asking or applying")
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "Apply the saved selection without asking")
	cmd.Flags().
		BoolVar(&o.choose, "choose", false, "Show the checklist with your saved choices, even when nothing is new")
	cmd.Flags().
		BoolVar(&o.reset, "reset", false, "Forget your saved choices and ask again with the defaults")
	o.localBuildFlag(cmd)
	o.destinationFlag(cmd)
	o.approveFlag(cmd)
	return cmd
}

// checkChoose refuses --choose where it cannot ask: with --yes or
// --approve-plan, which never prompt, or without a terminal.
func checkChoose(o *options) error {
	switch {
	case !o.choose:
		return nil
	case o.yes:
		return operation.Fail(
			operation.ExitInvalid,
			"choose",
			"--choose asks and --yes does not; pick one",
		)
	case o.approvePlan != "":
		return operation.Fail(
			operation.ExitInvalid,
			"choose",
			"--choose asks and --approve-plan does not; pick one",
		)
	case !o.interactive():
		return operation.Fail(operation.ExitInvalid, "choose", "--choose needs a terminal")
	}
	return nil
}

// needsChoice reports whether apply must show the checklist rather than apply
// the saved selection: a non-fixed effect the owner has not decided yet, or a
// file Workbench owns whole that was edited outside it. Merged files never
// count, since Claude Code and Codex rewrite theirs constantly.
func needsChoice(plan operation.Plan) bool {
	return slices.ContainsFunc(plan.Effects, func(effect operation.Effect) bool {
		return effect.New && !effect.Fixed
	}) || slices.ContainsFunc(plan.Edits, func(edit operation.Edit) bool {
		return edit.EditedOutside && !edit.Merged
	})
}

// machineSelection is the saved selection, or an empty one with --reset.
func machineSelection(c operation.Context, o *options) (machine.Selection, error) {
	if o.reset {
		return machine.Selection{}, nil
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
// questions the saved answers lack (or ask names), plans, gets the selection
// approved and applies it. A local checkout uses the saved answers and tools
// as they are.
func applyMachine(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	ask []string,
) (operation.Result, operation.Plan, error) {
	result := operation.NewResult(cmd.CommandPath())
	terminal, progress, closeConsole := nativeConsole(o, cmd.ErrOrStderr())
	defer closeConsole()
	if !c.Native.Developer {
		var err error
		c, err = setUp(cmd, c, o, terminal, &result, true, ask)
		if err != nil {
			return result, operation.Plan{}, err
		}
	}
	selection, err := machineSelection(c, o)
	if err != nil {
		return result, operation.Plan{}, err
	}
	plan, err := machine.Plan(cmd.Context(), c, selection)
	if err != nil {
		return result, plan, err
	}
	consent := consentFor(o, o.approvePlan)
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
				!needsChoice(plan),
			); err != nil {
				return result, plan, err
			}
		}
	case o.interactive() && !o.choose && !o.reset && !needsChoice(plan):
		// Nothing new to decide: show the plan and apply the saved selection.
		if err = writePlanView(cmd.OutOrStdout(), plan, o.verbose, true); err != nil {
			return result, plan, err
		}
		consent.ApprovedDigest = plan.Digest()
	case o.interactive():
		// Tag saved skips from machine.toml even under --reset: Apply's recheck
		// reads the file the same way, and SavedSkip is part of the digest the
		// approval must equal.
		saved, savedErr := machine.ReadSelection(c.Native.Config)
		if savedErr != nil {
			return result, plan, savedErr
		}
		checked, approved, chooseErr := choosePlan(plan, selection, o.verbose)
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
		// checked holds each part's own choice, not the force of a checked
		// parent, which Reselect applies.
		for i := range plan.Effects {
			if !plan.Effects[i].Fixed {
				plan.Effects[i].Checked = slices.Contains(checked, plan.Effects[i].Name)
			}
		}
		selection = machine.SelectionOf(plan.Effects)
		plan = machine.Reselect(plan, selection, saved)
		consent.ApprovedDigest = plan.Digest()
	default:
		// --json or --non-interactive: show the plan; WithMutation then
		// refuses without a digest.
		result.Results = append(result.Results, operation.Component{
			Name:    "machine-plan",
			Status:  operation.StatusComplete,
			Details: plan,
		})
	}
	result.PlanDigest = plan.Digest()
	applied, err := machine.Apply(cmd.Context(), c, selection, plan, consent, terminal, progress)
	result.Results = append(result.Results, applied.Results...)
	result.OperationID = applied.OperationID
	return result, plan, err
}

// appliedSummary is the branded result line. Apply's early return, with no
// file to write and no selection to save, reports itself by its message.
func appliedSummary(result operation.Result, plan operation.Plan) string {
	for _, component := range result.Results {
		if component.Message == machine.NothingToApply {
			return "[WorkBench] Nothing to apply; this machine already matches"
		}
	}
	effects, skipped := 0, 0
	for _, effect := range plan.Effects {
		switch {
		case effect.Fixed:
		case effect.Checked:
			effects++
		default:
			skipped++
		}
	}
	line := fmt.Sprintf("[WorkBench] Applied: %d files, %d effects", len(plan.Edits), effects)
	if skipped > 0 {
		line += fmt.Sprintf("; %d skipped", skipped)
	}
	return line
}
