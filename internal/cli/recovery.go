package cli

import (
	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

func recoveryCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revert [version]",
		Short: "Restore a retained machine configuration checkpoint",
		Args:  cobra.MaximumNArgs(1),
	}
	cmd.Flags().Bool("list", false, "List machine checkpoints without changing files")
	cmd.Flags().String("checkpoint", "", "Select an explicit checkpoint ID")
	cmd.Flags().Bool("dry-run", false, "Preview recovery conflicts and approval digest")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return o.action(
			machineAction,
			func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
				result := operation.NewResult(cmd.CommandPath())
				list, _ := cmd.Flags().GetBool("list")
				id, _ := cmd.Flags().GetString("checkpoint")
				selector := operation.RecoverySelector{Checkpoint: id}
				if len(args) > 0 {
					selector.Version = args[0]
				}
				if list || id == "" && selector.Version == "" {
					if list && (id != "" || selector.Version != "") {
						return result, operation.Fail(
							2,
							"selector",
							"--list cannot be combined with a recovery selector",
						)
					}
					checkpoints, err := operation.ListCheckpoints(c)
					result.Results = append(
						result.Results,
						operation.Component{
							Name:    "checkpoints",
							Status:  "complete",
							Details: checkpoints,
						},
					)
					if err != nil {
						return result, err
					}
					if !list {
						return result, operation.Fail(
							3,
							"selection",
							"Choose an explicit --checkpoint ID from this list",
						)
					}
					return result, nil
				}
				plan, err := operation.RecoveryPlan(c, selector)
				if err != nil {
					return result, err
				}
				result.PlanDigest = plan.Digest()
				dryRun, _ := cmd.Flags().GetBool("dry-run")
				if dryRun {
					result.Results = append(
						result.Results,
						operation.Component{
							Name:    "recovery-plan",
							Status:  "complete",
							Details: plan,
						},
					)
					return result, nil
				}
				consent := consentFor(o, o.approvePlan)
				consent.CompleteInputs = plan.Complete
				return machine.Revert(cmd.Context(), c, plan, selector, consent)
			},
		)(
			cmd,
			args,
		)
	}
	return cmd
}
