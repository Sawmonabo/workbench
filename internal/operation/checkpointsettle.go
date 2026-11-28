package operation

import (
	"bytes"
	"fmt"
	"slices"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// Settled is the unfinished operation the state recorded in a scope, which
// [SettleUnfinished] settled: its ID, whether it is a revert, whether its
// checkpoint is gone, so nothing of it could be checked, and the paths of the
// targets the settling's pass after native changed.
type Settled struct {
	ID       string
	Revert   bool
	Gone     bool
	Finished []string
}

// SettleUnfinished settles, under m, the operation state records as unfinished
// in m's scope, before an apply replaces or clears that record; it returns nil
// when there is none. For an apply that stopped before it recorded every
// target, it first does the pass after native (see [Checkpoint.FinalizeNative])
// on each target whose outcome the journal does not know and that holds what
// native wrote, then records each of them as it holds after that: written when
// it holds its approved image, untouched when it holds its earlier one. So a
// file chezmoi wrote before Workbench was killed gets the group, ACL, extended
// attributes, hidden flag and label its approved image holds, and none is lost
// once the record goes. Until it can, the apply stays unfinished: a target the
// pass could not finish fails the call with its error, and so does one that
// holds neither image and lacks the group, label, ACL, extended attributes or
// hidden flag its approved image holds (lostMetadata), since someone changed it
// after native wrote it, and a plan, which compares content, would find nothing
// to write. A target that holds neither image and lacks none of them stays as
// it is, its outcome unknown: its content is someone's change, which the next
// plan shows. A target that is gone lacks nothing either; the next plan writes
// it again. changes are the approved plan's: a target the settling's pass
// changed, which that plan also changes, does not hold the image the plan was
// made from, and fails the call too. A revert writes whole images and gets no
// pass.
func SettleUnfinished(m *Mutation, state *State, changes []TargetChange) (*Settled, error) {
	if err := m.Check(); err != nil {
		return nil, err
	}
	c := m.context
	if state == nil || state.PartialOperation == nil || state.PartialOperation.Scope != c.Scope {
		return nil, nil
	}
	settled := &Settled{ID: state.PartialOperation.ID}
	checkpoints, err := loadCheckpoints(c)
	if err != nil {
		return nil, err
	}
	at := slices.IndexFunc(checkpoints, func(cp *Checkpoint) bool {
		return cp.journal.OperationID == settled.ID
	})
	if at < 0 {
		settled.Gone = true
		return settled, nil
	}
	cp := checkpoints[at]
	cp.mutation = m
	if cp.journal.RecoveryCreated || cp.journal.Direction != "forward" {
		settled.Revert = true
		return settled, nil
	}
	var unknown []int
	for i, known := range cp.journal.Known {
		if known == outcomeUnknown {
			unknown = append(unknown, i)
		}
	}
	if len(unknown) == 0 {
		return settled, nil
	}
	finished, passErr := cp.restoreNativeMetadata(unknown, true)
	lacking, err := cp.observeSettled(unknown)
	if err != nil {
		return nil, err
	}
	for _, i := range finished {
		settled.Finished = append(settled.Finished, cp.changes[i].Path)
	}
	if passErr != nil {
		return nil, passErr
	}
	if len(lacking) > 0 {
		return nil, cp.changedAfterNative(lacking, settled.Finished)
	}
	return settled, planPredates(cp.ID, settled.Finished, changes)
}

// observeSettled records the outcome of each target indexed by targets as it
// holds after the pass, by the rule revert's preflight decides an unknown outcome by
// (see confirm), and the checkpoint's status as for a run that was stopped,
// then saves the journal. A target holding neither image stays unknown; it
// returns the index of each that lacks metadata its approved image holds
// (lostMetadata).
func (cp *Checkpoint) observeSettled(targets []int) ([]int, error) {
	c := cp.mutation.context
	var lacking []int
	for _, i := range targets {
		change := cp.changes[i]
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return nil, err
		}
		switch {
		case sameImageContent(current, change.After) &&
			holdsAttributes(current.Attributes, change.After.Attributes):
			cp.journal.Known[i] = outcomeAfter
			cp.journal.PostAttributes[i] = current.Attributes
			cp.changes[i].After.Attributes = current.Attributes
		case sameImageContent(current, change.Before):
			cp.journal.Known[i] = outcomeBefore
		default:
			if len(lostMetadata(change, current)) > 0 {
				lacking = append(lacking, i)
			}
			continue
		}
		cp.journal.ObservedAttributes[i] = current.Attributes
	}
	changed, unknown := false, false
	for i, known := range cp.journal.Known {
		unknown = unknown || known == outcomeUnknown
		changed = changed || known == outcomeAfter &&
			!sameImage(cp.changes[i].Before, cp.expectedImage(i))
	}
	if validateChanges(c, cp.changes) != nil {
		unknown = true
	}
	cp.journal.Status = outcomeStatus(true, changed, unknown)
	return lacking, cp.saveJournal()
}

// lostMetadata names what current, a target that holds neither of change's
// images, lacks of the metadata change's approved image holds and a plan,
// which compares content, does not see: its group (not a link's, which
// Workbench never changes), its SELinux label (foreignLabel, nativeLabel),
// and, where the approved image carries them from the one it replaced, its
// ACL, extended attributes and hidden flag. A target that is gone lacks
// nothing: the next plan writes it.
func lostMetadata(change TargetChange, current Image) []string {
	if current.Kind == ImageAbsent || change.After.Kind == ImageAbsent {
		return nil
	}
	var lost []string
	if current.Kind != ImageSymlink && current.Group != nil && change.After.Group != nil &&
		*current.Group != *change.After.Group {
		lost = append(lost, "group")
	}
	if foreignLabel(change, current) || nativeLabel(change, current) != nil {
		lost = append(lost, "SELinux label")
	}
	if !carries(change) {
		return lost
	}
	acl := !bytes.Equal(current.ACL, change.After.ACL)
	attributes := false
	for name, value := range change.After.Attributes {
		if name == platform.LabelAttribute {
			continue
		}
		if other, ok := current.Attributes[name]; !ok || !bytes.Equal(value, other) {
			if name == platform.AccessACLAttribute {
				acl = true
			} else {
				attributes = true
			}
		}
	}
	if acl {
		lost = append(lost, "ACL")
	}
	if attributes {
		lost = append(lost, "extended attributes")
	}
	if current.Hidden != change.After.Hidden {
		lost = append(lost, "hidden flag")
	}
	return lost
}

// changedAfterNative is the failure of settling the apply of cp, which
// stopped before the pass after native, while the targets indexed by lacking
// hold neither of their images and lack metadata their approved images hold
// (lostMetadata). finished are the paths the settling's pass gave their metadata to.
// Undoing those changes lets the pass finish; where every one lacks only its
// group, so does giving each its group.
func (cp *Checkpoint) changedAfterNative(lacking []int, finished []string) error {
	paths := make([]string, len(lacking))
	var lost []string
	groupOnly := len(lacking) <= 3
	fixes := make([]Part, 0, len(lacking))
	for n, i := range lacking {
		change := cp.changes[i]
		paths[n] = change.Path
		current, err := ReadImage(cp.mutation.context, change.Path)
		if err != nil {
			return err
		}
		names := lostMetadata(change, current)
		for _, name := range names {
			if !slices.Contains(lost, name) {
				lost = append(lost, name)
			}
		}
		groupOnly = groupOnly && len(names) == 1 && names[0] == "group"
		if groupOnly {
			command := []string{"chgrp", groupName(*change.After.Group)}
			if permitted, err := canSetGroup(*change.After.Group); err != nil || !permitted {
				command = append([]string{"sudo"}, command...)
			}
			fixes = append(fixes, FixPath(change.Path, command...))
		}
	}
	verb := "lacks its "
	if len(paths) > 1 {
		verb = "lack their "
	}
	parts := []Part{Cause("apply " + cp.ID + " stopped after chezmoi wrote " + them(paths) +
		", and " + they(paths) + " changed before Workbench finished " + them(paths))}
	if len(finished) > 0 {
		parts = append(parts, Changed(shownPaths(finished)))
	}
	if !groupOnly {
		fixes = []Part{FixText("undo those changes")}
	}
	return Fail(
		ExitPartial,
		"recovery",
		shownPaths(paths)+" "+verb+display.List(lost),
		append(parts, fixes...)...,
	)
}

// planPredates refuses an apply whose approved changes include a target the
// settling of apply id changed, at one of finished: the plan holds the
// image the target had before.
func planPredates(id string, finished []string, changes []TargetChange) error {
	var both []string
	for _, change := range changes {
		if slices.Contains(finished, change.Path) {
			both = append(both, change.Path)
		}
	}
	if len(both) == 0 {
		return nil
	}
	return Fail(
		ExitConflict,
		"conflict",
		shownPaths(both)+" changed after the plan was made",
		Cause("Workbench finished apply "+id+", which stopped earlier, on "+them(both)),
		FixText("plan again"),
	)
}

// shownPaths names up to three paths as people read them, then how many
// more there are.
func shownPaths(paths []string) string {
	const shown = 3
	names := make([]string, 0, shown+1)
	for _, path := range paths[:min(len(paths), shown)] {
		names = append(names, display.Path(path))
	}
	if len(paths) > shown {
		names = append(names, fmt.Sprintf("%d more", len(paths)-shown))
	}
	return display.List(names)
}

// them is the pronoun for paths: "it" for one, "them" for more.
func them(paths []string) string {
	if len(paths) == 1 {
		return "it"
	}
	return "them"
}

// they is the subject pronoun for paths.
func they(paths []string) string {
	if len(paths) == 1 {
		return "it"
	}
	return "they"
}
