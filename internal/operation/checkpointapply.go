package operation

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
	"golang.org/x/sys/unix"
)

// StartNative persists uncertainty before handing authority to the native engine.
// An abrupt process death cannot make unobserved writes look safely reversible.
func (cp *Checkpoint) StartNative() error {
	cp.journal.Status = JournalRunning
	for i := range cp.journal.Known {
		cp.journal.Known[i] = outcomeUnknown
	}
	return cp.saveJournal()
}

// FinalizeNative is the pass after native: it gives each target native wrote
// what native's write leaves out. A file or link native replaced gets the ACL,
// hidden flag and extended attributes the approved image carries from the one
// it replaced, and a file native created, which took its label from native's
// temporary folder, the label a file made in its own folder gets: Workbench
// writes the approved image anew over native's (restageNative). Any other
// target gets the approved group, which native's atomic creation may have
// taken from its temporary folder, and the label a replaced target had (see
// nativeLabel). Every target's expected content, type and mode is checked
// first; unknown content never authorizes metadata edits. It is called only
// following successful native execution.
func (cp *Checkpoint) FinalizeNative() error {
	return cp.restoreNativeMetadata(cp.targets(), false)
}

// SettleFailedNative is FinalizeNative for a native run that failed (a script
// exited nonzero, say) or was interrupted once native had stopped. The files
// written before the failure carry the inherited group too, which
// [Checkpoint.Finish] would record as unknown outcomes. FinalizeNative's rule
// applies one target at a time: a target whose content, type and mode already
// match its approved image gets the approved group and its earlier label, and
// any other target, such as one the run never wrote or someone edited, is left
// exactly as it is. A
// target that cannot be corrected does not stop the others; Finish records each
// as it finds it. The only error it could return, a lost mutation, makes Finish
// fail as well.
func (cp *Checkpoint) SettleFailedNative() { _ = cp.restoreNativeMetadata(cp.targets(), true) }

// targets is the index of every target of the checkpoint.
func (cp *Checkpoint) targets() []int {
	indices := make([]int, len(cp.changes))
	for i := range indices {
		indices[i] = i
	}
	return indices
}

// restoreNativeMetadata is the pass after native on the targets indexed by
// targets, the shared body of FinalizeNative and SettleFailedNative. Without
// keepGoing, any target that does not match its approved image fails the call
// before any group or label is changed. With it, a target that does not
// match is left as it is and one that cannot be read or corrected does not
// stop the others; the first such error is returned.
func (cp *Checkpoint) restoreNativeMetadata(targets []int, keepGoing bool) error {
	if err := cp.mutation.Check(); err != nil {
		return err
	}
	c := cp.mutation.context
	var first error
	observed := map[int]Image{}
	for _, i := range targets {
		current, native, err := nativeOutput(c, cp.changes[i])
		if err == nil && !native && !keepGoing {
			err = differsFromNative(c, cp.changes[i].Path)
		}
		if err != nil {
			if !keepGoing {
				return err
			}
			first = cmp.Or(first, err)
			continue
		}
		if native {
			observed[i] = current
		}
	}
	for _, i := range targets {
		current, ok := observed[i]
		change := cp.changes[i]
		if !ok || change.After.Kind == ImageAbsent {
			continue
		}
		var err error
		label := nativeLabel(change, current)
		switch {
		case missingCarried(change, current) || foreignLabel(change, current):
			err = restageNative(c, change, current, cp.stagedName(i), cp.again(false))
		case *current.Group != *change.After.Group || current.Mode != change.After.Mode ||
			label != nil:
			err = correctNativeMetadata(c, change, current, label)
		default:
			continue
		}
		if err != nil {
			if !keepGoing {
				return err
			}
			first = cmp.Or(first, err)
		}
	}
	return first
}

// nativeLabel is the label to put back on a file or folder native replaced, or
// nil when there is none to put back. Native stages a file in its temporary
// folder whenever a rename from there reaches the target (renameio's
// TempDir), and a rename keeps the label the file got there, so a file that
// was there before gets its earlier label again, as Workbench's own replace
// keeps it (carryLabel). A file native creates has no label to put back: one
// that holds a label is written anew in its own folder (foreignLabel).
func nativeLabel(change TargetChange, observed Image) []byte {
	if change.Before.Kind != change.After.Kind ||
		change.Before.Kind != ImageFile && change.Before.Kind != ImageDirectory {
		return nil
	}
	label, ok := change.Before.Attributes[platform.LabelAttribute]
	if !ok || bytes.Equal(observed.Attributes[platform.LabelAttribute], label) {
		return nil
	}
	return label
}

// nativeImage reports whether current is what native wrote of change's
// approved image before the pass after it: the approved image apart from the
// group atomic creation may have taken from native's temporary folder, on a
// folder apart from the set-group-ID bit native's chmod of it takes off (Go's
// os.Chmod sets only the bits it is given) or a folder native made in such a
// folder lacks, and, on a file or link that replaced one of its kind, apart
// from the ACL and the hidden flag native's new one lacks. Extended attributes
// are compared by missingCarried and Finish.
func nativeImage(current Image, change TargetChange) bool {
	current.Group = change.After.Group
	if current.Kind == ImageDirectory {
		current.Mode |= change.After.Mode & unix.S_ISGID
	}
	if carries(change) {
		if current.ACL == nil {
			current.ACL = change.After.ACL
		}
		current.Hidden = current.Hidden || change.After.Hidden
	}
	return sameImageContent(current, change.After)
}

// carries reports whether change's approved image carries the metadata of the
// one it replaces (Carried): a file replacing a file, or a link a link.
func carries(change TargetChange) bool {
	kind := change.After.Kind
	return change.Before.Kind == kind && (kind == ImageFile || kind == ImageSymlink)
}

// missingCarried reports whether observed, what native wrote of change, lacks
// the ACL, hidden flag or an extended attribute the approved image carries
// from the one it replaced.
func missingCarried(change TargetChange, observed Image) bool {
	return carries(change) && (!bytes.Equal(observed.ACL, change.After.ACL) ||
		observed.Hidden != change.After.Hidden ||
		!holdsAttributes(observed.Attributes, change.After.Attributes))
}

// foreignLabel reports whether observed is a file native made at a target
// that held no file, and holds a label: native stages a file in its temporary folder and a
// rename keeps the label it got there (nativeLabel), not the one a file made
// in its own folder gets.
func foreignLabel(change TargetChange, observed Image) bool {
	_, labeled := observed.Attributes[platform.LabelAttribute]
	return labeled && observed.Kind == ImageFile && change.Before.Kind != ImageFile
}

// holdsAttributes reports whether held has each attribute of carried with its
// value, apart from the label, which carryLabel's rule decides. held may have
// more: the system adds some to a file it writes, such as macOS's provenance.
func holdsAttributes(held, carried map[string][]byte) bool {
	for name, value := range carried {
		if name == platform.LabelAttribute {
			continue
		}
		if other, ok := held[name]; !ok || !bytes.Equal(value, other) {
			return false
		}
	}
	return true
}

// restageNative writes change's approved image anew over observed, what
// native wrote, with Workbench's own write (rewriteImage): a new file or link,
// staged at temporary in the target's own folder, with the approved data,
// group, attributes, mode, hidden flag and ACL, renamed over native's once that
// is still there. A new file made in its own folder gets the label the system
// gives a file there.
func restageNative(c Context, change TargetChange, observed Image, temporary, again string) error {
	err := rewriteImage(c, change.Path, observed, change.After, temporary, again)
	if _, ok := errors.AsType[*Error](err); ok {
		headline, body := Explained(err)
		return FailWith(ExitPartial, "native_image", headline, body.With(Explain(
			Changed("chezmoi wrote "+display.Path(change.Path)+"; Workbench could not finish it"),
		)))
	}
	return err
}

// nativeOutput reads a target after a native run and reports whether it is
// what native wrote of the approved image (nativeImage).
func nativeOutput(c Context, change TargetChange) (Image, bool, error) {
	current, err := ReadImage(c, change.Path)
	if err != nil || !nativeImage(current, change) {
		return current, false, err
	}
	if current.Kind == ImageSymlink && *current.Group != *change.After.Group &&
		!missingCarried(change, current) {
		return Image{}, false, Fail(
			ExitBlocked,
			"metadata",
			"chezmoi gave the link "+display.Path(change.Path)+" group "+groupName(*current.Group)+
				", not "+groupName(*change.After.Group),
			Cause("Workbench does not change a link's group"),
			Outcome("the link stays as it is"),
		)
	}
	return current, true, nil
}

// differsFromNative is the failure of the pass after native on path, which
// does not hold what native wrote of its approved image: the next apply of
// c's scope finishes it.
func differsFromNative(c Context, path string) error {
	return Fail(
		ExitPartial,
		"native_image",
		display.Path(path)+" differs from what the plan approved",
		Cause("it changed after chezmoi wrote it"),
		Outcome("Workbench left its group and label as they are"),
		FixText(c.ScopeApplyCommand()),
	)
}

// correctNativeMetadata gives a target the approved group, a folder its
// approved set-group-ID bit, and, unless label is nil, that label, once it is
// still the image nativeOutput observed.
func correctNativeMetadata(c Context, change TargetChange, observed Image, label []byte) error {
	current, err := ReadImage(c, change.Path)
	if err != nil {
		return err
	}
	if !sameImage(current, observed) {
		return Fail(
			ExitPartial,
			"native_image",
			display.Path(change.Path)+" changed after chezmoi wrote it",
			Outcome("Workbench left it as it is, without the group the plan approved"),
			FixText(c.ScopeApplyCommand()),
		)
	}
	return enforceNativeMetadata(
		c,
		change.Path,
		observed,
		*change.After.Group,
		change.After.Mode,
		label,
	)
}

// CheckPreservable is the native planner's check of one edit: it refuses
// group-exclusive modes of files and folders, replacements that would change
// a target's group, files whose group native's staging folder cannot keep and
// a folder the edit removes under an ACL entry that denies this user deleting
// it (removedFolderACL). What native's replacement lacks of the file or link
// it replaces, FinalizeNative gives it.
func CheckPreservable(
	c Context,
	edit Edit,
	before, after Image,
	staging string,
) error {
	if edit.Action == "remove" && before.Kind == ImageDirectory {
		if err := removedFolderACL(edit.Path); err != nil {
			return err
		}
	}
	// Native atomic writes can initially inherit the scratch directory's
	// group. Until corrected under the checkpoint owner's exact-image gate,
	// never expose permissions available only to that transient group. Native
	// makes a link in the folder of the path it writes, never in the scratch
	// directory, and a link's group is never changed, so no transient group
	// holds a link.
	if after.Kind != ImageSymlink && ((after.Mode>>3)&7) & ^(after.Mode&7) != 0 {
		return Fail(
			ExitBlocked,
			"metadata",
			fmt.Sprintf(
				"The configuration gives %s mode %04o",
				display.Path(edit.Path),
				after.Mode,
			),
			Cause("its group may do more than other users, and Workbench's new file first has "+
				"the group of its temporary folder"),
		)
	}
	if after.Kind == ImageFile {
		if err := checkStagedGroup(c, edit.Path, staging); err != nil {
			return err
		}
	}
	if before.Kind != ImageFile && before.Kind != ImageSymlink {
		return nil
	}
	group, err := creationGroup(c, edit.Path)
	if err != nil {
		return err
	}
	if edit.Action != "remove" && before.Group != nil && *before.Group != group {
		return groupChangeRefusal(edit.Path, before.Kind == ImageSymlink, *before.Group, group)
	}
	return nil
}

// Finish records each target's observed outcome after a native run and
// returns runErr, or a partial or unknown-outcome error when targets changed. A
// runErr that already reports a partial apply is returned as it is.
func (cp *Checkpoint) Finish(runErr error) error {
	changed, unknown, incomplete := false, false, false
	for i, change := range cp.changes {
		current, err := ReadImage(cp.mutation.context, change.Path)
		if err == nil {
			cp.journal.ObservedAttributes[i] = current.Attributes
		}
		switch {
		case err == nil && sameImageContent(current, change.After) &&
			holdsAttributes(current.Attributes, change.After.Attributes):
			cp.journal.Known[i] = outcomeAfter
			cp.journal.PostAttributes[i] = current.Attributes
			cp.changes[i].After.Attributes = current.Attributes
			changed = changed || !sameImage(change.Before, current)
		case err == nil && sameImage(current, change.Before):
			cp.journal.Known[i] = outcomeBefore
			incomplete = incomplete || !sameImage(change.Before, change.After)
		default:
			cp.journal.Known[i] = outcomeUnknown
			unknown = true
		}
	}
	if err := validateChanges(cp.mutation.context, cp.changes); err != nil {
		unknown = true
	}
	if incomplete && runErr == nil {
		runErr = Fail(
			ExitFailed,
			"incomplete",
			"chezmoi did not write every file the plan approved",
		)
	}
	cp.journal.Status = outcomeStatus(runErr != nil, changed, unknown)
	if err := cp.saveJournal(); err != nil {
		if ExitCode(runErr) == ExitInterrupted {
			return runErr
		}
		return Fail(
			ExitPartial,
			"checkpoint",
			"Workbench could not record what this apply wrote",
			Cause(err.Error()),
			Outcome("revert of checkpoint "+cp.ID+" checks each file before it restores it"),
			FixText(cp.again(false)),
		)
	}
	if ExitCode(runErr) == ExitInterrupted {
		return runErr
	}
	if unknown {
		unconfirmed := Outcome(
			"Workbench could not confirm every file this apply wrote; revert of checkpoint " +
				cp.ID + " checks them again and names any it cannot undo",
		)
		if runErr == nil {
			return Fail(
				ExitPartial,
				"recovery",
				"Workbench could not confirm every file this apply wrote",
				Outcome(
					"revert of checkpoint "+cp.ID+" checks them again and names any it cannot undo",
				),
				FixText(cp.again(false)),
			)
		}
		// The failure comes first: it is what the person has to act on.
		headline, body := Explained(runErr)
		return FailWith(ExitPartial, "recovery", headline, body.With(Explain(unconfirmed)))
	}
	if changed && runErr != nil {
		// A failure that already reports a partial apply says what failed and what
		// to do; the line below adds that files changed, which revert restores.
		if ExitCode(runErr) == ExitPartial {
			return runErr
		}
		headline, body := Explained(runErr)
		return FailWith(ExitPartial, "partial", headline, body.With(Explain(
			Changed("some files; revert of checkpoint "+cp.ID+" restores them"),
		)))
	}
	return runErr
}

// outcomeStatus is the status of a native run's checkpoint once every
// target's outcome is recorded: unknown when one is, partial when the run
// failed after it changed a target, failed when it failed before, and
// complete otherwise.
func outcomeStatus(failed, changed, unknown bool) JournalStatus {
	switch {
	case unknown:
		return JournalUnknown
	case failed && changed:
		return JournalPartial
	case failed:
		return JournalFailed
	}
	return JournalComplete
}

// stoppedPartWay is the line of the failure of a write that stopped an apply
// or a revert after it may have changed other files, which its checkpoint
// records.
var stoppedPartWay = Outcome(
	"Workbench stopped there and kept its checkpoint of the files it changed",
)

// stoppedAt is err, an [Error] that stopped a write part way, with its
// headline, cause and fix, and stoppedPartWay after them.
func stoppedAt(err error) error {
	headline, body := Explained(err)
	return FailWith(ExitPartial, "partial", headline, body.With(Explain(stoppedPartWay)))
}

// writeTarget gives target i of cp, which holds expected, the image desired
// (writeImage), and records cp as stopped part way when the write fails.
func (cp *Checkpoint) writeTarget(i int, expected, desired Image, reverse bool) error {
	c, path := cp.mutation.context, cp.changes[i].Path
	err := writeImage(c, path, expected, desired, cp.stagedName(i), cp.again(reverse))
	if err != nil {
		cp.journal.Status = JournalPartial
		_ = cp.saveJournal()
		return writeFailure(path, err)
	}
	return nil
}

// writeFailure is the failure of the write to path that stopped an apply,
// which err says why: a refusal in its own words, which name the file,
// otherwise led by the file, with an os error's path shown as people read
// it (see [FileError]).
func writeFailure(path string, err error) error {
	if _, ok := errors.AsType[*Error](err); ok {
		return stoppedAt(err)
	}
	if problem, ok := errors.AsType[*fs.PathError](err); ok {
		err = FileError(problem.Path, err)
	}
	return Fail(
		ExitPartial,
		"partial",
		"Workbench did not finish writing "+display.Path(path),
		Cause(err.Error()),
		stoppedPartWay,
	)
}

// outcomeRefusal is the failure of a write to path whose result cannot be
// read back, which err says why, or differs from what Workbench wrote, with
// err nil.
func outcomeRefusal(path string, err error) error {
	if _, ok := errors.AsType[*Error](err); ok {
		return stoppedAt(err)
	}
	if err != nil {
		return Fail(
			ExitPartial,
			"partial",
			"Workbench cannot read "+display.Path(path)+" after writing it",
			Cause(err.Error()),
			stoppedPartWay,
		)
	}
	return Fail(
		ExitPartial,
		"partial",
		display.Path(path)+" does not hold what Workbench wrote",
		stoppedPartWay,
	)
}

// Apply is the shared exact-write owner for project configuration and recovery.
func (cp *Checkpoint) Apply(ctx context.Context) error { return cp.applyImages(ctx, false, false) }

// applyImages writes each target's image for the direction. A folder it would
// remove that now holds other files refuses the whole write, unless keepHeld
// (a revert) leaves it in place with the image it has.
func (cp *Checkpoint) applyImages(ctx context.Context, reverse, keepHeld bool) error {
	if err := cp.mutation.Check(); err != nil {
		return err
	}
	c := cp.mutation.context
	held, err := cp.preflight(reverse)
	if err != nil {
		return err
	}
	if len(held) > 0 && !keepHeld {
		return heldConflict(held)
	}
	cp.journal.Status = JournalRunning
	if err := cp.saveJournal(); err != nil {
		return err
	}
	indices := make([]int, len(cp.changes))
	for i := range indices {
		indices[i] = i
	}
	// Creation goes parent-first, removal child-first; no recursive deletion.
	slices.SortFunc(indices, func(a, b int) int {
		x, y := cp.changes[a], cp.changes[b]
		xImage, yImage := x.After, y.After
		if reverse {
			xImage, yImage = x.Before, y.Before
		}
		if xImage.Kind == ImageAbsent && yImage.Kind != ImageAbsent {
			return 1
		}
		if xImage.Kind != ImageAbsent && yImage.Kind == ImageAbsent {
			return -1
		}
		if xImage.Kind == ImageAbsent {
			return strings.Compare(y.Path, x.Path)
		}
		return strings.Compare(x.Path, y.Path)
	})
	for _, i := range indices {
		if err := ctx.Err(); err != nil {
			cp.journal.Status = JournalPartial
			_ = cp.saveJournal()
			return err
		}
		if _, stays := held[i]; stays {
			// Preflight found it as the journal records it, so its outcome stands.
			continue
		}
		change := cp.changes[i]
		expected := cp.expectedImage(i)
		if found, ok := cp.found[i]; ok {
			expected = found
		}
		desired, known := change.After, outcomeAfter
		if reverse {
			desired, known = change.Before, outcomeBefore
		}
		if sameImage(expected, desired) {
			cp.journal.Known[i] = known
			continue
		}
		cp.journal.Known[i] = outcomeUnknown
		if err := cp.saveJournal(); err != nil {
			return err
		}
		if err := cp.writeTarget(i, expected, desired, reverse); err != nil {
			return err
		}
		observed, err := ReadImage(c, change.Path)
		if err != nil || !sameImageContent(observed, desired) {
			cp.journal.Status = JournalUnknown
			_ = cp.saveJournal()
			return outcomeRefusal(change.Path, err)
		}
		cp.journal.ObservedAttributes[i] = observed.Attributes
		if !reverse && !cp.journal.RecoveryCreated {
			cp.journal.PostAttributes[i] = observed.Attributes
			cp.changes[i].After.Attributes = observed.Attributes
		}
		cp.journal.Known[i] = known
		if err := cp.saveJournal(); err != nil {
			return Fail(
				ExitPartial,
				"checkpoint",
				"Workbench could not record that it wrote "+display.Path(change.Path),
				Cause(err.Error()),
				Outcome("checkpoint "+cp.ID+" keeps every file as it was before"),
			)
		}
	}
	cp.journal.Status = JournalComplete
	if err := cp.saveJournal(); err != nil {
		return Fail(
			ExitPartial,
			"checkpoint",
			"Workbench wrote every file but could not record it",
			Cause(err.Error()),
			Outcome("checkpoint "+cp.ID+" keeps every file as it was before"),
		)
	}
	return nil
}
