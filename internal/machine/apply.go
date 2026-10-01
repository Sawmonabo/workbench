package machine

import (
	"cmp"
	"context"
	"io"
	"os"
	"slices"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// NothingToApply is the message of the unchanged configuration result when
// every file matches and the selection equals the saved one, so Apply returned
// before approving or writing anything.
const NothingToApply = "Every file already matches and no effect is checked; nothing to apply"

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
	// An approved digest must match even when there is nothing to apply: a
	// caller whose approved plan ran effects must not be told it is done.
	if consent.ApprovedDigest != "" && consent.ApprovedDigest != result.PlanDigest {
		return result, operation.Fail(
			operation.ExitConflict,
			"plan",
			"Approval digest does not match the current plan; review a new plan",
		)
	}
	// A plan without file changes and without a checked effect writes nothing,
	// unless it settles an earlier unfinished apply or changes the saved
	// selection, so there is nothing to approve.
	unchanged, err := selectionUnchanged(c, displayed)
	if err != nil {
		return result, err
	}
	if displayed.Complete && len(displayed.Edits) == 0 && !hasProvisioning(displayed.Effects) &&
		unchanged {
		state, stateErr := operation.ReadState(c.Paths)
		if stateErr != nil {
			return result, stateErr
		}
		if state == nil || state.PartialOperation == nil {
			result.Results = append(result.Results, operation.Component{
				Name:    "configuration",
				Status:  operation.StatusUnchanged,
				Message: NothingToApply,
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
		prepared, err = prepare(ctx, preview, selection, false)
		if err == nil {
			// The recheck does not probe; carry the shown deltas so the
			// result messages keep them. The digest ignores these fields.
			for i, effect := range prepared.Plan.Effects {
				if j := slices.IndexFunc(displayed.Effects, func(shown operation.Effect) bool {
					return shown.Name == effect.Name
				}); j >= 0 {
					prepared.Plan.Effects[i].Delta = displayed.Effects[j].Delta
					prepared.Plan.Effects[i].Probe = displayed.Effects[j].Probe
				}
			}
		}
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
	plan := a.prepared.Plan
	provisioning := hasProvisioning(plan.Effects)
	if provisioning {
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
	if provisioning || settled != nil {
		if current == nil {
			current = &operation.State{SchemaVersion: 1}
		}
		now := time.Now().UTC()
		current.AppliedConfiguration = &plan.Source
		current.AppliedAt = &now
		current.Dependencies = plan.Dependencies
		current.PartialOperation = nil
		if err = a.m.WriteState(*current); err != nil {
			return operation.Fail(
				operation.ExitPartial,
				"state",
				"Apply completed; configuration identity finalization failed",
			)
		}
	}
	// With every effect unchecked and every file matching, the approved
	// selection is the only thing left to save.
	if err = a.saveSelection(plan); err != nil {
		return err
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
	a.result.Results = append(a.result.Results, effectResults(checkedEffects(plan.Effects))...)
	return nil
}

func (a *applyRun) withCheckpoint(state *operation.State) error {
	if state == nil {
		state = &operation.State{SchemaVersion: 1}
	}
	plan := a.prepared.Plan
	provisioning := hasProvisioning(plan.Effects)
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
	if provisioning && runErr != nil {
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
	now := time.Now().UTC()
	state.AppliedConfiguration = &plan.Source
	state.AppliedAt = &now
	state.Dependencies = plan.Dependencies
	state.PartialOperation = nil
	if err = a.m.WriteState(*state); err != nil {
		return operation.Fail(
			operation.ExitPartial,
			"state",
			"Configuration applied; state finalization failed, retained checkpoint remains available",
		)
	}
	if err = a.saveSelection(plan); err != nil {
		return err
	}
	a.result.Results = append(a.result.Results, operation.Component{
		Name:     "configuration",
		Status:   operation.StatusComplete,
		Recovery: "Exact configuration images retained; select checkpoint " + cp.ID,
	})
	a.result.Results = append(a.result.Results, effectResults(checkedEffects(plan.Effects))...)
	return nil
}

// saveSelection remembers what the approved apply checked, in machine.toml. An
// isolated destination is skipped: its plan unchecked every effect, and saving
// that would skip everything on the real machine.
func (a *applyRun) saveSelection(plan operation.Plan) error {
	if a.c.Native.Destination != a.c.Home {
		return nil
	}
	saved, err := ReadSelection(a.c.Native.Config)
	if err != nil {
		return err
	}
	selection := selectionToSave(plan.Effects, saved)
	if selection.equal(saved) {
		return nil
	}
	if err = WriteSelection(a.m, a.c.Native.Config, selection); err != nil {
		return operation.Fail(
			operation.ExitPartial,
			"state",
			"Applied; saving the effect selection failed",
		)
	}
	return nil
}

// selectionUnchanged reports whether approving plan would leave the saved
// selection as it is; an isolated destination never saves one.
func selectionUnchanged(c operation.Context, plan operation.Plan) (bool, error) {
	if c.Native.Destination != c.Home {
		return true, nil
	}
	saved, err := ReadSelection(c.Native.Config)
	if err != nil {
		return false, err
	}
	return selectionToSave(plan.Effects, saved).equal(saved), nil
}

// hasProvisioning reports whether any non-fixed effect is checked, which is
// when native must run the provisioning scripts.
func hasProvisioning(effects []operation.Effect) bool {
	return slices.ContainsFunc(effects, func(effect operation.Effect) bool {
		return effect.Checked && !effect.Fixed
	})
}

// checkedEffects keeps the effects this apply ran.
func checkedEffects(effects []operation.Effect) []operation.Effect {
	return slices.DeleteFunc(
		slices.Clone(effects),
		func(effect operation.Effect) bool { return !effect.Checked },
	)
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
				Message:  cmp.Or(effect.Delta, effect.Description),
				Recovery: effect.Recovery,
			},
		)
	}
	return results
}
