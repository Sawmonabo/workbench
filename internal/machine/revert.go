package machine

import (
	"context"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Revert restores the selected machine checkpoint under consent for plan.
func Revert(
	ctx context.Context,
	c operation.Context,
	plan operation.Plan,
	selector operation.RecoverySelector,
	consent operation.Consent,
) (operation.Result, error) {
	result := operation.NewResult("workbench revert")
	if c.Scope.Kind != "machine" {
		return result, operation.Fail(
			operation.ExitInvalid,
			"scope",
			"Machine recovery requires machine scope",
		)
	}
	result.PlanDigest = plan.Digest()
	result.Warnings = append(result.Warnings, plan.RecoveryLimits...)
	var err error
	result.OperationID, err = operation.Recover(ctx, c, plan, selector, consent)
	if err == nil {
		result.Results = append(
			result.Results,
			operation.Component{
				Name:     "configuration",
				Status:   operation.StatusComplete,
				Recovery: "Paired checkpoint retained; external effects were not reverted",
			},
		)
	}
	return result, err
}
