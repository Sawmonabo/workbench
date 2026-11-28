package operation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// RecoverySelector picks one checkpoint by ID, or by the release it restores.
type RecoverySelector struct{ Checkpoint string }

func selectCheckpoint(c Context, selector RecoverySelector) (*Checkpoint, bool, error) {
	if selector.Checkpoint == "" {
		return nil, false, Fail(
			ExitBlocked,
			"selection",
			"Revert needs a checkpoint ID",
			Cause("it never picks the latest checkpoint by itself"),
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
			"No checkpoint has that ID",
			Line{Label: "checkpoints", Text: strings.Join(choices, ", ")},
		)
	}
	if selected.journal.RecoveryCreated &&
		(selected.journal.Status == JournalRunning || selected.journal.Status == JournalPartial) &&
		((selected.journal.Direction == "reverse") != reverse) {
		resume := selected.record.ID
		if selected.journal.Direction == "forward" {
			resume = selected.record.RecoveryID
		}
		return nil, false, Fail(
			ExitConflict,
			"recovery",
			"Revert of checkpoint "+resume+" did not finish",
			Cause("this would undo it before it finished"),
			FixText(c.RevertCommand(resume)),
		)
	}
	return selected, reverse, nil
}

// preflight checks that every target still holds the image the checkpoint
// recorded, and returns the folders the restore would remove that now hold
// something it would not remove (see heldDirectories). A target whose outcome
// the journal could not record is decided first from what it holds now (see
// confirm). For a revert, a target that is not there counts as moved aside,
// which the revert writes over nothing, and one that holds the recorded image
// but for an ACL, extended attributes or the hidden flag it lacks altogether
// (lacksOnly) as it is, which the revert writes its image over: both are
// found, and nothing of a person's is lost. Any other target that holds
// neither image is refused (editedRefusal).
func (cp *Checkpoint) preflight(reverse bool) (map[int]string, error) {
	c := cp.mutation.context
	cp.regroup, cp.found = map[int]Image{}, map[int]Image{}
	var edited []int
	current := make([]Image, len(cp.changes))
	for i, change := range cp.changes {
		var err error
		if current[i], err = ReadImage(c, change.Path); err != nil {
			return nil, err
		}
		_, lacks := lacksOnly(current[i], cp.expectedImage(i))
		switch {
		case cp.journal.Known[i] == outcomeUnknown && cp.confirm(i, current[i]):
		case cp.journal.Known[i] != outcomeUnknown && sameImage(current[i], cp.expectedImage(i)):
		case cp.restoring && (current[i].Kind == ImageAbsent || lacks):
			cp.found[i] = current[i]
		default:
			edited = append(edited, i)
		}
	}
	if len(edited) > 0 {
		return nil, cp.editedRefusal(edited, current, reverse)
	}
	return heldDirectories(c, cp.changes, reverse)
}

// editedRefusal refuses a revert of cp while the targets indexed by edited,
// whose images are in current, hold neither image it recorded: someone
// changed them after Workbench wrote them. Its fix keeps each change and lets
// the revert go on: a file or link, or anything that replaced one, is moved
// aside under a name nothing has (movedAside), which preflight then takes as
// moved; a folder gets back the mode and group Workbench gave it, and loses
// an attribute or ACL it holds that Workbench did not give it, or holds with
// another value, the folder's content staying (folderFixes). Then the revert
// is run again. A project apply (cp not restoring) found its targets changed
// after its plan was made, and is run again.
func (cp *Checkpoint) editedRefusal(edited []int, current []Image, reverse bool) error {
	c := cp.mutation.context
	names := make([]string, len(edited))
	var fixes []Part
	for n, i := range edited {
		path := cp.changes[i].Path
		names[n] = c.ShowPath(path)
		expected := cp.expectedImage(i)
		if current[i].Kind != ImageDirectory || expected.Kind != ImageDirectory {
			fixes = append(
				fixes,
				FixText("mv -n "+c.ShellPath(path)+" "+c.ShellPath(movedAside(path))),
			)
			continue
		}
		fixes = append(fixes, folderFixes(c, path, current[i], expected)...)
	}
	const shown = 10
	listed := strings.Join(names[:min(len(names), shown)], ", ")
	if len(names) > shown {
		listed += fmt.Sprintf(" and %d more", len(names)-shown)
	}
	headline, parts := names[0]+" changed since Workbench wrote it", []Part{}
	if !cp.restoring {
		headline = names[0] + " changed after the plan was made"
	}
	if len(names) > 1 {
		headline = "Files changed since Workbench wrote them"
		if !cp.restoring {
			headline = "Files changed after the plan was made"
		}
		parts = append(parts, Line{Label: "files", Text: listed})
	}
	parts = append(parts, Changed("nothing"))
	if !cp.restoring {
		return Fail(
			ExitConflict,
			"conflict",
			headline,
			append(parts, FixText(c.ScopeApplyCommand()))...)
	}
	id := cp.record.ID
	if !reverse {
		id = cp.record.RecoveryID
	}
	return Fail(
		ExitConflict,
		"conflict",
		headline,
		append(append(parts, fixes...), FixText(c.RevertCommand(id)))...)
}

// foundWarning is what a revert plan says of the target at path that holds
// found, not expected, the image its checkpoint records, and that the revert
// takes as it is (see preflight): one that is not there, which the revert
// writes target over unless that is absent too, or one that lacks what
// lacksOnly names.
func foundWarning(c Context, path string, found, expected, target Image) []Warning {
	if found.Kind == ImageAbsent {
		if target.Kind == ImageAbsent {
			return nil
		}
		return []Warning{Warn(
			c.ShowPath(path)+" is not there",
			Outcome("revert writes it as the checkpoint holds it"),
		)}
	}
	l, _ := lacksOnly(found, expected)
	return []Warning{Warn(
		c.ShowPath(path)+" lacks its "+display.List(l.names()),
		Outcome("revert writes it as the checkpoint holds it"),
	)}
}

// movedAside is the name beside path a changed file is moved to, which
// nothing has: path with .edited, or .edited-2 and on after it.
func movedAside(path string) string {
	name := path + ".edited"
	for n := 2; ; n++ {
		if _, err := os.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			return name
		}
		name = fmt.Sprintf("%s.edited-%d", path, n)
	}
}

// folderFixes are the commands after which the folder at path, which holds
// current, holds expected, the image Workbench wrote, or holds it lacking an
// ACL, attributes or the hidden flag, which a revert writes over as it is
// (lacksOnly): an ACL expected does not hold as it is comes off, then the
// folder gets expected's group and mode, then each other attribute expected
// does not hold with its value comes off, and the hidden flag expected lacks.
// What expected holds and the folder lacks needs no line, so none carries a
// value. An ACL goes first, and the chmod follows a Linux access ACL's
// removal even where the mode matched, since taking it off sets the mode's
// group bits from its group entry (setfacl(1)); Linux and macOS let only a
// writer remove the other attributes. The chmod follows the chgrp, since
// macOS clears a folder's set-group-ID bit when a user other than root
// changes its group, and writes the mode in five octal digits, since GNU
// chmod keeps a folder's set-user-ID and set-group-ID bits for a mode of four
// digits (chmod 0755 leaves a folder of mode 2755 as it is) and clears them
// for one with a leading zero more (chmod 00755 gives it 0755); BSD chmod
// reads both forms as the same mode.
func folderFixes(c Context, path string, current, expected Image) []Part {
	shown := c.ShellPath(path)
	var acls, others []Part
	if current.ACL != nil && !bytes.Equal(current.ACL, expected.ACL) {
		acls = append(acls, FixText(shellWords(platform.ACLRemoval())+" "+shown))
	}
	modeChanges := current.Mode != expected.Mode
	for _, name := range slices.Sorted(maps.Keys(current.Attributes)) {
		value, kept := expected.Attributes[name]
		if kept && bytes.Equal(value, current.Attributes[name]) || name == platform.LabelAttribute {
			continue
		}
		kind, remove := platform.AttributeRemoval(name)
		fix := FixText(shellWords(remove) + " " + shown)
		if kind != "" {
			acls = append(acls, fix)
		} else {
			others = append(others, fix)
		}
		modeChanges = modeChanges || name == platform.AccessACLAttribute
	}
	fixes := acls
	if *current.Group != *expected.Group {
		fixes = append(fixes, FixText("chgrp "+groupName(*expected.Group)+" "+shown))
		modeChanges = true
	}
	if modeChanges {
		fixes = append(fixes, FixText(fmt.Sprintf("chmod %05o %s", expected.Mode, shown)))
	}
	fixes = append(fixes, others...)
	if current.Hidden && !expected.Hidden {
		if command := platform.ClearFlags(platform.HiddenFlag); command != nil {
			fixes = append(fixes, FixText(shellWords(command)+" "+shown))
		}
	}
	return fixes
}

// lacksOnly reports whether current, what a target holds now, is expected,
// the image its checkpoint records for it, but for an ACL, extended
// attributes or the hidden flag it lacks altogether, which someone took off
// after Workbench wrote it; l is what it lacks. A revert loses nothing of
// theirs then: all the target holds is in the recorded image, and the revert
// writes the image it restores in its place. What a target holds with
// another value, or more than the image holds (content, mode, group, an ACL
// with other entries, an attribute, the hidden flag), is theirs, and stops
// the revert with the lines that keep it or that they type to let it go
// (editedRefusal). On Linux an ACL is an attribute whose removal can change
// the mode's group bits, which then differ: that stops the revert too, with
// the chmod that gives the mode back.
func lacksOnly(current, expected Image) (l lacking, ok bool) {
	if current.Kind == ImageAbsent || current.Kind != expected.Kind ||
		!holdsAttributes(expected.Attributes, current.Attributes) ||
		current.ACL != nil && !bytes.Equal(current.ACL, expected.ACL) ||
		current.Hidden && !expected.Hidden {
		return l, false
	}
	for _, name := range slices.Sorted(maps.Keys(expected.Attributes)) {
		if _, held := current.Attributes[name]; held || name == platform.LabelAttribute {
			continue
		}
		if name == platform.AccessACLAttribute {
			l.acl = true
			continue
		}
		l.attributes = append(l.attributes, name)
	}
	l.acl = l.acl || expected.ACL != nil && current.ACL == nil
	l.hidden = expected.Hidden && !current.Hidden
	given := current
	given.ACL, given.Hidden = expected.ACL, expected.Hidden
	return l, !l.none() && sameImageContent(given, expected)
}

// confirm decides the outcome of target i, which the journal marked unknown
// before handing the target to a writer and could not settle because
// Workbench was stopped or crashed meanwhile, from what the target holds now:
// the approved image means written, the earlier one untouched. Only an exact
// match of either counts, so a revert never undoes anything the checkpoint did
// not write. A target native wrote before Workbench's pass after it
// (nativeImage) may still carry the group of native's temporary folder, or
// lack the ACL, hidden flag and attributes the pass gives a file or link that
// replaced one; it counts as written, and Recover finishes that pass before
// restoring anything (settleConfirmed). A link whose group differs is not
// such a target unless the pass writes it anew, since a link's group is never
// changed in place. It returns false, and records nothing, for a target that
// holds neither.
func (cp *Checkpoint) confirm(i int, current Image) bool {
	change := cp.changes[i]
	native := cp.journal.Direction == "forward" && !cp.journal.RecoveryCreated
	known := outcomeAfter
	switch {
	case sameImageContent(current, change.After) &&
		holdsAttributes(current.Attributes, change.After.Attributes):
	case native && nativeImage(current, change) &&
		(current.Kind != ImageSymlink || missingCarried(change, current)):
		cp.regroup[i] = current
	case sameImageContent(current, change.Before):
		known = outcomeBefore
	default:
		return false
	}
	cp.journal.Known[i] = known
	cp.journal.ObservedAttributes[i] = current.Attributes
	if native && known == outcomeAfter {
		cp.journal.PostAttributes[i] = current.Attributes
		cp.changes[i].After.Attributes = current.Attributes
	}
	return true
}

// settleConfirmed first removes what a stopped writer of the scope staged
// beside its targets (removeStagedIn), before the journal names this recovery
// instead, then finishes the pass after native on each target preflight
// confirmed as what native wrote before it (see confirm): a file or link that
// lacks what it carries is written anew (restageNative), and any other gets
// the group its apply approved. Each is then exactly the image the restore
// expects, whose attributes it records as observed; the revert then restores
// it, or keeps it if it is a folder holding other files.
func (cp *Checkpoint) settleConfirmed() error {
	c := cp.mutation.context
	if err := removeStagedIn(c); err != nil {
		return err
	}
	for i, observed := range cp.regroup {
		change := cp.changes[i]
		var err error
		if missingCarried(change, observed) {
			err = restageNative(c, change, observed, cp.stagedName(i))
		} else {
			err = correctNativeMetadata(c, change, observed, nil)
		}
		if err != nil {
			return err
		}
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return err
		}
		cp.journal.ObservedAttributes[i] = current.Attributes
		cp.journal.PostAttributes[i] = current.Attributes
		cp.changes[i].After.Attributes = current.Attributes
	}
	return nil
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
		"Folders this would remove hold files Workbench did not write",
		Line{Label: "folders", Text: strings.Join(heldNames(held), "; ")},
		Changed("nothing"),
	)
}

// RecoveryPlan previews restoring the selected checkpoint after checking
// every target still holds its recorded post-image.
func RecoveryPlan(c Context, selector RecoverySelector) (_ Plan, err error) {
	defer Annotate(&err, "plan checkpoint recovery")
	plan := Plan{
		Scope: c.Scope,
		RecoveryLimits: []string{
			"Revert puts back saved files only, not packages, extensions, the registry, environment variables, services or what scripts changed",
		},
	}
	cp, reverse, err := selectCheckpoint(c, selector)
	if err != nil {
		return plan, err
	}
	cp.mutation, cp.restoring = &Mutation{context: c}, true
	unconfirmed := slices.Contains(cp.journal.Known, outcomeUnknown)
	held, err := cp.preflight(reverse)
	if err != nil {
		return plan, err
	}
	if unconfirmed {
		plan.Warnings = append(
			plan.Warnings,
			Warn(
				"This apply stopped before Workbench recorded every file",
				Outcome(
					"revert checked each one now and restores only those holding exactly what the apply wrote",
				),
			),
		)
	}
	// A folder that now holds other files stays; everything else is restored.
	for _, name := range heldNames(held) {
		plan.Warnings = append(
			plan.Warnings,
			Warn(name+" stays", Cause("it holds files Workbench did not write")),
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
		found, isFound := cp.found[i]
		if isFound {
			plan.Warnings = append(
				plan.Warnings,
				foundWarning(c, change.Path, found, current, target)...)
			current = found
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
			cp.mutation, cp.restoring = m, true
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
				if err = state.Unfinished(c, cp.record.ID, cp.journal.OperationID); err != nil {
					return err
				}
			}
			if err = cp.settleConfirmed(); err != nil {
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
			"Files restored, but Workbench could not record it",
			Cause(err.Error()),
			Outcome("checkpoint "+cp.ID+" is kept"),
		)
	}
	return nil
}
