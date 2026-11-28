package operation

import (
	"bytes"
	"errors"
	"maps"
	"os"
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
	"golang.org/x/sys/unix"
)

// settleTarget gives t, a file or folder of cp's unfinished apply, what it
// lacks, as its settled image says, on the file itself through a descriptor,
// and leaves its content and its other metadata alone: its group, then a
// folder's set-group-ID bit as its mode, read after the group, holds it or
// not (setImageMode), then the extended attributes it lacks, then
// the hidden flag and the ACL (writeImageSecurity), each only where it
// differs. A new file, which replaces none, gets the label a file made in its
// own folder gets (giveFolderLabel). A link, and a file that lacks an extended
// attribute its owner may not write, are written anew (settleAnew). It
// reports whether it changed anything: a new file that lacks nothing but may
// lack that label holds it already when the two are the same.
func (cp *Checkpoint) settleTarget(t UnfinishedTarget) (bool, error) {
	c := cp.mutation.context
	if writtenAnew(t) {
		return len(t.Lacks) > 0 || t.newLabel, cp.settleAnew(c, t)
	}
	parent, name, err := imageParent(c, t.Path)
	if err != nil {
		return false, err
	}
	defer func() { _ = parent.Close() }()
	fd, err := unix.Openat(
		int(parent.Fd()),
		name,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC,
		0,
	)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
		return false, settleChanged(c, t.Path)
	}
	if err != nil {
		return false, err
	}
	file := os.NewFile(uintptr(fd), t.Path)
	defer func() { _ = file.Close() }()
	if err = holdsCurrent(c, fd, int(parent.Fd()), name, t); err != nil {
		return false, err
	}
	current, settled := t.Current, t.Settled
	if *current.Group != *settled.Group {
		if err = setImageGroup(fd, *settled.Group); err != nil {
			return false, carryFailure(
				t.Path,
				changedFolder,
				false,
				"group "+groupName(*settled.Group),
				err,
			)
		}
	}
	if err = setImageMode(fd, settled.Mode); err != nil {
		return false, carryFailure(t.Path, changedFolder, false, "the set-group-ID bit", err)
	}
	if err = writeImageAttributes(onFD(fd), t.Path, settled.Attributes, changedFolder); err != nil {
		return false, err
	}
	if err = writeImageSecurity(onFD(fd), t.Path, current, settled, changedFolder); err != nil {
		return false, err
	}
	changed := len(t.Lacks) > 0
	if t.newLabel {
		labeled, labelErr := giveFolderLabel(fd, int(parent.Fd()), t.Path, cp.stagedName(t.index))
		if labelErr != nil {
			return false, labelErr
		}
		changed = changed || labeled
	}
	if err = file.Sync(); err != nil {
		return false, err
	}
	return changed, settledAs(c, t)
}

// holdsCurrent refuses t when fd, opened as name in the folder parent, is not
// the file at its path, or that file does not hold what the plan found
// there: someone changed it after the plan was made.
func holdsCurrent(c Context, fd, parent int, name string, t UnfinishedTarget) error {
	var opened, now unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil {
		return err
	}
	current, err := ReadImage(c, t.Path)
	if err != nil {
		return err
	}
	if err = unix.Fstatat(parent, name, &now, unix.AT_SYMLINK_NOFOLLOW); err != nil &&
		!errors.Is(err, unix.ENOENT) {
		return err
	}
	if err != nil || opened.Ino != now.Ino || opened.Dev != now.Dev ||
		!sameImage(current, t.Current) {
		return settleChanged(c, t.Path)
	}
	return nil
}

// writtenAnew reports whether t is settled by writing it anew (settleAnew):
// a link, and a file that lacks an extended attribute while its owner may not
// write it. Writing a user.* attribute on Linux (xattr_permission in
// fs/xattr.c) and any attribute on macOS takes the owner's write permission,
// which a file of mode 0444 does not give; a write in place would have to
// give it for a moment, and a stop then would leave the file writable.
func writtenAnew(t UnfinishedTarget) bool {
	return t.Current.Kind == ImageSymlink || t.Current.Kind == ImageFile &&
		t.Current.Mode&0o200 == 0 && !sameAttributes(t.Current.Attributes, t.Settled.Attributes)
}

// settleAnew writes t anew with its settled image, which holds its content
// and its other metadata as they are, the way every apply writes a file or
// link (rewriteImage): staged under the name its apply stages it under, its
// attributes written before its mode, then renamed over it once it still
// holds what the plan found. A new file, which replaces none, keeps the
// label the staged file gets in its folder.
func (cp *Checkpoint) settleAnew(c Context, t UnfinishedTarget) error {
	desired := t.Settled
	if t.newLabel {
		desired.Attributes = maps.Clone(desired.Attributes)
		delete(desired.Attributes, platform.LabelAttribute)
	}
	if err := rewriteImage(c, t.Path, t.Current, desired, cp.stagedName(t.index)); err != nil {
		return err
	}
	return settledAs(c, t)
}

// giveFolderLabel gives fd, the file at path, the SELinux label a file made in
// its folder, opened as folder, gets, where it holds another, and reports
// whether it did: it makes an empty file there at temporary, the name its
// apply stages that file under, reads that file's label with the reader of
// every image (readImageAttributes) and removes it again. Where the new file
// gets none, SELinux is off and the file keeps its own (carryLabel).
func giveFolderLabel(fd, folder int, path, temporary string) (bool, error) {
	made, err := unix.Openat(
		folder,
		temporary,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		return false, labelFailure(path, err)
	}
	gets, err := readImageAttributes(
		onFD(made),
		filepath.Join(filepath.Dir(path), temporary),
		changedFolder,
	)
	_ = unix.Close(made)
	if removeErr := unix.Unlinkat(folder, temporary, 0); removeErr != nil {
		return false, labelFailure(path, removeErr)
	}
	if err != nil {
		return false, labelFailure(path, err)
	}
	label, labeled := gets[platform.LabelAttribute]
	if !labeled {
		return false, nil
	}
	attributes, err := readImageAttributes(onFD(fd), path, changedFolder)
	if err != nil {
		return false, err
	}
	if bytes.Equal(attributes[platform.LabelAttribute], label) {
		return false, nil
	}
	if err = carryLabel(onFD(fd), attributes, label); err != nil {
		return false, carryFailure(path, changedFolder, false, "its SELinux label", err)
	}
	return true, nil
}

// labelFailure is the failure of learning, with err, the label a file made
// in path's folder gets.
func labelFailure(path string, err error) error {
	return Fail(
		ExitBlocked,
		"metadata",
		"Cannot tell which SELinux label a file made in "+display.Path(filepath.Dir(path))+" gets",
		Cause(err.Error()),
		Outcome(display.Path(path)+" keeps the label it has"),
	)
}

// settledAs fails unless the file at t's path holds what settling gave it.
func settledAs(c Context, t UnfinishedTarget) error {
	written, err := ReadImage(c, t.Path)
	if err != nil {
		return err
	}
	if !sameImage(written, t.Settled) {
		return Fail(
			ExitPartial,
			"recovery",
			display.Path(t.Path)+" does not hold what Workbench gave it",
			Changed("its "+display.List(t.given())),
		)
	}
	return nil
}

// settleChanged is the refusal of settling a file of an unfinished apply at
// path that changed after the plan was made.
func settleChanged(c Context, path string) error {
	return Fail(
		ExitConflict,
		"conflict",
		display.Path(path)+" changed after the plan was made",
		FixText(c.ApplyCommand(c.Scope.Root)),
	)
}
