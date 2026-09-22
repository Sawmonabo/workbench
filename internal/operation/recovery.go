package operation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type RecoverySelector struct{ Checkpoint, Version string }

func selectCheckpoint(c Context, selector RecoverySelector) (*Checkpoint, bool, error) {
	if selector.Checkpoint != "" && selector.Version != "" {
		return nil, false, Fail(
			2,
			"selector",
			"Select either checkpoint ID or before-release version",
		)
	}
	if selector.Checkpoint == "" && selector.Version == "" {
		return nil, false, Fail(
			3,
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
	count := 0
	var choices []string
	for _, cp := range checkpoints {
		choices = append(choices, cp.record.ID)
		if cp.journal.RecoveryCreated {
			choices = append(choices, cp.record.RecoveryID)
		}
		if selector.Checkpoint == cp.record.ID ||
			(selector.Version != "" && cp.record.Before != nil && selector.Version == cp.record.Before.Release) {
			selected = cp
			reverse = true
			count++
		}
		if cp.journal.RecoveryCreated &&
			(selector.Checkpoint == cp.record.RecoveryID || selector.Version != "" && selector.Version == cp.record.Applied.Release) {
			selected = cp
			reverse = false
			count++
		}
	}
	if count != 1 {
		return nil, false, Fail(
			4,
			"selection",
			"Selection has zero or multiple matches; choose a checkpoint ID: "+strings.Join(
				choices,
				", ",
			),
		)
	}
	if selected.journal.RecoveryCreated &&
		(selected.journal.Status == "running" || selected.journal.Status == "partial") &&
		((selected.journal.Direction == "reverse") != reverse) {
		return nil, false, Fail(
			4,
			"recovery",
			"Resume or reconcile the interrupted recovery direction before undoing it",
		)
	}
	return selected, reverse, nil
}

func (cp *Checkpoint) preflight(reverse bool) error {
	c := cp.mutation.context
	for i, change := range cp.changes {
		if cp.journal.Known[i] == "unknown" {
			return Fail(
				4,
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
				4,
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
		if image.Kind != "absent" {
			continue
		}
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return err
		}
		if current.Kind != "directory" {
			continue
		}
		entries, err := os.ReadDir(change.Path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			child, ok := desired[filepath.Join(change.Path, entry.Name())]
			if !ok || child.Kind != "absent" {
				return Fail(
					4,
					"conflict",
					"Directory contains uncheckpointed children; no targets were restored",
				)
			}
		}
	}
	return nil
}

func RecoveryPlan(c Context, selector RecoverySelector) (Plan, error) {
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
	digest := sha256.Sum256(data)
	plan.Inputs = append(
		plan.Inputs,
		Input{Name: "checkpoint", Digest: hex.EncodeToString(digest[:])},
	)
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

func Recover(
	ctx context.Context,
	c Context,
	displayed Plan,
	selector RecoverySelector,
	consent Consent,
) (string, error) {
	c.ReadOnly = false
	operationID := ""
	err := WithMutation(
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
				cp.journal.PairPost = append([]string{}, cp.journal.Known...)
				for i, known := range cp.journal.PairPost {
					if known == "before" {
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
			cp.journal.Status = "running"
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
			if state != nil {
				state.AppliedConfiguration = &cp.record.Applied
				if reverse {
					state.AppliedConfiguration = cp.record.Before
				}
				state.PartialOperation = nil
				if err = m.WriteState(*state); err != nil {
					return Fail(
						5,
						"state",
						"Files restored; current-state finalization failed; retained checkpoint remains available",
					)
				}
			}
			return nil
		},
	)
	return operationID, err
}
