package machine

import (
	"context"

	"github.com/Sawmonabo/workbench/internal/operation"
)

func Revert(
	ctx context.Context,
	c operation.Context,
	plan operation.Plan,
	selector operation.RecoverySelector,
	consent operation.Consent,
) (operation.Result, error) {
	result := operation.NewResult("workbench revert")
	if c.Scope.Kind != "machine" {
		return result, operation.Fail(2, "scope", "Machine recovery requires machine scope")
	}
	result.PlanDigest, _ = plan.Digest()
	result.Warnings = append(result.Warnings, plan.RecoveryLimits...)
	var err error
	result.OperationID, err = operation.Recover(ctx, c, plan, selector, consent)
	if err == nil {
		result.Results = append(
			result.Results,
			operation.Component{
				Name:     "configuration",
				Status:   "complete",
				Recovery: "Paired checkpoint retained; external effects were not reverted",
			},
		)
	}
	return result, err
}
