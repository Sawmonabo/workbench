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
			"answers lack, then show every file change and provisioning effect with what it would " +
			"do here. Space unchecks an effect, enter applies; unchecked effects are remembered for " +
			"this machine until --reset. --yes applies the saved selection without asking; " +
			"--dry-run only shows the checklist.",
		Args: cobra.NoArgs,
		RunE: o.action(
			nativeAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
					return machinePlan(cmd, c, o)
				}
				c.ReadOnly = false
				result, plan, err := applyMachine(cmd, c, o, nil)
				if err == nil {
					result.Summary = appliedSummary(plan)
				}
				return result, err
			},
		),
	}
	cmd.Flags().Bool("dry-run", false, "Show the checklist without installing, asking or applying")
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "Apply the saved selection without asking")
	cmd.Flags().
		BoolVar(&o.reset, "reset", false, "Forget the saved skips; everything is checked again")
	o.localBuildFlag(cmd)
	o.destinationFlag(cmd)
	o.approveFlag(cmd)
	return cmd
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
		consent.ApprovedDigest = plan.Digest()
	case o.interactive():
		checked, approved, chooseErr := choosePlan(plan, o.verbose)
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
		// With --reset nothing is "saved" for this run, so no row is tagged.
		saved, savedErr := machineSelection(c, o)
		if savedErr != nil {
			return result, plan, savedErr
		}
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

// appliedSummary is the branded result line.
func appliedSummary(plan operation.Plan) string {
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
