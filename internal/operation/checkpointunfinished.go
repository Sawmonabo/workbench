package operation

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	// Targets are the files settling gives what they lack, in the order the
	// checkpoint records them.
	Targets []UnfinishedTarget
	// cp is the operation's checkpoint and unknown the index of each of its
	// targets whose outcome its journal does not know.
	cp      *Checkpoint
	unknown []int
}

// UnfinishedTarget is a file of an unfinished apply that lacks what its
// approved image holds: what it holds now, what it holds once settled, and
// what it lacks, named as a person reads it.
type UnfinishedTarget struct {
	Path             string
	Current, Settled Image
	Lacks            []string
	index            int
	// newLabel is set when the file lacks the SELinux label a file made in
	// its own folder gets, which only making one there tells (newFileLabel).
	newLabel bool
}

// PlanUnfinished reads, in c's scope, the operation state records as
// unfinished there, and what settling it does (see [Unfinished]); it returns
// nil when state records none for c's scope. A file the settling cannot give
// what it lacks is refused here, as the settling would refuse it.
func PlanUnfinished(c Context, state *State) (*Unfinished, error) {
	if state == nil || state.PartialOperation == nil || state.PartialOperation.Scope != c.Scope {
		return nil, nil
	}
	u := &Unfinished{ID: state.PartialOperation.ID, Root: c.Scope.Root}
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
		missing := lacksOf(change, current)
		if missing.none() {
			continue
		}
		if missing.group {
			if err = settableGroup(c, u.ID, change.Path, *change.After.Group); err != nil {
				return nil, err
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
	return u, nil
}

// settableGroup refuses path, a file apply id left without group, the group
// its approved image holds, when this user cannot give it that group: only
// a member of a group may give a file it (chown(2)), and sudo may.
func settableGroup(c Context, id, path string, group uint32) error {
	permitted, err := canSetGroup(group)
	if err != nil || permitted {
		return err
	}
	return Fail(
		ExitPartial,
		"recovery",
		c.ShowPath(path)+" lacks its group "+groupName(group),
		Cause("apply "+id+" stopped after chezmoi wrote it, and you are not a member of group "+
			groupName(group)+", so Workbench cannot give it that group"),
		Changed("nothing"),
		FixPath(path, "sudo", "chgrp", groupName(group)),
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

// Input binds u to a plan: the operation, its checkpoint's journal as it
// stands, and each file's image now and once settled. A plan made before
// any of them changed, or before the record went, then no longer matches.
func (u *Unfinished) Input() Input {
	type target struct{ Path, Current, Settled string }
	bound := struct {
		ID, Root, Checkpoint string
		Revert, Gone         bool
		Sequence             uint64
		Targets              []target
	}{ID: u.ID, Root: u.Root, Revert: u.Revert, Gone: u.Gone}
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
// that file lacks.
func (u *Unfinished) Note(c Context) Warning {
	what := "Apply "
	if u.Revert {
		what = "Revert "
	}
	headline := what + u.ID + " to " + c.ShowPath(u.Root) + " did not finish"
	switch {
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
	case len(u.Targets) == 0:
		return Warn(
			headline,
			Outcome("each file it wrote is finished; the next apply records it as finished"),
		)
	}
	parts := []Part{
		Cause(
			"it stopped after chezmoi wrote files, before Workbench gave them what chezmoi leaves out",
		),
	}
	// One line for the files that lack the same, in the order the first of
	// them comes in.
	var lacks []string
	paths := map[string][]string{}
	for _, t := range u.Targets {
		what := display.List(t.Lacks)
		if paths[what] == nil {
			lacks = append(lacks, what)
		}
		paths[what] = append(paths[what], t.Path)
	}
	for _, what := range lacks {
		const shown = 3
		files := paths[what]
		names := make([]string, 0, shown+1)
		for _, path := range files[:min(len(files), shown)] {
			names = append(names, c.ShowPath(path))
		}
		if len(files) > shown {
			names = append(names, fmt.Sprintf("%d more", len(files)-shown))
		}
		verb := " lacks its "
		if len(files) > 1 {
			verb = " lack their "
		}
		parts = append(parts, Line{Label: "files", Text: display.List(names) + verb + what})
	}
	return Warn(headline, append(
		parts,
		Outcome("the next apply gives each file what it lacks before it writes anything, "+
			"and leaves its content as it is"),
	)...)
}

// lacking is what a target of an unfinished apply lacks of what its approved
// image holds and chezmoi's write leaves out (lacksOf).
type lacking struct {
	// group is set when the target has another group than the approved one,
	// and setgid when a folder lacks the set-group-ID bit.
	group, setgid bool
	// label is the label a file or folder that replaced one of its kind had
	// before, to put back (nativeLabel); newLabel is set for a file made
	// where none was that holds a label, which gets the label a file made in
	// its own folder gets (foreignLabel).
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

// names names what l lacks, as a person reads it.
func (l lacking) names() []string {
	var names []string
	for _, lack := range []struct {
		lacks bool
		name  string
	}{
		{l.group, "group"},
		{l.setgid, "set-group-ID bit"},
		{l.label != nil || l.newLabel, "SELinux label"},
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
