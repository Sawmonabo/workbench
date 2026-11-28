package platform

import (
	"bytes"
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openFolderAt is openat(2) of path in the folder dir with O_SEARCH, or
// O_RDONLY when that is refused with EACCES, and O_DIRECTORY and O_NOFOLLOW.
func openFolderAt(dir int, path string) (*os.File, error) {
	const flags = unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Openat(dir, path, unix.O_SEARCH|flags, 0)
	if errors.Is(err, unix.EACCES) {
		fd, err = unix.Openat(dir, path, unix.O_RDONLY|flags, 0)
	}
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

// chmodFolder is fchmod(2).
func chmodFolder(f *os.File, mode os.FileMode) error { return f.Chmod(mode) }

// chmodFolderAt is fstatat(2) and fchmodat(2), both with
// AT_SYMLINK_NOFOLLOW, then openFolderAt.
func chmodFolderAt(
	dir *os.File,
	name string,
	judged os.FileInfo,
	mode os.FileMode,
) (*os.File, error) {
	fd := int(dir.Fd())
	var stat unix.Stat_t
	if err := unix.Fstatat(fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	if !sameFolder(judged, &stat) {
		return nil, ErrFolderReplaced
	}
	if err := unix.Fchmodat(fd, name, ModeBits(mode), unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	return openFolderAt(fd, name)
}

// The kauth rights of sys/kauth.h besides those in file_darwin.go that let a
// principal change who may write in a folder: KAUTH_VNODE_WRITE_SECURITY and
// KAUTH_VNODE_TAKE_OWNERSHIP; and KAUTH_ACE_PERMIT, an allow entry's kind.
const (
	securityRights uint32 = 1<<12 | 1<<13
	acePermit      uint32 = 1
)

// othersMayWrite is othersAllowed with the rights that change who may write
// in a folder.
func othersMayWrite(fd int, path string) (int, error) {
	return othersAllowed(fd, path, childRights|securityRights|genericRights)
}

// othersMaySearch is othersAllowed with the right to search and the generic
// rights that hold it.
func othersMaySearch(fd int, path string) (int, error) {
	return othersAllowed(fd, path, searchRight|genericHolding(searchRight))
}

// othersAllowed reads the ACL as FileSecurity does and compares the
// applicable GUID of each allow entry with one of rights, the first 16 bytes
// of its kauth_ace, with the user's own from mbr_uid_to_uuid, which it asks
// for only when there is such an entry: the deny entries macOS puts on the
// home folder and the folders of ~/Library never need it.
func othersAllowed(fd int, path string, rights uint32) (int, error) {
	acl, err := fileSecurity(fd, path)
	if err != nil {
		return -1, err
	}
	var own *[16]byte
	for i, entry := range acl.Entries {
		if entry.Flags&aceKind != acePermit || entry.Rights&rights == 0 {
			continue
		}
		if own == nil {
			uuid, err := userUUID(uint32(os.Geteuid()))
			if err != nil {
				return -1, err
			}
			own = &uuid
		}
		if !bytes.Equal(acl.Data[filesecHeader+aceSize*i:][:16], own[:]) {
			return i, nil
		}
	}
	return -1, nil
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
