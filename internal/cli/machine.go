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
				result.Results = append(
					result.Results,
					operation.Component{
						Name:    "answers",
						Status:  operation.StatusComplete,
						Message: "Saved; apply now uses them without --machine-config",
					},
				)
			}
			return result, err
		},
	)
	return cmd
}

// applyCommand previews the machine plan and, unless --dry-run, applies it
// after approval.
func applyCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Show what would change on this machine, ask, then apply it with file checkpoints",
		Args:  cobra.NoArgs,
		RunE: o.action(
			nativeAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				dryRun, _ := cmd.Flags().GetBool("dry-run")
				return machinePlan(cmd, c, o, !dryRun)
			},
		),
	}
	cmd.Flags().Bool("dry-run", false, "Show the plan without applying it")
	cmd.Flags().Bool("config-only", false, "Apply configuration files without provisioning scripts")
	addEffectFlag(cmd)
	o.sourceFlag(cmd)
	o.machineConfigFlag(cmd)
	o.destinationFlag(cmd)
	o.approveFlag(cmd)
	return cmd
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
