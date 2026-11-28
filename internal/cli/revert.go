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
	o.recoveryCommand(
		cmd,
		machineAction,
		"checkpoints",
		"recovery-plan",
		func(restore recovery) (operation.Result, error) {
			// The machine's result is its own: the restore, its warnings and the
			// recovery limits, in place of the plan.
			reverted, err := machine.Revert(
				restore.cmd.Context(), restore.c, restore.plan, restore.selector, restore.consent,
			)
			if err == nil {
				reverted.Summary = "Restored the files from the checkpoint"
			}
			return reverted, err
		},
	)
	o.destinationFlag(cmd)
	return cmd
}

// recovery is one approved restore as a revert command's restore func gets it:
// the plan, the checkpoint it restores, the consent bound to the plan, and the
// result so far, which holds the plan under its name.
type recovery struct {
	cmd      *cobra.Command
	c        operation.Context
	plan     operation.Plan
	selector operation.RecoverySelector
	consent  operation.Consent
	result   operation.Result
}

// recoveryCommand makes cmd a revert command of kind: it adds --checkpoint,
// --dry-run and --approve-plan, and its run takes the checkpoint --checkpoint
// names or, at a terminal, the one chosen from those listed under list, plans
// its restore and shows the plan under planName. With --dry-run it stops
// there; otherwise restore restores it.
func (o *options) recoveryCommand(
	cmd *cobra.Command,
	kind actionKind,
	list, planName string,
	restore func(recovery) (operation.Result, error),
) {
	cmd.Flags().
		String("checkpoint", "", "Restore this checkpoint ID instead of choosing from a list")
	cmd.Flags().Bool("dry-run", false, "Show the restore plan without restoring")
	o.approveFlag(cmd)
	cmd.RunE = o.action(
		kind,
		func(cmd *cobra.Command, c operation.Context) (operation.Result, error) {
			result := operation.NewResult(cmd.CommandPath())
			id, err := o.checkpointChoice(cmd, c, &result, list)
			if err != nil {
				return result, err
			}
			selector := operation.RecoverySelector{Checkpoint: id}
			plan, err := operation.RecoveryPlan(c, selector)
			if err != nil {
				return result, err
			}
			result.PlanDigest = plan.Digest()
			result.Results = append(result.Results, operation.Component{
				Name:    planName,
				Status:  operation.StatusComplete,
				Details: plan,
			})
			if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
				return result, nil
			}
			return restore(recovery{
				cmd:      cmd,
				c:        c,
				plan:     plan,
				selector: selector,
				consent:  consentFor(cmd, c, o, o.approvePlan),
				result:   result,
			})
		},
	)
}
