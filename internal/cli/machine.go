package cli

import (
	"context"
	"path/filepath"

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
					result.Summary = "Every check passed"
				}
				return result, err
			},
		),
	}
	o.sourceFlag(cmd)
	return cmd
}

// initCommand adopts machine answers from an existing chezmoi config once, so a
// dotfiles machine switches without re-answering the questionnaire. It is
// hidden: docs/switch-from-dotfiles.md is its only audience.
func initCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "init",
		Short:  "Save machine answers from an existing chezmoi config's [data] table",
		Args:   cobra.NoArgs,
		Hidden: true,
	}
	o.approveFlag(cmd)
	cmd.Flags().
		String("answers-from", "", "Existing chezmoi config, for example ~/.config/chezmoi/chezmoi.toml")
	cmd.Flags().Bool("dry-run", false, "Show the plan without saving answers")
	_ = cmd.MarkFlagRequired("answers-from")
	cmd.RunE = o.action(
		machineAction,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			from, _ := cmd.Flags().GetString("answers-from")
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
						Message: "Saved; apply now uses them without --machine-config",
					},
				)
				result.Summary = "Saved your machine answers"
			}
			return result, err
		},
	)
	return cmd
}

// applyCommand makes this machine match the installed release, or --source,
// through applyMachine. --dry-run only shows the plan.
func applyCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Show what would change on this machine, ask, then apply it with file checkpoints",
		Long: "Install Workbench's tools when missing and ask the machine questions your saved " +
			"answers lack, then show what would change on this machine and ask before applying " +
			"it with file checkpoints. --dry-run only shows the plan; it installs and asks nothing.",
		Args: cobra.NoArgs,
		RunE: o.action(
			nativeAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				if err := checkAsk(cmd, c, o); err != nil {
					return operation.NewResult(cmd.CommandPath()), err
				}
				if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
					return machinePlan(cmd, c)
				}
				c.ReadOnly = false
				result, plan, err := applyMachine(cmd, c, o, operation.NewResult(cmd.CommandPath()))
				if err == nil {
					result.Summary = matchSummary(machineSelection(cmd), plan.Source)
				}
				return result, err
			},
		),
	}
	cmd.Flags().Bool("dry-run", false, "Show the plan without installing, asking or applying")
	cmd.Flags().Bool("config-only", false, "Apply configuration files without provisioning scripts")
	addAskFlag(cmd)
	addEffectFlag(cmd)
	o.sourceFlag(cmd)
	o.machineConfigFlag(cmd)
	o.destinationFlag(cmd)
	o.approveFlag(cmd)
	return cmd
}

// machinePlan shows the native machine plan without applying it.
func machinePlan(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	plan, err := machine.Plan(cmd.Context(), c, machineSelection(cmd))
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
// questions the saved answers lack, or --ask names, then shows the machine
// plan and applies it after its one approval. apply runs it for the installed
// release; update runs it in the release it just installed. An answer file
// given with --machine-config is used as is and never saved, and a developer
// checkout given with --source uses the saved answers and tools as they are:
// setup runs from a release.
func applyMachine(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	result operation.Result,
) (operation.Result, operation.Plan, error) {
	terminal, progress, closeConsole := nativeConsole(o, cmd.ErrOrStderr())
	defer closeConsole()
	if !c.Native.Developer {
		var err error
		c, err = setUp(cmd, c, o, terminal, &result, o.resolve.MachineConfig == "")
		if err != nil {
			return result, operation.Plan{}, err
		}
	}
	selection := machineSelection(cmd)
	plan, err := machine.Plan(cmd.Context(), c, selection)
	if err != nil {
		return result, plan, err
	}
	result.PlanDigest = plan.Digest()
	if !o.interactive() {
		// At a terminal the approval prompt shows the plan instead.
		result.Results = append(result.Results, operation.Component{
			Name:    "machine-plan",
			Status:  operation.StatusComplete,
			Details: plan,
		})
	}
	applied, err := machine.Apply(
		cmd.Context(),
		c,
		selection,
		plan,
		consentFor(o, o.approvePlan),
		terminal,
		progress,
	)
	result.Results = append(result.Results, applied.Results...)
	result.OperationID = applied.OperationID
	return result, plan, err
}

// addAskFlag offers asking saved machine answers again.
func addAskFlag(cmd *cobra.Command) {
	cmd.Flags().StringSlice(
		"ask",
		nil,
		"Ask these saved machine answers again, for example --ask machine_role",
	)
}

// matchSummary says what a successful apply of source leaves in place.
func matchSummary(selection machine.Selection, source operation.SourceIdentity) string {
	if selection.ConfigOnly {
		return "Your configuration files match " + sourceName(&source)
	}
	return "Your machine matches " + sourceName(&source)
}

// addEffectFlag offers the optional host steps, hidden where there are none.
func addEffectFlag(cmd *cobra.Command) {
	available := machine.AvailableEffects()
	cmd.Flags().
		StringArray("effect", nil, "Select an optional WSL host step by name (repeatable): "+available)
	if available == "" {
		_ = cmd.Flags().MarkHidden("effect")
	}
}

func machineSelection(cmd *cobra.Command) machine.Selection {
	configOnly, _ := cmd.Flags().GetBool("config-only")
	effects, _ := cmd.Flags().GetStringArray("effect")
	return machine.Selection{ConfigOnly: configOnly, Effects: effects}
}
