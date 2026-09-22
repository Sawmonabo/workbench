package machine

import (
	"context"
	"io"
	"os"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/release"
)

// Apply shares native preparation with preview and obtains fresh exact images
// under the operation locks before granting the native engine write authority.
// See [Prepared.Apply] for terminal and progress.
func Apply(ctx context.Context, c operation.Context, selection Selection, displayed operation.Plan, consent operation.Consent, terminal *os.File, progress io.Writer) (operation.Result, error) {
	result := operation.NewResult("workbench apply")
	result.PlanDigest, _ = displayed.Digest()
	result.Warnings = append(result.Warnings, displayed.RecoveryLimits...)
	c.ReadOnly = false
	var prepared *Prepared
	defer func() {
		if prepared != nil {
			prepared.Close()
		}
	}()
	err := operation.WithMutation(ctx, c, displayed, consent, func(ctx context.Context, preview operation.Context) (operation.Plan, error) {
		var err error
		prepared, err = Prepare(ctx, preview, selection)
		return prepared.Plan, err
	}, func(m *operation.Mutation) (applyErr error) {
		runtimeMutated, activationPlanned := false, false
		for _, effect := range prepared.Plan.Effects {
			activationPlanned = activationPlanned || effect.Name == "activate-candidate"
		}
		defer func() {
			if runtimeMutated && applyErr != nil && operation.ExitCode(applyErr) != 130 && operation.ExitCode(applyErr) != 5 {
				applyErr = operation.Fail(5, "partial", "Runtime activated; subsequent configuration work did not complete, retained recovery state requires inspection")
			}
		}()
		state, err := operation.ReadState(c.Paths)
		if err != nil {
			return err
		}
		if selection.ConfigOnly && len(prepared.Changes) == 0 {
			if state != nil && state.PartialOperation != nil {
				return operation.Fail(3, "recovery", "Resolve the recorded incomplete operation before selecting another configuration release")
			}
			if err = release.ActivateCandidate(ctx, c, m); err != nil {
				return err
			}
			runtimeMutated = activationPlanned
			current, readErr := operation.ReadState(c.Paths)
			if readErr != nil {
				return readErr
			}
			if runtimeChanged(state, current) {
				current.AppliedConfiguration = &prepared.Plan.Source
				current.Dependencies = prepared.Plan.Dependencies
				if err = m.WriteState(*current); err != nil {
					return operation.Fail(5, "state", "Runtime activated; configuration identity finalization failed")
				}
				result.Results = append(result.Results, operation.Component{Name: "runtime", Status: "complete", Message: "Approved matching runtime activated"})
			}
			result.Results = append(result.Results, operation.Component{Name: "configuration", Status: "unchanged", Recovery: "No target writes or checkpoint allocation"})
			return nil
		}
		if state == nil {
			state = &operation.State{SchemaVersion: 1}
		}
		cp, err := operation.BeginCheckpoint(m, prepared.Plan, state.AppliedConfiguration, prepared.Changes)
		if err != nil {
			return err
		}
		result.OperationID = cp.ID
		if err = release.ActivateCandidate(ctx, c, m); err != nil {
			return err
		}
		runtimeMutated = activationPlanned
		current, err := operation.ReadState(c.Paths)
		if err != nil {
			return err
		}
		runtimeMutated = runtimeMutated || runtimeChanged(state, current)
		if current != nil {
			state = current
		}
		state.PartialOperation = &operation.PartialOperation{ID: cp.ID, Scope: c.Scope}
		if err = m.WriteState(*state); err != nil {
			return err
		}
		if err = cp.StartNative(); err != nil {
			return err
		}
		runErr := prepared.Apply(ctx, c, m, terminal, progress)
		if runErr == nil {
			runErr = cp.FinalizeNative()
		}
		if !selection.ConfigOnly && runErr != nil && operation.ExitCode(runErr) != 130 {
			runErr = operation.Fail(5, "partial", "Native provisioning failed after it started ("+runErr.Error()+"); external effects may be partial and are not rolled back")
		}
		if err = cp.Finish(runErr); err != nil {
			status := "failed"
			if operation.ExitCode(err) == 5 || operation.ExitCode(err) == 130 {
				status = "partial"
			}
			result.Results = append(result.Results, operation.Component{Name: "configuration", Status: status, Recovery: "Checkpoint " + cp.ID + " retained; unknown outcomes require reconciliation"})
			return err
		}
		state.AppliedConfiguration = &prepared.Plan.Source
		state.Dependencies = prepared.Plan.Dependencies
		state.PartialOperation = nil
		if err = m.WriteState(*state); err != nil {
			return operation.Fail(5, "state", "Configuration applied; state finalization failed, retained checkpoint remains available")
		}
		result.Results = append(result.Results, operation.Component{Name: "configuration", Status: "complete", Recovery: "Exact configuration images retained; select checkpoint " + cp.ID})
		for _, effect := range prepared.Plan.Effects {
			result.Results = append(result.Results, operation.Component{Name: effect.Name, Status: "complete", Message: effect.Description, Recovery: effect.Recovery})
		}
		return nil
	})
	return result, err
}

func runtimeChanged(before, after *operation.State) bool {
	return after != nil && after.ActiveRelease != nil && (before == nil || before.ActiveRelease == nil || *before.ActiveRelease != *after.ActiveRelease)
}
