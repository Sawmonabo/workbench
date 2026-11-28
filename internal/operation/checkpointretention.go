package operation

import (
	"fmt"
	"path/filepath"
	"slices"
	"time"
)

// RetentionEffect names the checkpoint that a new forward checkpoint replaces
// once the scope holds MaxForwardCheckpoints: the oldest settled one, else the
// oldest incomplete one that a newer checkpoint supersedes. A settled one
// finished, failed before it changed a target, or stopped while prepared,
// before it marked any target as being written, so none of them holds the
// only record of a change. Plans list it so consent covers the removal. The
// checkpoint named by the recorded partial operation is never a candidate.
func RetentionEffect(c Context) (*Effect, error) {
	oldest, err := retentionCandidate(c)
	if oldest == nil || err != nil {
		return nil, err
	}
	effect := retentionEffect(oldest)
	return &effect, nil
}

func retentionEffect(cp *Checkpoint) Effect {
	kind := "superseded incomplete"
	if settledJournal(cp.journal.Status) {
		kind = "settled"
	}
	return Effect{
		Name: "checkpoint-retention",
		Description: fmt.Sprintf(
			"Remove checkpoint %s from %s, the oldest %s of %d retained, with its paired recovery record",
			cp.ID,
			cp.record.Created.Format(time.RFC3339),
			kind,
			MaxForwardCheckpoints,
		),
		Privilege: "user",
		Fixed:     true,
		Checked:   true,
		Recovery:  "the removed checkpoint can no longer be restored",
		Title:     "Remove the oldest checkpoint",
		Summary:   fmt.Sprintf("keeps the %d most recent", MaxForwardCheckpoints),
		What: fmt.Sprintf(
			"Removes the oldest %s checkpoint, %s, with its paired recovery record, to make room for this apply. Workbench keeps %d.",
			kind,
			cp.ID,
			MaxForwardCheckpoints,
		),
		Touches: "Workbench's checkpoint store",
		RunsAs:  "you",
		Undo:    "Cannot be undone: the removed checkpoint can no longer be restored",
	}
}

func retentionCandidate(c Context) (*Checkpoint, error) {
	checkpoints, err := loadCheckpoints(c)
	if err != nil || len(checkpoints) < MaxForwardCheckpoints {
		return nil, err
	}
	state, err := ReadState(c.Paths)
	if err != nil {
		return nil, err
	}
	// A running, partial or unknown checkpoint is superseded once any newer
	// checkpoint exists in the scope: a later apply already took the targets'
	// images as they were, so no later work waits on it. Requiring a newer
	// *complete* one would let 20 partial applies (a step that keeps failing)
	// fill every slot with nothing removable.
	var newest time.Time
	for _, cp := range checkpoints {
		if cp.record.Created.After(newest) {
			newest = cp.record.Created
		}
	}
	var settled, superseded *Checkpoint
	for _, cp := range checkpoints {
		if state != nil && state.PartialOperation != nil &&
			state.PartialOperation.ID == cp.journal.OperationID {
			continue
		}
		slot := &settled
		switch status := cp.journal.Status; {
		case settledJournal(status):
		case status == JournalRunning || status == JournalPartial || status == JournalUnknown:
			if !cp.record.Created.Before(newest) {
				continue
			}
			slot = &superseded
		default:
			continue
		}
		if *slot == nil || cp.record.Created.Before((*slot).record.Created) {
			*slot = cp
		}
	}
	if settled != nil {
		return settled, nil
	}
	return superseded, nil
}

// settledJournal reports whether a checkpoint of status changed no target
// it did not record: complete, failed before any change, or prepared, its
// apply stopped before it started writing (see [JournalPrepared]).
func settledJournal(status JournalStatus) bool {
	return status == JournalComplete || status == JournalFailed || status == JournalPrepared
}

// pruneApproved removes only the checkpoint the approved plan names. A rename
// out of the scope makes the removal atomic before its files are deleted.
// At MaxForwardCheckpoints retentionCandidate finds one, as it passes over
// only the newest incomplete checkpoint and the one the state records, and
// [WithMutation] plans again under the same locks, refusing a plan whose
// digest differs, so the plan machine and project applies hand
// BeginCheckpoint names the same removal. The refusal below guards a plan
// that names none or another one; its fix is the scope's apply, which plans
// from the checkpoints as they are.
func pruneApproved(c Context, plan Plan) error {
	oldest, err := retentionCandidate(c)
	if err != nil {
		return err
	}
	if oldest == nil || !slices.Contains(plan.Effects, retentionEffect(oldest)) {
		return Fail(
			ExitBlocked,
			"checkpoint_limit",
			fmt.Sprintf(
				"%s keeps %d checkpoints, the most Workbench keeps",
				c.ShowPath(c.Scope.Root),
				MaxForwardCheckpoints,
			),
			Cause("this apply adds one, and its plan removes none of them"),
			FixText(c.ScopeApplyCommand()),
		)
	}
	// The trash is outside the scope, whose listing admits only checkpoints.
	trash := filepath.Dir(checkpointScope(c))
	return removeDirectory(oldest.directory, trash, ".removed-"+oldest.ID)
}
