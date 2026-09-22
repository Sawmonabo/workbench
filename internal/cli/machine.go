package cli

import (
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
	"github.com/spf13/cobra"
)

func doctorCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use: "doctor", Short: "Check native tool versions and local host prerequisites without repair", Args: cobra.NoArgs,
		RunE: o.action(false, func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			var err error
			result.Results, err = machine.Doctor(cmd.Context(), c)
			return result, err
		}),
	}
}

func statusCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Inspect active, staged and applied identities without network or drift probes", Args: cobra.NoArgs,
		RunE: o.action(false, func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			state, err := operation.ReadState(c.Paths)
			if err != nil {
				return result, err
			}
			result.Results = append(result.Results, operation.Component{Name: "cli", Status: "complete", Message: version()})
			message := "No recorded Workbench state"
			if state != nil {
				message = "Current state schema and recorded identities validated"
			}
			result.Results = append(result.Results, operation.Component{Name: "state", Status: "complete", Message: message, Details: state})
			candidate, err := release.Candidate(c)
			if err != nil {
				return result, err
			}
			if candidate != "" {
				metadata, inspectErr := release.Inspect(candidate)
				if inspectErr != nil {
					return result, inspectErr
				}
				result.Results = append(result.Results, operation.Component{Name: "candidate", Status: "complete", Details: operation.SourceIdentity{Release: metadata.Release, ContentDigest: metadata.SourceDigest}})
			} else {
				result.Results = append(result.Results, operation.Component{Name: "candidate", Status: "absent", Message: "No staged release"})
			}
			result.Warnings = append(result.Warnings, "Target drift and remote updates are not inspected. No state was created or changed.")
			return result, nil
		}),
	}
}

// Both plan and apply --dry-run use the same native planning owner.
func machinePlan(cmd *cobra.Command, c operation.Context, o *options) (operation.Result, error) {
	result := operation.NewResult(cmd.CommandPath())
	selection := machineSelection(cmd)
	plan, err := machine.Plan(cmd.Context(), c, selection)
	status := "complete"
	if err != nil {
		status = "blocked"
	}
	result.Results = append(result.Results, operation.Component{Name: "machine-plan", Status: status, Details: plan})
	if err != nil {
		return result, err
	}
	result.PlanDigest, err = plan.Digest()
	if err != nil {
		return result, err
	}
	if cmd.Name() == "apply" {
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		if !dryRun {
			terminal, progress, closeConsole := nativeConsole(o, cmd.ErrOrStderr())
			defer closeConsole()
			return machine.Apply(cmd.Context(), c, selection, plan, releaseConsent(o, o.approvePlan), terminal, progress)
		}
	}
	return result, nil
}

func addEffectFlag(cmd *cobra.Command) {
	cmd.Flags().StringArray("effect", nil, "Select an optional provisioning effect by name (repeatable); available here: "+strings.Join(machine.OptionalEffectNames(), ", "))
}

func machineSelection(cmd *cobra.Command) machine.Selection {
	configOnly, _ := cmd.Flags().GetBool("config-only")
	effects, _ := cmd.Flags().GetStringArray("effect")
	return machine.Selection{ConfigOnly: configOnly, Effects: effects}
}
