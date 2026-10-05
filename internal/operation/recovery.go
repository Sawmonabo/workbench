package operation

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// preflight checks that every target still holds the image the checkpoint
// recorded, and returns the folders the restore would remove that now hold
// something it would not remove (see heldDirectories).
func (cp *Checkpoint) preflight(reverse bool) (map[int]string, error) {
	c := cp.mutation.context
	var edited []string
	for i, change := range cp.changes {
		if cp.journal.Known[i] == outcomeUnknown {
			return nil, Fail(
				ExitConflict,
				"recovery",
				"Workbench could not confirm every file this checkpoint wrote, so revert cannot undo it",
			)
		}
		expected := cp.expectedImage(i)
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return nil, err
		}
		if !sameImage(current, expected) {
			edited = append(edited, c.ShowPath(change.Path))
		}
	}
	if len(edited) > 0 {
		// Name the files, so the person knows which ones to put back.
		const shown = 10
		names := strings.Join(edited[:min(len(edited), shown)], ", ")
		if len(edited) > shown {
			names += fmt.Sprintf(" and %d more", len(edited)-shown)
		}
		return nil, Fail(
			ExitConflict,
			"conflict",
			"Changed since Workbench wrote them, so nothing was restored: "+names+
				". Undo those changes, then run revert again",
		)
	}
	return heldDirectories(c, cp.changes, reverse)
}

// heldDirectories returns, by change index, each folder the change set would
// remove that now holds something it would not remove: a file Workbench did not
// write, or a folder that is itself held. Removal never recurses, so such a
// folder can only stay. Each is named with what holds it.
func heldDirectories(c Context, changes []TargetChange, reverse bool) (map[int]string, error) {
	desired := map[string]Image{}
	for _, change := range changes {
		image := change.After
		if reverse {
			image = change.Before
		}
		desired[change.Path] = image
	}
	var removed []int
	for i, change := range changes {
		if desired[change.Path].Kind != ImageAbsent {
			continue
		}
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return nil, err
		}
		if current.Kind == ImageDirectory {
			removed = append(removed, i)
		}
	}
	// A folder's path sorts after its parent's, so going from the last path
	// back settles every folder before the one that holds it.
	slices.SortFunc(
		removed,
		func(a, b int) int { return strings.Compare(changes[b].Path, changes[a].Path) },
	)
	held := map[int]string{}
	heldPaths := map[string]bool{}
	for _, i := range removed {
		path := changes[i].Path
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		var others []string
		for _, entry := range entries {
			child := filepath.Join(path, entry.Name())
			image, ok := desired[child]
			if !ok || image.Kind != ImageAbsent || heldPaths[child] {
				others = append(others, entry.Name())
			}
		}
		if len(others) == 0 {
			continue
		}
		const shown = 3
		names := strings.Join(others[:min(len(others), shown)], ", ")
		if len(others) > shown {
			names += fmt.Sprintf(" and %d more", len(others)-shown)
		}
		heldPaths[path] = true
		held[i] = c.ShowPath(path) + " (" + names + ")"
	}
	return held, nil
}

// heldNames lists the held folders in path order.
func heldNames(held map[int]string) []string {
	names := slices.Collect(maps.Values(held))
	slices.Sort(names)
	return names
}

// preflightDirectories refuses an apply that would remove a folder now holding
// files Workbench did not write. Only a revert leaves such a folder in place.
func preflightDirectories(c Context, changes []TargetChange, reverse bool) error {
	held, err := heldDirectories(c, changes, reverse)
	if err != nil || len(held) == 0 {
		return err
	}
	return heldConflict(held)
}

func heldConflict(held map[int]string) error {
	// Name the folders and what is in them, so the person knows why.
	return Fail(
		ExitConflict,
		"conflict",
		"Folders this would remove now hold files Workbench did not write, so nothing was changed: "+
			strings.Join(
				heldNames(held),
				"; ",
			),
	)
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
	held, err := cp.preflight(reverse)
	if err != nil {
		return plan, err
	}
	// A folder that now holds other files stays; everything else is restored.
	for _, name := range heldNames(held) {
		plan.Warnings = append(
			plan.Warnings,
			"Stays, because it holds files Workbench did not write: "+name,
		)
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
		if _, stays := held[i]; !stays && !sameImage(current, target) {
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
			if _, err = cp.preflight(reverse); err != nil {
				return err
			}
			// Refuse before the journal changes: only this checkpoint's own
			// unfinished apply or recovery may be replaced in the state.
			var state *State
			if c.Scope.Kind == "machine" {
				state, err = ReadState(c.Paths)
				if err != nil {
					return err
				}
				if err = state.Unfinished(
					c.Scope,
					cp.record.ID,
					cp.journal.OperationID,
				); err != nil {
					return err
				}
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
			if c.Scope.Kind == "machine" {
				if state == nil {
					state = &State{SchemaVersion: 1}
				}
				state.PartialOperation = &PartialOperation{ID: operationID, Scope: c.Scope}
				if err = m.WriteState(*state); err != nil {
					return err
				}
			}
			if err = cp.applyImages(ctx, reverse, true); err != nil {
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
