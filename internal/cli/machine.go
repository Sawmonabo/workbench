package cli

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
	"github.com/spf13/cobra"
)

func doctorCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check native tool versions and local host prerequisites without repair",
		Args:  cobra.NoArgs,
		RunE: o.action(
			machineAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				result := operation.NewResult(cmd.CommandPath())
				var err error
				result.Results, err = machine.Doctor(cmd.Context(), c)
				return result, err
			},
		),
	}
}

func statusCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Inspect active, staged and applied identities without network or drift probes",
		Args:  cobra.NoArgs,
		RunE: o.action(
			machineAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				result := operation.NewResult(cmd.CommandPath())
				state, err := operation.ReadState(c.Paths)
				if err != nil {
					return result, err
				}
				result.Results = append(
					result.Results,
					operation.Component{
						Name:    "cli",
						Status:  operation.StatusComplete,
						Message: version(),
					},
				)
				message := "No recorded Workbench state"
				if state != nil {
					message = "Current state schema and recorded identities validated"
				}
				result.Results = append(
					result.Results,
					operation.Component{
						Name:    "state",
						Status:  operation.StatusComplete,
						Message: message,
						Details: state,
					},
				)
				candidate, err := release.Candidate(c)
				if err != nil {
					return result, err
				}
				if candidate != "" {
					metadata, inspectErr := release.Inspect(candidate)
					if inspectErr != nil {
						return result, inspectErr
					}
					result.Results = append(
						result.Results,
						operation.Component{
							Name:   "candidate",
							Status: operation.StatusComplete,
							Details: operation.SourceIdentity{
								Release:       metadata.Release,
								ContentDigest: metadata.SourceDigest,
							},
						},
					)
				} else {
					result.Results = append(
						result.Results,
						operation.Component{
							Name:    "candidate",
							Status:  operation.StatusAbsent,
							Message: "No staged release",
						},
					)
				}
				result.Warnings = append(
					result.Warnings,
					"Target drift and remote updates are not inspected. No state was created or changed.",
				)
				return result, nil
			},
		),
	}
}

// initCommand adopts machine answers from an existing chezmoi config once, so a
// dotfiles machine switches without re-answering the questionnaire.
func initCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Save machine answers from an existing chezmoi config's [data] table",
		Args:  cobra.NoArgs,
	}
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
				result.Results = append(
					result.Results,
					operation.Component{
						Name:    "answers",
						Status:  operation.StatusComplete,
						Message: "Saved; plan and apply now use them without --machine-config",
					},
				)
			}
			return result, err
		},
	)
	return cmd
}

// machineCommands returns plan and apply, which share one native planner.
func machineCommands(o *options) []*cobra.Command {
	plan := &cobra.Command{
		Use:   "plan",
		Short: "Preview native machine changes and prerequisites",
		Args:  cobra.NoArgs,
		RunE: o.action(
			nativeAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				return machinePlan(cmd, c, o, false)
			},
		),
	}
	plan.Flags().Bool("config-only", false, "Preview configuration without provisioning effects")
	apply := &cobra.Command{
		Use:   "apply",
		Short: "Apply approved native machine changes with file checkpoints",
		Args:  cobra.NoArgs,
		RunE: o.action(
			nativeAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				dryRun, _ := cmd.Flags().GetBool("dry-run")
				return machinePlan(cmd, c, o, !dryRun)
			},
		),
	}
	apply.Flags().Bool("dry-run", false, "Preview only through the shared machine planner")
	apply.Flags().
		Bool("config-only", false, "Apply native configuration without provisioning scripts")
	addEffectFlag(plan)
	addEffectFlag(apply)
	return []*cobra.Command{plan, apply}
}

// machinePlan previews the native machine plan and, when apply is set, applies
// it with consent.
func machinePlan(
	cmd *cobra.Command,
	c operation.Context,
	o *options,
	apply bool,
) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	selection := machineSelection(cmd)
	plan, err := machine.Plan(cmd.Context(), c, selection)
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
	if !apply {
		return result, nil
	}
	terminal, progress, closeConsole := nativeConsole(o, cmd.ErrOrStderr())
	defer closeConsole()
	return machine.Apply(
		cmd.Context(),
		c,
		selection,
		plan,
		consentFor(o, o.approvePlan),
		terminal,
		progress,
	)
}

func addEffectFlag(cmd *cobra.Command) {
	cmd.Flags().
		StringArray("effect", nil, "Select an optional provisioning effect by name (repeatable); available here: "+strings.Join(machine.OptionalEffectNames(), ", "))
}

func machineSelection(cmd *cobra.Command) machine.Selection {
	configOnly, _ := cmd.Flags().GetBool("config-only")
	effects, _ := cmd.Flags().GetStringArray("effect")
	return machine.Selection{ConfigOnly: configOnly, Effects: effects}
}
