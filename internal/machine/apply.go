package machine

import (
	"context"
	"io"
	"os"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Apply shares native preparation with preview and obtains fresh exact images
// under the operation locks before granting the native engine write authority.
// An interactive apply gives native chezmoi terminal; otherwise its redacted
// output goes to progress.
func Apply(
	ctx context.Context,
	c operation.Context,
	selection Selection,
	displayed operation.Plan,
	consent operation.Consent,
	terminal *os.File,
	progress io.Writer,
) (_ operation.Result, err error) {
	defer operation.Annotate(&err, "apply machine configuration")
	result := operation.NewResult("workbench apply")
	result.PlanDigest = displayed.Digest()
	// A config-only plan without file changes writes nothing, unless it
	// settles an earlier unfinished apply, so there is nothing to approve.
	if selection.ConfigOnly && displayed.Complete && len(displayed.Edits) == 0 {
		state, stateErr := operation.ReadState(c.Paths)
		if stateErr != nil {
			return result, stateErr
		}
		if state == nil || state.PartialOperation == nil {
			result.Results = append(result.Results, operation.Component{
				Name:    "configuration",
				Status:  operation.StatusUnchanged,
				Message: "Every file already matches; nothing to apply",
			})
			return result, nil
		}
	}
	result.Warnings = append(result.Warnings, displayed.RecoveryLimits...)
	c.ReadOnly = false
	var prepared *preparation
	defer func() {
		if prepared != nil {
			prepared.Close()
		}
	}()
	planner := func(ctx context.Context, preview operation.Context) (operation.Plan, error) {
		defer preview.ShowProgress("Rechecking the plan")()
		var err error
		prepared, err = prepare(ctx, preview, selection)
		return prepared.Plan, err
	}
	err = operation.WithMutation(
		ctx,
		c,
		displayed,
		consent,
		planner,
		func(m *operation.Mutation) error {
			run := &applyRun{
				ctx:       ctx,
				c:         c,
				m:         m,
				selection: selection,
				prepared:  prepared,
				terminal:  terminal,
				progress:  progress,
				result:    &result,
			}
			return run.apply()
		},
	)
	return result, err
}

// applyRun is one approved apply holding the operation locks.
type applyRun struct {
	ctx       context.Context
	c         operation.Context
	m         *operation.Mutation
	selection Selection
	prepared  *preparation
	terminal  *os.File
	progress  io.Writer
	result    *operation.Result
}

func (a *applyRun) apply() error {
	state, err := operation.ReadState(a.c.Paths)
	if err != nil {
		return err
	}
	// Without target changes there is nothing to checkpoint; provisioning
	// effects are external and never rolled back either way.
	if len(a.prepared.Changes) == 0 {
		return a.withoutCheckpoint(state)
	}
	return a.withCheckpoint(state)
}

func (a *applyRun) withoutCheckpoint(state *operation.State) error {
	if state != nil && state.PartialOperation != nil && state.PartialOperation.Scope != a.c.Scope {
		return operation.Fail(
			operation.ExitBlocked,
			"recovery",
			"Apply "+state.PartialOperation.ID+" to "+state.PartialOperation.Scope.Root+
				" did not finish; rerun apply for that destination first",
		)
	}
	if !a.selection.ConfigOnly {
		if err := a.prepared.Apply(a.ctx, a.c, a.m, a.terminal, a.progress); err != nil {
			return provisioningFailure(err)
		}
	}
	current, err := operation.ReadState(a.c.Paths)
	if err != nil {
		return err
	}
	// A plan with no file changes found every target already as this source
	// renders it, which settles an earlier apply that stopped before finishing.
	var settled *operation.PartialOperation
	if current != nil {
		settled = current.PartialOperation
	}
	if !a.selection.ConfigOnly || settled != nil {
		if current == nil {
			current = &operation.State{SchemaVersion: 1}
		}
		current.AppliedConfiguration = &a.prepared.Plan.Source
		current.Dependencies = a.prepared.Plan.Dependencies
		current.PartialOperation = nil
		if err = a.m.WriteState(*current); err != nil {
			return operation.Fail(
				operation.ExitPartial,
				"state",
				"Apply completed; configuration identity finalization failed",
			)
		}
	}
	a.result.Results = append(a.result.Results, operation.Component{
		Name:     "configuration",
		Status:   operation.StatusUnchanged,
		Recovery: "No target writes or checkpoint allocation",
	})
	if settled != nil {
		a.result.Results = append(a.result.Results, operation.Component{
			Name:    "unfinished-apply",
			Status:  operation.StatusComplete,
			Message: "Apply " + settled.ID + " did not finish earlier; every file now matches this plan",
		})
	}
	if !a.selection.ConfigOnly {
		a.result.Results = append(a.result.Results, effectResults(a.prepared.Plan.Effects)...)
	}
	return nil
}

func (a *applyRun) withCheckpoint(state *operation.State) error {
	if state == nil {
		state = &operation.State{SchemaVersion: 1}
	}
	plan := a.prepared.Plan
	cp, err := operation.BeginCheckpoint(a.m, plan, state.AppliedConfiguration, a.prepared.Changes)
	if err != nil {
		return err
	}
	a.result.OperationID = cp.ID
	state.PartialOperation = &operation.PartialOperation{ID: cp.ID, Scope: a.c.Scope}
	if err = a.m.WriteState(*state); err != nil {
		return err
	}
	if err = cp.StartNative(); err != nil {
		return err
	}
	runErr := a.prepared.Apply(a.ctx, a.c, a.m, a.terminal, a.progress)
	if runErr == nil {
		runErr = cp.FinalizeNative()
	}
	if !a.selection.ConfigOnly && runErr != nil {
		runErr = provisioningFailure(runErr)
	}
	if err = cp.Finish(runErr); err != nil {
		status := operation.StatusFailed
		if code := operation.ExitCode(err); code == operation.ExitPartial ||
			code == operation.ExitInterrupted {
			status = operation.StatusPartial
		}
		a.result.Results = append(a.result.Results, operation.Component{
			Name:     "configuration",
			Status:   status,
			Recovery: "Checkpoint " + cp.ID + " retained; unknown outcomes require reconciliation",
		})
		return err
	}
	state.AppliedConfiguration = &plan.Source
	state.Dependencies = plan.Dependencies
	state.PartialOperation = nil
	if err = a.m.WriteState(*state); err != nil {
		return operation.Fail(
			operation.ExitPartial,
			"state",
			"Configuration applied; state finalization failed, retained checkpoint remains available",
		)
	}
	a.result.Results = append(a.result.Results, operation.Component{
		Name:     "configuration",
		Status:   operation.StatusComplete,
		Recovery: "Exact configuration images retained; select checkpoint " + cp.ID,
	})
	a.result.Results = append(a.result.Results, effectResults(plan.Effects)...)
	return nil
}

// provisioningFailure keeps the native cause; external effects are not rolled back.
func provisioningFailure(err error) error {
	if operation.ExitCode(err) == operation.ExitInterrupted {
		return err
	}
	return operation.Fail(
		operation.ExitPartial,
		"partial",
		"Native provisioning failed after it started ("+err.Error()+"); external effects may be partial and are not rolled back",
	)
}

func effectResults(effects []operation.Effect) []operation.Component {
	results := make([]operation.Component, 0, len(effects))
	for _, effect := range effects {
		results = append(
			results,
			operation.Component{
				Name:     effect.Name,
				Status:   operation.StatusComplete,
				Message:  effect.Description,
				Recovery: effect.Recovery,
			},
		)
	}
	return results
}
