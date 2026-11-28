package operation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
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

// FinalizeNative restores only the approved group after native atomic creation
// inherited its temporary directory's group, and the label a replaced target
// had (see nativeLabel). Every target's expected content, type and mode is
// checked first; unknown content never authorizes metadata edits. It is called
// only following successful native execution.
func (cp *Checkpoint) FinalizeNative() error { return cp.restoreNativeMetadata(false) }

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
func (cp *Checkpoint) SettleFailedNative() { _ = cp.restoreNativeMetadata(true) }

// restoreNativeMetadata is the shared body of both. Without failed, any target
// that does not match its approved image fails the call before any group or
// label is changed.
func (cp *Checkpoint) restoreNativeMetadata(failed bool) error {
	if err := cp.mutation.Check(); err != nil {
		return err
	}
	c := cp.mutation.context
	observed := make([]Image, len(cp.changes))
	settled := make([]bool, len(cp.changes))
	for i, change := range cp.changes {
		current, err := nativeOutput(c, change)
		if err != nil {
			if failed {
				continue
			}
			return err
		}
		observed[i], settled[i] = current, true
	}
	for i, change := range cp.changes {
		if !settled[i] || change.After.Kind == ImageAbsent {
			continue
		}
		label := nativeLabel(change, observed[i])
		if *observed[i].Group == *change.After.Group && label == nil {
			continue
		}
		if err := correctNativeMetadata(c, change, observed[i], label); err != nil && !failed {
			return err
		}
	}
	return nil
}

// nativeLabel is the label to put back on a file or folder native replaced, or
// nil when there is none to put back. Native stages a file in its temporary
// folder whenever a rename from there reaches the target (renameio's
// TempDir), and a rename keeps the label the file got there, so a file that
// was there before gets its earlier label again, as Workbench's own replace
// keeps it (carryLabel). A file native creates keeps the label native's write
// gave it: it had none to keep.
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

// nativeOutput reads a target after a native run and returns it when it is the
// approved image apart from a group that atomic creation may have inherited.
func nativeOutput(c Context, change TargetChange) (Image, error) {
	current, err := ReadImage(c, change.Path)
	if err != nil {
		return Image{}, err
	}
	comparison := current
	comparison.Group = change.After.Group
	if !sameImageContent(comparison, change.After) {
		shown := display.Path(change.Path)
		return Image{}, Fail(
			ExitPartial,
			"native_image",
			shown+" differs from what the plan approved after chezmoi wrote it, so Workbench did not change its group or label",
		)
	}
	if current.Kind == ImageSymlink && *current.Group != *change.After.Group {
		return Image{}, Fail(
			ExitBlocked,
			"metadata",
			display.Path(change.Path)+" is a link chezmoi gave group "+groupName(*current.Group)+
				", not "+groupName(*change.After.Group)+
				", and Workbench does not change a link's group, so it left the link as it is",
		)
	}
	return current, nil
}

// correctNativeMetadata gives a target the approved group and, unless label is
// nil, that label, once it is still the image nativeOutput observed.
func correctNativeMetadata(c Context, change TargetChange, observed Image, label []byte) error {
	current, err := ReadImage(c, change.Path)
	if err != nil {
		return err
	}
	if !sameImage(current, observed) {
		shown := display.Path(change.Path)
		return Fail(
			ExitPartial,
			"native_image",
			shown+" changed after chezmoi wrote it, before Workbench gave it its group, so Workbench left it as it is",
		)
	}
	return enforceNativeMetadata(c, change.Path, observed, *change.After.Group, label)
}

// CheckPreservable is the native planner's check of one edit: it refuses
// replacements of metadata native's replacement would not have
// (nativeReplaceRefusal), group-exclusive modes of files and folders,
// replacements that would change a target's group and files whose group
// native's staging folder cannot keep.
func CheckPreservable(
	c Context,
	edit Edit,
	before, after Image,
	staging string,
) error {
	if err := nativeReplaceRefusal(edit, before, after); err != nil {
		return err
	}
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
				"The configuration gives %s mode %04o, which lets its group do more than other users, and Workbench does not write such a file, since it first has the group of Workbench's temporary folder",
				display.Path(edit.Path),
				after.Mode,
			),
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

// nativeReplaceRefusal refuses an edit in which native writes a new file or
// link in place of before, a file or link, that has what native's
// replacement does not have and the pass after native (FinalizeNative) does
// not put back: the hidden flag, an ACL, or an extended attribute but the
// label, which nativeLabel puts back, and, on macOS, the four small ones
// macOS adds that grant no access: download provenance and quarantine,
// Finder flags and the last used date. It checks them in that order, names
// the first it finds and gives the command that removes it.
func nativeReplaceRefusal(edit Edit, before, after Image) error {
	replaced := func(kind ImageKind) bool { return kind == ImageFile || kind == ImageSymlink }
	if edit.Action == "remove" || !replaced(before.Kind) || !replaced(after.Kind) {
		return nil
	}
	h := replacedFile
	if before.Kind == ImageSymlink {
		h = replacedLink
	}
	switch {
	case before.Hidden:
		return flagRefusal(edit.Path, platform.HiddenFlag, h)
	case before.ACL != nil:
		return Fail(
			ExitBlocked,
			"metadata",
			display.Path(
				edit.Path,
			)+" has an ACL, "+h.lost()+h.retry(
				platform.ACLRemoval(),
				edit.Path,
			),
		)
	}
	dropped := []string{
		platform.LabelAttribute,
		"com.apple.provenance",
		"com.apple.quarantine",
		"com.apple.FinderInfo",
		"com.apple.lastuseddate#PS",
	}
	if platform.ACLsAreAttributes {
		dropped = dropped[:1]
	}
	for _, name := range slices.Sorted(maps.Keys(before.Attributes)) {
		if !slices.Contains(dropped, name) {
			return attributeRefusal(edit.Path, name, h)
		}
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
		case err == nil && sameImageContent(current, change.After):
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
			"Native apply did not produce every approved target image",
		)
	}
	cp.journal.Status = JournalComplete
	if runErr != nil {
		cp.journal.Status = JournalFailed
	}
	if changed && runErr != nil {
		cp.journal.Status = JournalPartial
	}
	if unknown {
		cp.journal.Status = JournalUnknown
	}
	if err := cp.saveJournal(); err != nil {
		if ExitCode(runErr) == ExitInterrupted {
			return runErr
		}
		return Fail(
			ExitPartial,
			"checkpoint",
			"Target outcome could not be recorded; retained images require reviewed reconciliation",
		)
	}
	if ExitCode(runErr) == ExitInterrupted {
		return runErr
	}
	if unknown {
		cannot := "could not confirm every file this apply wrote (checkpoint " + cp.ID + "); revert checks them again and names any it cannot undo."
		if runErr == nil {
			return Fail(
				ExitPartial,
				"recovery",
				"Workbench "+cannot+" Run "+cp.mutation.context.WorkbenchCommand()+" apply again to check the files.",
			)
		}
		// The failure comes first: it is what the person has to act on.
		return Fail(
			ExitPartial,
			"recovery",
			strings.TrimRight(runErr.Error(), ".")+". Workbench also "+cannot,
		)
	}
	if changed && runErr != nil {
		// A failure that already reports a partial apply says what failed and what
		// to do; the generic line below would replace it.
		if ExitCode(runErr) == ExitPartial {
			return runErr
		}
		return Fail(
			ExitPartial,
			"partial",
			"Some target changes completed; recover using the retained checkpoint",
		)
	}
	return runErr
}

// stoppedPartWay ends the failure of a write that stopped an apply or a
// revert after it may have changed other files, which its checkpoint
// records.
const stoppedPartWay = ". Workbench stopped there and kept its checkpoint of the files it changed"

// writeFailure is the failure of the write to path that stopped an apply,
// which err says why: a refusal in its own words, which name the file,
// otherwise led by the file, with an os error's path shown as people read
// it (see [FileError]).
func writeFailure(path string, err error) error {
	reason := "Workbench did not finish writing " + display.Path(path) + ": " + err.Error()
	if problem, ok := errors.AsType[*Error](err); ok {
		reason = problem.Message
	} else if problem, ok := errors.AsType[*fs.PathError](err); ok {
		reason = "Workbench did not finish writing " + display.Path(path) + ": " +
			FileError(problem.Path, err).Error()
	}
	return Fail(ExitPartial, "partial", strings.TrimSuffix(reason, ".")+stoppedPartWay)
}

// outcomeRefusal is the failure of a write to path whose result cannot be
// read back, which err says why, or differs from what Workbench wrote, with
// err nil.
func outcomeRefusal(path string, err error) error {
	reason := display.Path(path) + " does not hold what Workbench wrote"
	if problem, ok := errors.AsType[*Error](err); ok {
		reason = problem.Message
	} else if err != nil {
		reason = "Workbench cannot read " + display.Path(path) + " after writing it: " + err.Error()
	}
	return Fail(ExitPartial, "partial", strings.TrimSuffix(reason, ".")+stoppedPartWay)
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
		if err := writeImage(cp.mutation.context, change.Path, expected, desired); err != nil {
			cp.journal.Status = JournalPartial
			_ = cp.saveJournal()
			return writeFailure(change.Path, err)
		}
		observed, err := ReadImage(cp.mutation.context, change.Path)
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
				"A target changed but journal update failed; recovery images are retained",
			)
		}
	}
	cp.journal.Status = JournalComplete
	if err := cp.saveJournal(); err != nil {
		return Fail(
			ExitPartial,
			"checkpoint",
			"Target changes completed but journal finalization failed; retained images remain available",
		)
	}
	return nil
}
