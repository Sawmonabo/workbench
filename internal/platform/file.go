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

// IsTerminal reports whether fd is a terminal: whether its terminal settings
// can be read.
func IsTerminal(fd uintptr) bool {
	_, err := unix.IoctlGetTermios(int(fd), termiosRequest)
	return err == nil
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
