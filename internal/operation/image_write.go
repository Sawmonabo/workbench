package operation

import (
	"io"
	"os"
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
	"golang.org/x/sys/unix"
)

// enforceNativeMetadata gives a target native wrote the approved group, a
// folder the approved mode (its set-group-ID bit, see nativeImage), and,
// unless label is nil, that label (carryLabel). It uses the opened inode for
// both the exact-image proof and the change, so replacing a path cannot
// redirect the approved metadata write.
func enforceNativeMetadata(
	c Context,
	path string,
	observed Image,
	group, mode uint32,
	label []byte,
) error {
	if observed.Kind != ImageFile && observed.Kind != ImageDirectory {
		return Fail(
			ExitBlocked,
			"metadata",
			display.Path(path)+" is "+imageKindName(observed.Kind)+
				", and Workbench gives what chezmoi wrote its group only on a file or folder",
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
	if int(stat.Uid) != os.Geteuid() || specialBits(uint32(stat.Mode)) != 0 ||
		(observed.Kind == ImageFile && (stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1)) ||
		(observed.Kind == ImageDirectory && stat.Mode&unix.S_IFMT != unix.S_IFDIR) {
		shown := display.Path(path)
		return Fail(
			ExitPartial,
			"native_image",
			shown+" changed after chezmoi wrote it, before Workbench gave it its group",
		)
	}
	current := Image{Kind: observed.Kind, Mode: imageMode(uint32(stat.Mode))}
	currentGroup := uint32(stat.Gid)
	current.Group = &currentGroup
	held, err := c.openMetadata(fd, path, observed.Kind == ImageDirectory)
	if err != nil {
		return err
	}
	current.ACL, current.Hidden = held.acl, held.hidden
	current.Attributes, err = readImageAttributes(
		fd,
		path,
		folderOrFile(observed.Kind == ImageDirectory),
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
			shown+" differs from what chezmoi wrote, so Workbench did not change its group",
		)
	}
	if err = setImageGroup(fd, group); err != nil {
		return err
	}
	if observed.Kind == ImageDirectory && current.Mode != mode {
		if err = unix.Fchmod(fd, mode); err != nil {
			return err
		}
	}
	if label != nil {
		if err = carryLabel(fd, path, current.Attributes, label); err != nil {
			return err
		}
	}
	return file.Sync()
}

// writeDirectoryImage creates name when absent, then sets the desired
// attributes, group, mode, hidden flag and ACL through a descriptor, each
// that differs from current, the image it holds. It never replaces a
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
		"",
		path,
		desired.Attributes,
		changedFolder,
	); err != nil {
		return err
	}
	if err = setImageGroup(directoryFD, *desired.Group); err != nil {
		return err
	}
	if err = unix.Fchmod(directoryFD, desired.Mode); err != nil {
		return err
	}
	return writeImageSecurity(directoryFD, "", path, current, desired, changedFolder)
}

// stageLinkImage creates the desired symlink at temporary beside path, with
// its group, attributes, mode, hidden flag and ACL, ready to replace the
// target. Each is set on the link itself, never on the file it points to.
// The mode follows the attributes: on macOS writing a link's attributes needs
// its owner's write permission, which the desired mode may not give.
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
	if err := writeImageAttributes(-1, link, path, desired.Attributes, replacedLink); err != nil {
		return err
	}
	if uint32(stat.Mode)&0o777 != desired.Mode {
		if err := platform.SetLinkMode(fd, temporary, desired.Mode); err != nil {
			return err
		}
	}
	return writeImageSecurity(-1, link, path, Image{}, desired, replacedLink)
}

// stageFileImage writes the desired bytes, group, attributes, mode, hidden
// flag and ACL to a new private file at temporary and syncs it, ready to
// replace the target. The attributes come before the mode: writing them needs
// write permission, on Linux for a user.* attribute (xattr_permission in
// fs/xattr.c) and on macOS for any, which the desired mode may not give; and
// on Linux setting an ACL's attribute sets the mode from the ACL
// (posix_acl_update_mode), so the mode set after it is the image's, with the
// ACL's mask rewritten from it as chmod rewrites it (posix_acl_chmod). A new
// file holds no ACL and no flag: the folder rules refuse a folder whose ACL
// passes to the files made in it.
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
		err = writeImageAttributes(fileFD, "", path, desired.Attributes, replacedFile)
	}
	if err == nil {
		err = file.Chmod(os.FileMode(desired.Mode))
	}
	if err == nil {
		err = writeImageSecurity(fileFD, "", path, Image{}, desired, replacedFile)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// carryFailure is the failure of giving what to the file or link Workbench
// writes in place of path, or the folder path itself, as h says, or of taking
// it off when remove, which the system refused with err. It names the
// metadata, never its value.
func carryFailure(path string, h holder, remove bool, what string, err error) error {
	shown := display.Path(path)
	subject := shown
	switch h {
	case replacedFile:
		subject = "the file Workbench writes in place of " + shown
	case replacedLink:
		subject = "the link Workbench writes in place of " + shown
	case changedFolder, parentFolder:
	}
	message := "Cannot give " + subject + " " + what + ": " + err.Error()
	if remove {
		message = "Cannot take " + what + " off " + subject + ": " + err.Error()
	}
	if h.replaced() {
		message += ", so Workbench left " + shown + " as it is"
	}
	return Fail(ExitBlocked, "metadata", message)
}

// writeImage gives path, which must hold expected, the image desired, and
// leaves a path that already holds desired as it is. A file or link is staged
// at temporary in path's folder, then renamed over path.
func writeImage(c Context, path string, expected, desired Image, temporary string) error {
	return putImage(c, path, expected, desired, false, temporary)
}

// rewriteImage is writeImage for a file or link that is to be written anew
// even when it holds desired already, as image comparisons see it: they leave
// the label out, which a new file made in path's own folder gets from there.
func rewriteImage(c Context, path string, expected, desired Image, temporary string) error {
	return putImage(c, path, expected, desired, true, temporary)
}

// putImage is writeImage, and rewriteImage for a file or link when anew is
// set.
func putImage(c Context, path string, expected, desired Image, anew bool, temporary string) error {
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
	fresh := anew && (desired.Kind == ImageFile || desired.Kind == ImageSymlink)
	if sameImage(current, desired) && !fresh {
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
