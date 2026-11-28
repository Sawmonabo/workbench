package operation

import (
	"io"
	"os"
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
	"golang.org/x/sys/unix"
)

// enforceNativeMetadata gives a target native wrote the approved group and,
// unless label is nil, that label (carryLabel). It uses the opened inode for
// both the exact-image proof and the change, so replacing a path cannot
// redirect the approved metadata write.
func enforceNativeMetadata(
	c Context,
	path string,
	observed Image,
	group uint32,
	label []byte,
) error {
	if observed.Kind != ImageFile && observed.Kind != ImageDirectory {
		return Fail(
			ExitBlocked,
			"metadata",
			display.Path(path)+" is "+imageKindName(observed.Kind)+
				", and Workbench gives native's output its group only on a file or folder",
		)
	}
	parent, name, err := imageParent(c, path)
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
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if int(stat.Uid) != os.Geteuid() || stat.Mode&0o7000 != 0 ||
		(observed.Kind == ImageFile && (stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1)) ||
		(observed.Kind == ImageDirectory && stat.Mode&unix.S_IFMT != unix.S_IFDIR) {
		return Fail(
			ExitPartial,
			"native_image",
			display.Path(path)+" changed after native wrote it, before Workbench gave it its group",
		)
	}
	current := Image{Kind: observed.Kind, Mode: uint32(stat.Mode) & 0o777}
	currentGroup := uint32(stat.Gid)
	current.Group = &currentGroup
	if err = c.openMetadata(fd, path, observed.Kind == ImageDirectory); err != nil {
		return err
	}
	current.Attributes, err = readImageAttributes(
		fd,
		path,
		holderOf(observed.Kind == ImageDirectory),
	)
	if err != nil {
		return err
	}
	if current.Kind == ImageFile {
		current.Data, err = io.ReadAll(io.LimitReader(file, MaxImageBytes+1))
		if err != nil {
			return err
		}
	}
	if !sameImage(current, observed) {
		shown := display.Path(path)
		return Fail(
			ExitPartial,
			"native_image",
			shown+" differs from what native wrote, so Workbench did not change its group",
		)
	}
	if err = setImageGroup(fd, group); err != nil {
		return err
	}
	if label != nil {
		if err = carryLabel(fd, path, current.Attributes, label); err != nil {
			return err
		}
	}
	return file.Sync()
}

// writeDirectoryImage creates name when absent, then sets the desired
// attributes, group and mode through a descriptor. It never replaces a
// non-directory.
func writeDirectoryImage(fd int, name, path string, current, desired Image) error {
	if current.Kind != ImageAbsent && current.Kind != ImageDirectory {
		return Fail(
			ExitBlocked,
			"image",
			display.Path(path)+" is "+imageKindName(current.Kind)+
				", and Workbench does not replace it with a folder; move it aside and retry",
		)
	}
	if current.Kind == ImageAbsent {
		if err := unix.Mkdirat(fd, name, desired.Mode); err != nil {
			return err
		}
	}
	directoryFD, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(directoryFD) }()
	if err = writeImageAttributes(
		directoryFD,
		path,
		desired.Attributes,
		changedFolder,
	); err != nil {
		return err
	}
	if err = setImageGroup(directoryFD, *desired.Group); err != nil {
		return err
	}
	return unix.Fchmod(directoryFD, desired.Mode)
}

// stageLinkImage creates the desired symlink at temporary beside path, with
// its group and attributes, ready to replace the target.
func stageLinkImage(fd int, temporary, path string, desired Image) error {
	if err := unix.Symlinkat(desired.Link, fd, temporary); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(fd, temporary, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if uint32(stat.Gid) != *desired.Group {
		group := int(*desired.Group)
		if err := unix.Fchownat(fd, temporary, -1, group, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
	}
	link := filepath.Join(filepath.Dir(path), temporary)
	return writeImageAttributes(-1, link, desired.Attributes, replacedFile)
}

// stageFileImage writes the desired bytes, group, mode and attributes to a new
// private file at temporary and syncs it, ready to replace the target.
func stageFileImage(fd int, temporary, path string, desired Image) error {
	flags := unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fileFD, err := unix.Openat(fd, temporary, flags, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fileFD), temporary)
	_, err = file.Write(desired.Data)
	if err == nil {
		err = setImageGroup(fileFD, *desired.Group)
	}
	if err == nil {
		err = file.Chmod(os.FileMode(desired.Mode))
	}
	if err == nil {
		err = writeImageAttributes(fileFD, path, desired.Attributes, replacedFile)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func writeImage(c Context, path string, expected, desired Image) error {
	if err := validateImageGroup(c, path, desired); err != nil {
		return err
	}
	current, err := ReadImage(c, path)
	if err != nil {
		return err
	}
	if !sameImage(current, expected) {
		return Fail(
			ExitConflict,
			"conflict",
			display.Path(path)+" changed after the plan was made,"+
				" so Workbench kept those edits and did not write it",
		)
	}
	if sameImage(current, desired) {
		return nil
	}
	parent, name, err := imageParent(c, path)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	fd := int(parent.Fd())
	if desired.Kind == ImageAbsent {
		flags := 0
		if current.Kind == ImageDirectory {
			flags = unix.AT_REMOVEDIR
		}
		if err = unix.Unlinkat(fd, name, flags); err != nil {
			return err
		}
		return syncDirectory(parent)
	}
	if desired.Kind == ImageDirectory {
		if err = writeDirectoryImage(fd, name, path, current, desired); err != nil {
			return err
		}
		return syncDirectory(parent)
	}
	if current.Kind == ImageDirectory {
		return Fail(
			ExitBlocked,
			"image",
			display.Path(path)+" is a folder, and Workbench does not replace a folder with "+
				imageKindName(desired.Kind)+"; move it aside and retry",
		)
	}
	id, err := NewID()
	if err != nil {
		return err
	}
	temporary := ".workbench-" + id
	defer func() { _ = unix.Unlinkat(fd, temporary, 0) }()
	stage := stageFileImage
	if desired.Kind == ImageSymlink {
		stage = stageLinkImage
	}
	if err = stage(fd, temporary, path, desired); err != nil {
		return err
	}
	// A final check narrows races with non-Workbench writers; conflicts never
	// trigger a rollback that could destroy their newly written data.
	current, err = ReadImage(c, path)
	if err != nil {
		return err
	}
	if !sameImage(current, expected) {
		return Fail(
			ExitConflict,
			"conflict",
			display.Path(path)+" changed while Workbench prepared its replacement,"+
				" so Workbench kept those edits and did not write it",
		)
	}
	if err = platform.RenameAt(fd, temporary, name, expected.Kind == ImageAbsent); err != nil {
		return err
	}
	return syncDirectory(parent)
}
