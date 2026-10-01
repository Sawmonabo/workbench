package operation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// RecoverySelector picks one checkpoint by ID, or by the release it restores.
type RecoverySelector struct{ Checkpoint string }

func selectCheckpoint(c Context, selector RecoverySelector) (*Checkpoint, bool, error) {
	if selector.Checkpoint == "" {
		return nil, false, Fail(
			ExitBlocked,
			"selection",
			"List checkpoints and explicitly select an ID; recovery has no implicit latest selection",
		)
	}
	checkpoints, err := loadCheckpoints(c)
	if err != nil {
		return nil, false, err
	}
	var selected *Checkpoint
	reverse := true
	var choices []string
	for _, cp := range checkpoints {
		choices = append(choices, cp.record.ID)
		if cp.journal.RecoveryCreated {
			choices = append(choices, cp.record.RecoveryID)
		}
		// A forward checkpoint restores the files before its apply; its
		// recovery pair restores the applied ones.
		if selector.Checkpoint == cp.record.ID {
			selected = cp
			reverse = true
		}
		if cp.journal.RecoveryCreated && selector.Checkpoint == cp.record.RecoveryID {
			selected = cp
			reverse = false
		}
	}
	if selected == nil {
		return nil, false, Fail(
			ExitConflict,
			"selection",
			"No checkpoint has that ID; choose one of: "+strings.Join(
				choices,
				", ",
			),
		)
	}
	if selected.journal.RecoveryCreated &&
		(selected.journal.Status == JournalRunning || selected.journal.Status == JournalPartial) &&
		((selected.journal.Direction == "reverse") != reverse) {
		return nil, false, Fail(
			ExitConflict,
			"recovery",
			"Resume or reconcile the interrupted recovery direction before undoing it",
		)
	}
	return selected, reverse, nil
}

func (cp *Checkpoint) preflight(reverse bool) error {
	c := cp.mutation.context
	for i, change := range cp.changes {
		if cp.journal.Known[i] == outcomeUnknown {
			return Fail(
				ExitConflict,
				"recovery",
				"Checkpoint has unknown post-images; reviewed reconciliation is required",
			)
		}
		expected := cp.expectedImage(i)
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return err
		}
		if !sameImage(current, expected) {
			return Fail(
				ExitConflict,
				"conflict",
				"At least one target has later edits; no targets were restored",
			)
		}
	}
	return preflightDirectories(c, cp.changes, reverse)
}

func preflightDirectories(c Context, changes []TargetChange, reverse bool) error {
	desired := map[string]Image{}
	for _, change := range changes {
		image := change.After
		if reverse {
			image = change.Before
		}
		desired[change.Path] = image
	}
	for _, change := range changes {
		image := desired[change.Path]
		if image.Kind != ImageAbsent {
			continue
		}
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return err
		}
		if current.Kind != ImageDirectory {
			continue
		}
		entries, err := os.ReadDir(change.Path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			child, ok := desired[filepath.Join(change.Path, entry.Name())]
			if !ok || child.Kind != ImageAbsent {
				return Fail(
					ExitConflict,
					"conflict",
					"Directory contains uncheckpointed children; no targets were restored",
				)
			}
		}
	}
	return nil
}

// RecoveryPlan previews restoring the selected checkpoint after checking
// every target still holds its recorded post-image.
func RecoveryPlan(c Context, selector RecoverySelector) (_ Plan, err error) {
	defer Annotate(&err, "plan checkpoint recovery")
	plan := Plan{
		Scope: c.Scope,
		RecoveryLimits: []string{
			"Restores exact retained files only; packages, extensions, registry/environment, services and uncheckpointed script writes are not reverted",
		},
	}
	cp, reverse, err := selectCheckpoint(c, selector)
	if err != nil {
		return plan, err
	}
	cp.mutation = &Mutation{context: c}
	if err = cp.preflight(reverse); err != nil {
		return plan, err
	}
	plan.Source = cp.record.Applied
	if reverse && cp.record.Before != nil {
		plan.Source = *cp.record.Before
	}
	data, _ := json.Marshal(struct {
		Record  checkpointRecord
		Journal checkpointJournal
		Reverse bool
	}{cp.record, cp.journal, reverse})
	plan.Inputs = append(plan.Inputs, Input{Name: "checkpoint", Digest: SHA256Hex(data)})
	for i, change := range cp.changes {
		current := cp.expectedImage(i)
		target := change.After
		if reverse {
			target = change.Before
		}
		plan.Inputs = append(plan.Inputs, Input{Name: change.Path, Digest: ImageDigest(current)})
		if !sameImage(current, target) {
			plan.Edits = append(
				plan.Edits,
				Edit{
					Path:        change.Path,
					Action:      "restore",
					Description: "Restore exact retained configuration image",
				},
			)
		}
	}
	plan.Complete = true
	return plan, nil
}

// Recover restores the selected checkpoint under consent for displayed,
// returning the recovery operation's ID.
func Recover(
	ctx context.Context,
	c Context,
	displayed Plan,
	selector RecoverySelector,
	consent Consent,
) (_ string, err error) {
	defer Annotate(&err, "recover checkpoint")
	c.ReadOnly = false
	operationID := ""
	err = WithMutation(
		ctx,
		c,
		displayed,
		consent,
		func(_ context.Context, preview Context) (Plan, error) { return RecoveryPlan(preview, selector) },
		func(m *Mutation) error {
			cp, reverse, err := selectCheckpoint(c, selector)
			if err != nil {
				return err
			}
			cp.mutation = m
			if err = cp.preflight(reverse); err != nil {
				return err
			}
			operationID, err = NewID()
			if err != nil {
				return err
			}
			cp.journal.OperationID = operationID
			if !cp.journal.RecoveryCreated {
				cp.journal.PairPost = append([]targetOutcome{}, cp.journal.Known...)
				for i, known := range cp.journal.PairPost {
					if known == outcomeBefore {
						cp.changes[i].After = cp.changes[i].Before
						cp.journal.PostAttributes[i] = cp.changes[i].Before.Attributes
					}
				}
			}
			cp.journal.RecoveryCreated = true
			cp.journal.Direction = "forward"
			if reverse {
				cp.journal.Direction = "reverse"
			}
			cp.journal.Status = JournalRunning
			if err = cp.saveJournal(); err != nil {
				return err
			}
			var state *State
			if c.Scope.Kind == "machine" {
				state, err = ReadState(c.Paths)
				if err != nil {
					return err
				}
				if state == nil {
					state = &State{SchemaVersion: 1}
				}
				state.PartialOperation = &PartialOperation{ID: operationID, Scope: c.Scope}
				if err = m.WriteState(*state); err != nil {
					return err
				}
			}
			if err = cp.applyImages(ctx, reverse); err != nil {
				return err
			}
			return finishRecovery(c, m, state, cp, reverse)
		},
	)
	return operationID, err
}

// finishRecovery records the restored configuration in a machine scope's
// state: what the checkpoint applied, or with reverse what it replaced. An
// isolated destination only clears its partial operation; it never changes
// what the machine reports as applied.
func finishRecovery(c Context, m *Mutation, state *State, cp *Checkpoint, reverse bool) error {
	if state == nil {
		return nil
	}
	if c.RecordsHome() {
		state.AppliedConfiguration = &cp.record.Applied
		if reverse {
			state.AppliedConfiguration = cp.record.Before
		}
	}
	state.PartialOperation = nil
	if err := m.WriteState(*state); err != nil {
		return Fail(
			ExitPartial,
			"state",
			"Files restored; current-state finalization failed; retained checkpoint remains available",
		)
	}
	return nil
}
