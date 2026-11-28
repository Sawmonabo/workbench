package operation

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
	"golang.org/x/sys/unix"
)

// Unfinished is the operation a scope's state records as unfinished, as the
// next apply of that scope settles it ([SettleUnfinished]): for an apply that
// stopped after chezmoi wrote and before Workbench's pass after it, each file
// that lacks what its approved image holds and chezmoi's write leaves out,
// and what the file holds once settling gives it that. A plan takes that as
// the file's image before it ([Unfinished.Image], [Unfinished.WithGroup]),
// shows it ([Unfinished.Note]) and binds it ([Unfinished.Input]), so what
// the settling puts right is never refused, and a change to it before the
// apply stops the apply. [PlanUnfinished] makes it; it changes nothing.
type Unfinished struct {
	// ID is the operation's, Root its scope's root.
	ID, Root string
	// Revert is set for a revert, which writes whole images and leaves
	// nothing to finish; Gone when the operation's checkpoint is gone, so
	// nothing of it can be checked.
	Revert, Gone bool
	// Removed is how the folder at Root is gone since the operation, after
	// its name ("is not there", "is a file"), so nothing of the operation is
	// left to finish there and any apply settles it; empty while it is there.
	Removed string
	// Targets are the files settling gives what they lack, in the order the
	// checkpoint records them.
	Targets []UnfinishedTarget
	// Missing are the files whose outcome the journal does not know that are
	// not there, where the apply writes them: someone removed them, or the
	// folder that holds them, after the stop.
	Missing []string
	// cp is the operation's checkpoint and unknown the index of each of its
	// targets whose outcome its journal does not know.
	cp      *Checkpoint
	unknown []int
	// linked is set when a link takes Root's place or leads to it.
	linked bool
}

// UnfinishedTarget is a file of an unfinished apply that lacks what its
// approved image holds: what it holds now, what it holds once settled, and
// what it lacks, named as a person reads it.
type UnfinishedTarget struct {
	Path             string
	Current, Settled Image
	Lacks            []string
	index            int
	// newLabel is set for a file made at a target that held none, which holds
	// a label: it may lack the SELinux label a file made in its own folder
	// gets, which only making one there tells, so the settling compares the
	// two (giveFolderLabel) and a plan says only that it may.
	newLabel bool
}

// given names what settling t gives it, as a person reads it: what it
// lacks, and the SELinux label a file made in its folder gets.
func (t UnfinishedTarget) given() []string {
	if t.newLabel && !slices.Contains(t.Lacks, labelName) {
		return append(slices.Clone(t.Lacks), labelName)
	}
	return t.Lacks
}

// labelName names the SELinux label as a person reads it.
const labelName = "SELinux label"

// PlanUnfinished reads, in c's scope, the operation state records as
// unfinished there, and what settling it does (see [Unfinished]); it returns
// nil when state records none for c's scope. The record of a scope whose
// folder is gone ([PartialOperation.gone]) is read in any scope, which
// settles it: nothing of it is left to finish there. A folder of c's scope
// Workbench cannot look up is refused. A file the settling cannot give what
// it lacks is refused here, as the settling would refuse it.
func PlanUnfinished(c Context, state *State) (*Unfinished, error) {
	if state == nil || state.PartialOperation == nil {
		return nil, nil
	}
	partial := state.PartialOperation
	removed, err := partial.gone()
	switch {
	case err != nil && partial.Scope == c.Scope:
		return nil, err
	case err == nil && removed.is != "":
		return &Unfinished{
			ID:      partial.ID,
			Root:    partial.Scope.Root,
			Removed: removed.is,
			linked:  removed.linked,
		}, nil
	case partial.Scope != c.Scope:
		return nil, nil
	}
	u := &Unfinished{ID: partial.ID, Root: c.Scope.Root}
	checkpoints, err := loadCheckpoints(c)
	if err != nil {
		return nil, err
	}
	at := slices.IndexFunc(checkpoints, func(cp *Checkpoint) bool {
		return cp.journal.OperationID == u.ID
	})
	if at < 0 {
		u.Gone = true
		return u, nil
	}
	u.cp = checkpoints[at]
	if u.cp.journal.RecoveryCreated || u.cp.journal.Direction != "forward" {
		u.Revert = true
		return u, nil
	}
	var refused []lackedGroup
	for i, known := range u.cp.journal.Known {
		if known != outcomeUnknown {
			continue
		}
		u.unknown = append(u.unknown, i)
		change := u.cp.changes[i]
		current, err := ReadImage(c, change.Path)
		if err != nil {
			return nil, err
		}
		if current.Kind == ImageAbsent && change.After.Kind != ImageAbsent {
			u.Missing = append(u.Missing, change.Path)
			continue
		}
		missing := lacksOf(change, current)
		if missing.none() {
			continue
		}
		if missing.group {
			permitted, err := canSetGroup(*change.After.Group)
			if err != nil {
				return nil, err
			}
			if !permitted {
				refused = append(refused, lackedGroup{change.Path, *change.After.Group})
			}
		}
		u.Targets = append(u.Targets, UnfinishedTarget{
			Path:     change.Path,
			Current:  current,
			Settled:  missing.settled(change, current),
			Lacks:    missing.names(),
			index:    i,
			newLabel: missing.newLabel,
		})
	}
	if len(refused) > 0 {
		return nil, groupRefusal(c, u.ID, refused)
	}
	return u, nil
}

// lackedGroup is a file an unfinished apply left without group, the group
// its approved image holds, which this user cannot give it.
type lackedGroup struct {
	path  string
	group uint32
}

// groupRefusal refuses the files of refused, which apply id left without
// the group its approved image holds, when this user cannot give them those
// groups: only a member of a group may give a file it (chown(2)), and sudo
// may. Each file gets its own sudo chgrp, so a copied line is one command.
func groupRefusal(c Context, id string, refused []lackedGroup) error {
	paths := make([]string, len(refused))
	var groups []string
	fixes := make([]Part, len(refused))
	for i, r := range refused {
		paths[i] = r.path
		if name := groupName(r.group); !slices.Contains(groups, name) {
			groups = append(groups, name)
		}
		fixes[i] = FixPath(r.path, "sudo", "chgrp", groupName(r.group))
	}
	headline := c.ShowPath(paths[0]) + " lacks its group " + groups[0]
	cause := "apply " + id + " stopped after chezmoi wrote it, and you are not a member of group " +
		groups[0] + ", so Workbench cannot give it that group"
	if len(paths) > 1 {
		which, those := "group "+groups[0], "that group"
		if len(groups) > 1 {
			which, those = "groups "+display.List(groups), "those groups"
		}
		headline = shownPaths(c, paths) + " lack their " + which
		cause = "apply " + id + " stopped after chezmoi wrote them, and you are not a member of " +
			which + ", so Workbench cannot give them " + those
	}
	return Fail(
		ExitPartial,
		"recovery",
		headline,
		append([]Part{Cause(cause), Changed("nothing")}, fixes...)...,
	)
}

// Image is what the target at path holds once u is settled: a file u gives
// what it lacks holds what [UnfinishedTarget] says, any other what it holds
// now ([ReadImage]). A nil u settles nothing.
func (u *Unfinished) Image(c Context, path string) (Image, error) {
	if t := u.target(path); t != nil {
		return t.Settled, nil
	}
	return ReadImage(c, path)
}

// WithGroup is [ImageWithGroup] of image at path once u is settled: a file u
// gives what it lacks keeps the group it then holds.
func (u *Unfinished) WithGroup(c Context, path string, image Image) (Image, error) {
	if t := u.target(path); t != nil {
		return imageWithGroup(c, path, t.Settled, image)
	}
	return ImageWithGroup(c, path, image)
}

// target is the file u gives what it lacks at path, or nil.
func (u *Unfinished) target(path string) *UnfinishedTarget {
	if u == nil {
		return nil
	}
	for i := range u.Targets {
		if u.Targets[i].Path == path {
			return &u.Targets[i]
		}
	}
	return nil
}

// Input binds u to a plan: the operation, how its folder is gone, its
// checkpoint's journal as it stands, each file's current and settled image,
// and the files that are not there. A plan made before any of them changed,
// or before the record went, does not match it.
func (u *Unfinished) Input() Input {
	type target struct{ Path, Current, Settled string }
	bound := struct {
		ID, Root, Checkpoint, Removed string
		Revert, Gone                  bool
		Sequence                      uint64
		Targets                       []target
		Missing                       []string
	}{ID: u.ID, Root: u.Root, Removed: u.Removed, Revert: u.Revert, Gone: u.Gone, Missing: u.Missing}
	if u.cp != nil {
		bound.Checkpoint, bound.Sequence = u.cp.ID, u.cp.journal.Sequence
	}
	for _, t := range u.Targets {
		bound.Targets = append(
			bound.Targets,
			target{t.Path, ImageDigest(t.Current), ImageDigest(t.Settled)},
		)
	}
	data, _ := json.Marshal(bound)
	return Input{Name: "unfinished", Digest: SHA256Hex(data)}
}

// Note is u as a plan and doctor show it: that the operation did not finish,
// and what the next apply does about it, with each file it finishes and what
// that file lacks, and the files that are not there.
func (u *Unfinished) Note(c Context) Warning {
	what := "Apply "
	if u.Revert {
		what = "Revert "
	}
	headline := what + u.ID + " to " + c.ShowPath(u.Root) + " did not finish"
	switch {
	case u.Removed != "":
		cause := "the destination " + u.Removed + ", so none of the files the apply wrote is left to finish"
		if u.linked {
			cause = "the destination " + u.Removed + ", so Workbench cannot finish the files the apply wrote there"
		}
		return Warn(
			headline,
			Cause(cause),
			Outcome("the next apply records it as finished and keeps its checkpoint"),
		)
	case u.Gone:
		return Warn(
			headline,
			Cause("its checkpoint is gone, so Workbench cannot check its files"),
			Outcome("the next apply records it as finished"),
		)
	case u.Revert:
		return Warn(
			headline,
			Outcome("the next apply takes its place and writes each file as it plans"),
		)
	case len(u.Targets) == 0 && len(u.Missing) == 0:
		return Warn(
			headline,
			Outcome("each file it wrote is finished; the next apply records it as finished"),
		)
	}
	var parts []Part
	if len(u.Targets) > 0 {
		parts = append(parts, Cause(
			"it stopped after chezmoi wrote files, before Workbench gave them what chezmoi leaves out",
		))
	}
	// One line for the files that lack the same, in the order the first of
	// them comes in, then one for the new files whose label only settling
	// can compare with the one a file made in their folder gets.
	var lacks []string
	var labeled []string
	paths := map[string][]string{}
	for _, t := range u.Targets {
		if t.newLabel {
			labeled = append(labeled, t.Path)
		}
		if len(t.Lacks) == 0 {
			continue
		}
		what := display.List(t.Lacks)
		if paths[what] == nil {
			lacks = append(lacks, what)
		}
		paths[what] = append(paths[what], t.Path)
	}
	for _, what := range lacks {
		files := paths[what]
		verb := " lacks its "
		if len(files) > 1 {
			verb = " lack their "
		}
		parts = append(parts, Line{Label: "files", Text: shownPaths(c, files) + verb + what})
	}
	if len(labeled) > 0 {
		verb := " is new, and may lack the SELinux label a file made in its folder gets"
		if len(labeled) > 1 {
			verb = " are new, and may lack the SELinux label a file made in their folder gets"
		}
		parts = append(parts, Line{Label: "files", Text: shownPaths(c, labeled) + verb})
	}
	if len(u.Missing) > 0 {
		verb := " is not there"
		if len(u.Missing) > 1 {
			verb = " are not there"
		}
		parts = append(parts, Line{Label: "files", Text: shownPaths(c, u.Missing) + verb})
	}
	result := "the next apply records it as finished"
	if len(u.Targets) > 0 {
		result = "the next apply gives each file what it lacks before it writes anything, " +
			"and leaves its content as it is"
	}
	return Warn(headline, append(parts, Outcome(result))...)
}

// lacking is what a target of an unfinished apply lacks of what its approved
// image holds and chezmoi's write leaves out (lacksOf).
type lacking struct {
	// group is set when the target has another group than the approved one,
	// and setgid when a folder lacks the set-group-ID bit.
	group, setgid bool
	// label is the label a file or folder that replaced one of its kind had
	// before, to put back (nativeLabel); newLabel is set for a file made at
	// a target that held none, which holds a label and may lack the label a
	// file made in its own folder gets (foreignLabel): only settling tells.
	label    []byte
	newLabel bool
	// attributes are the extended attributes it lacks, acl whether it lacks
	// the ACL and hidden whether it lacks the hidden flag, each one the
	// approved image carries from the file or link it replaced.
	attributes []string
	acl        bool
	hidden     bool
}

// lacksOf is what current, a target of change whose outcome the journal does
// not know, lacks of what change's approved image holds and chezmoi's write
// leaves out: its group (not a link's, which Workbench never changes), a
// folder's set-group-ID bit, its SELinux label (nativeLabel, foreignLabel),
// and, where the approved image carries them from the one it replaced, each
// extended attribute, the ACL and the hidden flag it lacks altogether. What
// it holds of them with another value, someone gave it after chezmoi wrote
// it, and stays. A target that holds its earlier image, that is gone, or
// that is now of another kind lacks nothing: the next plan shows it.
func lacksOf(change TargetChange, current Image) lacking {
	var l lacking
	after := change.After
	if current.Kind == ImageAbsent || current.Kind != after.Kind ||
		sameImageContent(current, change.Before) {
		return l
	}
	l.group = current.Kind != ImageSymlink && *current.Group != *after.Group
	l.setgid = current.Kind == ImageDirectory && after.Mode&unix.S_ISGID != 0 &&
		current.Mode&unix.S_ISGID == 0
	l.label = nativeLabel(change, current)
	l.newLabel = foreignLabel(change, current)
	if !carries(change) {
		return l
	}
	for _, name := range slices.Sorted(maps.Keys(after.Attributes)) {
		if _, held := current.Attributes[name]; held || name == platform.LabelAttribute {
			continue
		}
		if name == platform.AccessACLAttribute {
			l.acl = true
			continue
		}
		l.attributes = append(l.attributes, name)
	}
	l.acl = l.acl || after.ACL != nil && current.ACL == nil
	l.hidden = after.Hidden && !current.Hidden
	return l
}

// none reports whether l lacks nothing.
func (l lacking) none() bool {
	return !l.group && !l.setgid && l.label == nil && !l.newLabel && len(l.attributes) == 0 &&
		!l.acl && !l.hidden
}

// content reports whether l lacks nothing but a label, which image
// comparisons leave out: a target that holds its approved image's content
// and lacks only that counts as written.
func (l lacking) content() bool {
	l.label, l.newLabel = nil, false
	return l.none()
}

// names names what l lacks, as a person reads it: a label a new file may
// lack is not known before settling (UnfinishedTarget.given).
func (l lacking) names() []string {
	var names []string
	for _, lack := range []struct {
		lacks bool
		name  string
	}{
		{l.group, "group"},
		{l.setgid, "set-group-ID bit"},
		{l.label != nil, labelName},
		{l.acl, "ACL"},
		{len(l.attributes) > 0, "extended attributes"},
		{l.hidden, "hidden flag"},
	} {
		if lack.lacks {
			names = append(names, lack.name)
		}
	}
	return names
}

// settled is current, a target of change, once it holds what it lacks (l)
// of change's approved image: the ACL with the mask, owner and other entries
// current's mode gives them, since giving a file an access ACL sets its mode
// from the ACL (posix_acl_update_mode). The label a file made in its own
// folder gets is not known before one is made, and image comparisons leave
// the label out.
func (l lacking) settled(change TargetChange, current Image) Image {
	after, settled := change.After, current
	settled.Attributes = maps.Clone(current.Attributes)
	if settled.Attributes == nil {
		settled.Attributes = map[string][]byte{}
	}
	if l.group {
		settled.Group = after.Group
	}
	if l.setgid {
		settled.Mode |= unix.S_ISGID
	}
	if l.label != nil {
		settled.Attributes[platform.LabelAttribute] = bytes.Clone(l.label)
	}
	for _, name := range l.attributes {
		settled.Attributes[name] = bytes.Clone(after.Attributes[name])
	}
	if acl, ok := after.Attributes[platform.AccessACLAttribute]; ok && l.acl &&
		platform.AccessACLAttribute != "" {
		settled.Attributes[platform.AccessACLAttribute] = platform.ACLForMode(acl, current.Mode)
	}
	if l.acl && after.ACL != nil && current.ACL == nil {
		settled.ACL = bytes.Clone(after.ACL)
	}
	settled.Hidden = current.Hidden || l.hidden
	return settled
}
