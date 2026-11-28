package operation

import (
	"bytes"
	"cmp"
	"errors"
	"os"
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
	"golang.org/x/sys/unix"
)

// settleTarget gives t, a file or folder of cp's unfinished apply, what it
// lacks, as its settled image says, on the file itself through a descriptor,
// and leaves its content and its other metadata alone: its group, then a
// folder's set-group-ID bit, then the extended attributes it lacks, then
// the hidden flag and the ACL (writeImageSecurity), each only where it
// differs. Writing a user.* attribute on Linux (xattr_permission in
// fs/xattr.c) and any attribute on macOS takes the owner's write permission,
// which a file of mode 0444 does not give: its owner gets it for those
// writes, then the file's mode again, which a stop in between leaves as the
// owner could set it, the file's content and the rest unchanged. A file made
// where none was gets the label a file made in its own folder gets
// (newFileLabel). A link is given what it lacks by its path (settleLink).
func (cp *Checkpoint) settleTarget(t UnfinishedTarget) error {
	c := cp.mutation.context
	if t.Current.Kind == ImageSymlink {
		return settleLink(c, t)
	}
	parent, name, err := imageParent(c, t.Path)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	fd, err := unix.Openat(
		int(parent.Fd()),
		name,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC,
		0,
	)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
		return settleChanged(c, t.Path)
	}
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), t.Path)
	defer func() { _ = file.Close() }()
	if err = holdsCurrent(c, fd, int(parent.Fd()), name, t); err != nil {
		return err
	}
	current, settled := t.Current, t.Settled
	if *current.Group != *settled.Group {
		if err = setImageGroup(fd, *settled.Group); err != nil {
			return carryFailure(
				t.Path,
				changedFolder,
				false,
				"group "+groupName(*settled.Group),
				err,
			)
		}
	}
	if current.Mode != settled.Mode {
		if err = unix.Fchmod(fd, settled.Mode); err != nil {
			return carryFailure(t.Path, changedFolder, false, "the set-group-ID bit", err)
		}
	}
	label := platform.LabelAttribute
	if !sameAttributes(current.Attributes, settled.Attributes) ||
		!bytes.Equal(current.Attributes[label], settled.Attributes[label]) {
		writable := current.Mode&0o200 != 0
		if !writable {
			if err = unix.Fchmod(fd, settled.Mode|0o200); err != nil {
				return carryFailure(
					t.Path,
					changedFolder,
					false,
					"its owner's write permission",
					err,
				)
			}
		}
		err = writeImageAttributes(fd, "", t.Path, settled.Attributes, changedFolder)
		if !writable {
			err = cmp.Or(err, unix.Fchmod(fd, settled.Mode))
		}
		if err != nil {
			return err
		}
	}
	if err = writeImageSecurity(fd, "", t.Path, current, settled, changedFolder); err != nil {
		return err
	}
	if t.newLabel {
		if err = giveFolderLabel(fd, int(parent.Fd()), t.Path, cp.stagedName(t.index)); err != nil {
			return err
		}
	}
	if err = file.Sync(); err != nil {
		return err
	}
	return settledAs(c, t)
}

// holdsCurrent refuses t when fd, opened as name in the folder parent, is no
// longer the file at its path, or that file no longer holds what the plan
// found there: someone changed it after the plan was made.
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

// settleLink gives t, a link of an unfinished apply, the extended attributes,
// hidden flag and ACL it lacks, each on the link itself by its path, as
// Workbench gives a link it writes them: Linux opens no descriptor of a link
// to set them through.
func settleLink(c Context, t UnfinishedTarget) error {
	current, err := ReadImage(c, t.Path)
	if err != nil {
		return err
	}
	if !sameImage(current, t.Current) {
		return settleChanged(c, t.Path)
	}
	if err = writeImageAttributes(
		-1,
		t.Path,
		t.Path,
		t.Settled.Attributes,
		changedFolder,
	); err != nil {
		return err
	}
	if err = writeImageSecurity(-1, t.Path, t.Path, current, t.Settled, changedFolder); err != nil {
		return err
	}
	return settledAs(c, t)
}

// giveFolderLabel gives fd, the file at path, the SELinux label a file made in
// its folder, opened as folder, gets: it makes an empty file there at
// temporary, the name its apply stages that file under, reads that file's
// label and removes it again. Where the new file gets none, SELinux is off
// and the file keeps its own (carryLabel).
func giveFolderLabel(fd, folder int, path, temporary string) error {
	made, err := unix.Openat(
		folder,
		temporary,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		return labelFailure(path, err)
	}
	var data [4096]byte
	n, err := unix.Fgetxattr(made, platform.LabelAttribute, data[:])
	_ = unix.Close(made)
	if removeErr := unix.Unlinkat(folder, temporary, 0); removeErr != nil {
		return labelFailure(path, removeErr)
	}
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.EOPNOTSUPP) {
		return nil
	}
	if err != nil {
		return labelFailure(path, err)
	}
	attributes, err := readImageAttributes(fd, path, changedFolder)
	if err != nil {
		return err
	}
	if err = carryLabel(fd, path, attributes, data[:n]); err != nil {
		return carryFailure(path, changedFolder, false, "its SELinux label", err)
	}
	return nil
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
			Changed("its "+display.List(t.Lacks)),
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
