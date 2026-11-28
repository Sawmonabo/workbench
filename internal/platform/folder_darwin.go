package platform

import (
	"bytes"
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openFolder is open(2) with O_SEARCH, or O_RDONLY when that is refused
// with EACCES, and O_DIRECTORY and O_NOFOLLOW.
func openFolder(path string) (*os.File, error) {
	const flags = unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Open(path, unix.O_SEARCH|flags, 0)
	if errors.Is(err, unix.EACCES) {
		fd, err = unix.Open(path, unix.O_RDONLY|flags, 0)
	}
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

// chmodFolder is fchmod(2).
func chmodFolder(f *os.File, mode os.FileMode) error { return f.Chmod(mode) }

// The kauth rights of sys/kauth.h besides those in file_darwin.go that let a
// principal change who may write in a folder: KAUTH_VNODE_WRITE_SECURITY and
// KAUTH_VNODE_TAKE_OWNERSHIP; and KAUTH_ACE_PERMIT, an allow entry's kind.
const (
	securityRights uint32 = 1<<12 | 1<<13
	acePermit      uint32 = 1
)

// othersMayWrite reads the ACL as FileSecurity does and compares the
// applicable GUID of each allow entry with such rights, the first 16 bytes of
// its kauth_ace, with the user's own from mbr_uid_to_uuid, which it asks for
// only when there is such an entry: the deny entries macOS puts on the home
// folder and the folders of ~/Library never need it.
func othersMayWrite(fd int, path string) (bool, error) {
	acl, err := fileSecurity(fd, path)
	if err != nil {
		return false, err
	}
	var own *[16]byte
	for i, entry := range acl.Entries {
		if entry.Flags&aceKind != acePermit ||
			entry.Rights&(childRights|securityRights|genericRights) == 0 {
			continue
		}
		if own == nil {
			uuid, err := userUUID(uint32(os.Geteuid()))
			if err != nil {
				return false, err
			}
			own = &uuid
		}
		if !bytes.Equal(acl.Data[filesecHeader+aceSize*i:][:16], own[:]) {
			return true, nil
		}
	}
	return false, nil
}

// userUUID is mbr_uid_to_uuid(3) of uid, which returns 0 or the error
// itself rather than -1.
func userUUID(uid uint32) ([16]byte, error) {
	var uuid [16]byte
	r, _, _ := libcCall6(mbrUIDToUUID, uintptr(uid), uintptr(unsafe.Pointer(&uuid[0])), 0, 0, 0, 0)
	if r != 0 {
		return uuid, unix.Errno(r)
	}
	return uuid, nil
}
