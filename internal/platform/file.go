package platform

import (
	"bytes"
	"errors"
	"io/fs"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ACE is one access control entry of a file's ACL, as FileSecurity reads it:
// on macOS a kauth_ace (sys/kauth.h) after its 16-byte applicable GUID, its
// kind and inheritance flags and the rights it allows or denies.
type ACE struct{ Flags, Rights uint32 }

// ErrUnsupportedACL is FileSecurity's error for security data whose layout
// it does not recognize, which no caller may read as "no ACL".
var ErrUnsupportedACL = errors.New("unsupported ACL layout")

// NativeFilesystem reports whether path, or its first existing ancestor, is
// on a file system native to this system: APFS or HFS on macOS; on Linux
// ext, XFS, Btrfs, tmpfs or overlay, including under WSL, and not Windows,
// 9P, FUSE or network mounts. The error is a statfs failure.
func NativeFilesystem(path string) (bool, error) { return nativeFilesystem(path) }

// RenameNoReplace renames from to to in one system call, which fails,
// renaming nothing, when a file is at to. The error is the call's errno.
func RenameNoReplace(from, to string) error { return renameNoReplace(from, to) }

// RenameAt renames from to to, both in the folder fd, and with exclusive
// fails, renaming nothing, when to exists. The error is the call's errno.
func RenameAt(fd int, from, to string, exclusive bool) error {
	return renameAt(fd, from, to, exclusive)
}

// FileSecurity returns the ACL entries of fd, or of path itself, not a
// link's target, when path is not empty, and whether an ACL is present at
// all. An error is the call's errno, or ErrUnsupportedACL for security data
// whose layout it does not recognize, so a caller fails closed. On Linux it
// always reports no ACL and no error: POSIX ACLs there are extended
// attributes, which ExtendedAttributeCount counts.
func FileSecurity(fd int, path string) (entries []ACE, present bool, err error) {
	return fileSecurity(fd, path)
}

// existingStatfs is statfs of path or, while path does not exist yet, of its
// first existing ancestor, the filesystem a new file at path would be on.
func existingStatfs(path string) (unix.Statfs_t, error) {
	var stat unix.Statfs_t
	for {
		err := unix.Statfs(path, &stat)
		if err == nil || !errors.Is(err, fs.ErrNotExist) || path == filepath.Dir(path) {
			return stat, err
		}
		path = filepath.Dir(path)
	}
}

// ExtendedAttributeCount is the number of extended attribute names of fd,
// or of path itself, not a link's target, when fd is negative. A call
// without a buffer only bounds the name list's length: on overlayfs it also
// counts the trusted.overlay.* names the read leaves out, so the names are
// read into a buffer that size and counted. A list that grew in between
// fails with ERANGE.
func ExtendedAttributeCount(fd int, path string) (int, error) {
	list := func(names []byte) (int, error) {
		if fd < 0 {
			return unix.Llistxattr(path, names)
		}
		return unix.Flistxattr(fd, names)
	}
	size, err := list(nil)
	if err != nil || size == 0 {
		return 0, err
	}
	names := make([]byte, size)
	size, err = list(names)
	if err != nil {
		return 0, err
	}
	return bytes.Count(names[:size], []byte{0}), nil
}
