package cli

import (
	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/spf13/cobra"
)

// revertCommand restores the files of a saved machine checkpoint.
func revertCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revert",
		Short: "Undo an earlier apply's file changes from a saved checkpoint",
		Long: "Restore the files an earlier apply changed, from the checkpoint it saved. " +
			"Without --checkpoint it lists the checkpoints to choose from at a terminal. It " +
			"restores files only: what the apply's steps installed or ran stays. Without a " +
			"terminal, or for a coding agent, pass --approve-plan DIGEST from a --dry-run " +
			"--json plan.",
		Example: "  workbench revert --dry-run\n" +
			"  workbench revert\n" +
			"  workbench revert --checkpoint ID --dry-run --json",
		Args: cobra.NoArgs,
	}
	cmd.Flags().
		String("checkpoint", "", "Restore this checkpoint ID instead of choosing from a list")
	cmd.Flags().Bool("dry-run", false, "Show the restore plan without restoring")
	o.destinationFlag(cmd)
	o.approveFlag(cmd)
	cmd.RunE = o.action(
		machineAction,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			id, err := o.checkpointChoice(cmd, c, &result, "checkpoints")
			if err != nil {
				return result, err
			}
			selector := operation.RecoverySelector{Checkpoint: id}
			plan, err := operation.RecoveryPlan(c, selector)
			if err != nil {
				return result, err
			}
			result.PlanDigest = plan.Digest()
			if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
				result.Results = append(
					result.Results,
					operation.Component{
						Name:    "recovery-plan",
						Status:  operation.StatusComplete,
						Details: plan,
					},
				)
				return result, nil
			}
			consent := consentFor(o, o.approvePlan)
			consent.CompleteInputs = plan.Complete
			reverted, err := machine.Revert(cmd.Context(), c, plan, selector, consent)
			if err == nil {
				reverted.Summary = "Restored the files from the checkpoint"
			}
			return reverted, err
		},
	)
	return cmd
}
