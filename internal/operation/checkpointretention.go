package operation

import (
	"fmt"
	"path/filepath"
	"slices"
	"time"
)

// RetentionEffect names the checkpoint that a new forward checkpoint replaces
// once the scope holds MaxForwardCheckpoints: the oldest settled one, else the
// oldest incomplete one that a newer checkpoint supersedes. Plans list
// it so consent covers the removal. The checkpoint named by the recorded
// partial operation is never a candidate.
func RetentionEffect(c Context) (*Effect, error) {
	oldest, err := retentionCandidate(c)
	if oldest == nil || err != nil {
		return nil, err
	}
	effect := retentionEffect(oldest)
	return &effect, nil
}

func retentionEffect(cp *Checkpoint) Effect {
	kind := "settled"
	if cp.journal.Status != JournalComplete && cp.journal.Status != JournalFailed {
		kind = "superseded incomplete"
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
		switch cp.journal.Status {
		case JournalComplete, JournalFailed:
		case JournalRunning, JournalPartial, JournalUnknown:
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

// pruneApproved removes only the checkpoint the approved plan names. A rename
// out of the scope makes the removal atomic before its files are deleted.
func pruneApproved(c Context, plan Plan) error {
	oldest, err := retentionCandidate(c)
	if err != nil {
		return err
	}
	if oldest == nil || !slices.Contains(plan.Effects, retentionEffect(oldest)) {
		return Fail(
			ExitBlocked,
			"checkpoint_limit",
			"Scope retains 20 forward checkpoints and the plan approves no removal; resolve incomplete checkpoints and review a new plan",
		)
	}
	// The trash is outside the scope, whose listing admits only checkpoints.
	trash := filepath.Dir(checkpointScope(c))
	return removeDirectory(oldest.directory, trash, ".removed-"+oldest.ID)
}
