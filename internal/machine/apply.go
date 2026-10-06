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
// output goes to progress. brewRefreshed says [RefreshHomebrew] refreshed
// Homebrew's index before this apply planned, so its scripts need not.
func Apply(
	ctx context.Context,
	c operation.Context,
	selection Selection,
	displayed operation.Plan,
	consent operation.Consent,
	terminal *os.File,
	progress io.Writer,
	brewRefreshed bool,
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
	unchanged, err := selectionUnchanged(c, selection, displayed)
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
			prepared.brewRefreshed = brewRefreshed
			// The recheck does not probe; carry the shown deltas so the
			// result messages keep them. The digest ignores these fields.
			for i, effect := range prepared.Plan.Effects {
				if j := slices.IndexFunc(displayed.Effects, func(shown operation.Effect) bool {
					return shown.Name == effect.Name
				}); j >= 0 {
					prepared.Plan.Effects[i].Delta = displayed.Effects[j].Delta
					prepared.Plan.Effects[i].Probe = displayed.Effects[j].Probe
					prepared.Plan.Effects[i].ProbeNote = displayed.Effects[j].ProbeNote
					prepared.Plan.Effects[i].NoChange = displayed.Effects[j].NoChange
					prepared.Plan.Effects[i].New = displayed.Effects[j].New
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

// apply makes administrator rights available before anything is written: a
// checkpoint, a state file or a script, by the Mac password or, where sudo asks
// for Touch ID, by sudo itself. See [operation.WithAdmin]. A plan with no script
// to run has no use for it.
func (a *applyRun) apply() error {
	effects := a.prepared.Plan.Effects
	if !hasProvisioning(effects) {
		return a.write()
	}
	admin := operation.Admin{Terminal: a.terminal}
	if installsHomebrew(effects) {
		admin.Why = adminReason
		admin.Refusal = "Installing Homebrew needs your Mac password; run " +
			a.c.WorkbenchCommand() + " apply in a terminal"
	}
	return operation.WithAdmin(a.ctx, a.c, a.m, admin, func(askpass string) error {
		a.prepared.askpass = askpass
		return a.write()
	})
}

// write checkpoints, provisions and records the apply.
func (a *applyRun) write() error {
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
	if err := state.Unfinished(a.c.Scope); err != nil {
		return err
	}
	plan := a.prepared.Plan
	provisioning := hasProvisioning(plan.Effects)
	if provisioning {
		if err := a.prepared.Apply(a.ctx, a.c, a.m, a.terminal, a.progress); err != nil &&
			!a.stepsReported(err) {
			return provisioningFailure(a.c, err)
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
		recordApplied(a.c, current, plan)
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
	return a.reportEffects(plan)
}

func (a *applyRun) withCheckpoint(state *operation.State) error {
	if state == nil {
		state = &operation.State{SchemaVersion: 1}
	}
	// The single partial-operation record is replaced below; another
	// destination's unfinished apply must not be overwritten.
	if err := state.Unfinished(a.c.Scope); err != nil {
		return err
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
	switch {
	case runErr == nil:
		runErr = cp.FinalizeNative()
	case a.stepsReported(runErr) && cp.FinalizeNative() == nil:
		// Every file is as approved and each step that failed named itself:
		// the apply finishes, and reportEffects shows those steps blocked.
		runErr = nil
	default:
		// The files are written before the scripts run, so a script that failed
		// or was interrupted leaves them as approved except for the group native
		// creation gave them. Settle that, so the checkpoint records them as
		// written and not as unknown. An interrupted run is settled too: the
		// process runner kills native's whole process group and waits for it, so
		// nothing native started is still writing.
		cp.SettleFailedNative()
		if provisioning {
			runErr = provisioningFailure(a.c, runErr)
		}
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
			Recovery: "Checkpoint " + cp.ID + " retained",
		})
		return err
	}
	recordApplied(a.c, state, plan)
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
	return a.reportEffects(plan)
}

// recordApplied finishes the machine state after an apply: the operation is
// no longer partial, and for the real home the applied source, its time and
// its dependencies are recorded. An isolated destination records none of
// them, so it never changes what the machine reports as applied.
func recordApplied(c operation.Context, state *operation.State, plan operation.Plan) {
	state.PartialOperation = nil
	if !c.RecordsHome() {
		return
	}
	now := time.Now().UTC()
	state.AppliedConfiguration = &plan.Source
	state.AppliedAt = &now
	state.Dependencies = plan.Dependencies
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
	selection := selectionToSave(plan.Effects, a.selection, saved)
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
func selectionUnchanged(
	c operation.Context,
	selection Selection,
	plan operation.Plan,
) (bool, error) {
	if c.Native.Destination != c.Home {
		return true, nil
	}
	saved, err := ReadSelection(c.Native.Config)
	if err != nil {
		return false, err
	}
	return selectionToSave(plan.Effects, selection, saved).equal(saved), nil
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

// stepsReported reports whether a native run that failed did so only in setup
// steps that named themselves in the effect report. chezmoi runs with
// --keep-going, so every other step ran; an interrupted run is not one.
func (a *applyRun) stepsReported(err error) bool {
	return err != nil && operation.ExitCode(err) != operation.ExitInterrupted &&
		len(a.prepared.blocked) > 0
}

// provisioningFailure is what a failed setup run says when no step named
// itself or a file is not as approved: what failed and what to do. The native
// cause is kept; the output above it says why. External effects are not rolled
// back, and files the run wrote are kept. An interrupted run says only that,
// since there is nothing to fix.
func provisioningFailure(c operation.Context, err error) error {
	if operation.ExitCode(err) == operation.ExitInterrupted {
		return operation.Fail(
			operation.ExitInterrupted,
			"interrupted",
			"Interrupted; what already ran is kept, and running "+c.WorkbenchCommand()+
				" apply again finishes the rest.",
		)
	}
	return operation.Fail(
		operation.ExitPartial,
		"partial",
		"Setup did not finish ("+err.Error()+"); the other steps ran and what they did is kept. "+
			"The output above says why; fix that, then run "+c.WorkbenchCommand()+" apply again.",
	)
}

// reportEffects adds one result line per effect this apply ran. An effect its
// script could not do (a failed download, a Windows call that did not answer)
// is blocked and says why on its line; the others ran, and the apply then ends
// blocked so that a caller reading only the exit status is told. The next apply
// tries a blocked step again.
func (a *applyRun) reportEffects(plan operation.Plan) error {
	a.result.Results = append(
		a.result.Results,
		effectResults(checkedEffects(plan.Effects), a.prepared.blocked)...,
	)
	if len(a.prepared.blocked) == 0 {
		return nil
	}
	return operation.Fail(
		operation.ExitBlocked,
		"effects",
		"A step could not be done (marked blocked above); everything else was applied, and the next apply tries it again",
	)
}

func effectResults(effects []operation.Effect, blocked map[string]string) []operation.Component {
	results := make([]operation.Component, 0, len(effects))
	for _, effect := range effects {
		component := operation.Component{
			Name:     effect.Name,
			Title:    cmp.Or(effect.Title, effect.Name),
			Status:   operation.StatusComplete,
			Message:  cmp.Or(effect.Delta, effect.Summary, effect.Description),
			Recovery: effect.Recovery,
		}
		if reason, ok := blocked[effect.Name]; ok {
			component.Status = operation.StatusBlocked
			component.Message = reason
		}
		results = append(results, component)
	}
	return results
}
