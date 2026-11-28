package platform

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// OpenFolder opens the folder at path to read its status and ACL and to
// change its mode (see ChmodFolder), never following a link and needing no
// right on the folder itself that its owner may lack: on Linux with O_PATH,
// which needs none; on macOS, which has no such open, with O_SEARCH, or
// O_RDONLY for a folder its owner may read but not search. A link or a file
// at path fails with ENOTDIR or ELOOP, and on macOS a folder its owner may
// neither read nor search fails with EACCES: ChmodFolderAt changes that one
// by its name. The error is the call's errno.
func OpenFolder(path string) (*os.File, error) { return openFolderAt(unix.AT_FDCWD, path) }

// ChmodFolder gives the folder f, opened by OpenFolder, mode: its permission
// bits and setuid, setgid and sticky bits. On macOS fchmod(2) changes it
// through f. On Linux, where fchmod(2) refuses an O_PATH descriptor, it is
// fchmodat2(2) with AT_EMPTY_PATH, which Linux 6.6 added, or before that a
// chmod of /proc/self/fd/N, the link to the folder f holds, as glibc changes
// a file it has only an O_PATH descriptor for. Either changes the folder f
// holds, whatever is at its path by then. The error is the call's errno.
func ChmodFolder(f *os.File, mode os.FileMode) error { return chmodFolder(f, mode) }

// ErrFolderReplaced is ChmodFolderAt's error, with nothing changed, when the
// entry it is to change is not the folder its caller judged.
var ErrFolderReplaced = errors.New("replaced while it was checked")

// ChmodFolderAt gives the folder name in the folder dir, opened by
// OpenFolder, mode, as ChmodFolder does, for a folder OpenFolder opens no
// descriptor for, and returns a descriptor of it opened as OpenFolder opens
// one. It never follows a link, and changes nothing unless name in dir is the
// folder judged describes (os.SameFile), as fstatat(2) with
// AT_SYMLINK_NOFOLLOW reads it there; otherwise it fails with
// ErrFolderReplaced. On macOS fchmodat(2) with AT_SYMLINK_NOFOLLOW then
// changes it by name, and openat(2) opens it, once its owner may search
// it. Only a process that may write in dir can put another entry at name
// between the two calls, and fchmodat changes only what this user owns (or
// a link's own mode), so the caller compares the descriptor with judged to
// tell that the folder it judged is the one that changed. On Linux, where
// OpenFolder opens every folder, openat(2) with O_PATH opens it after the
// fstatat and ChmodFolder changes it through that descriptor. The error is
// the call's errno or ErrFolderReplaced.
func ChmodFolderAt(
	dir *os.File,
	name string,
	judged os.FileInfo,
	mode os.FileMode,
) (*os.File, error) {
	return chmodFolderAt(dir, name, judged, mode)
}

// OthersMayWrite is the index, as FileSecurity lists it (and ACLEntryRemoval
// takes it), of the first entry of the ACL of the folder fd, or of path
// itself, not a link's target, when path is not empty (a relative path in
// the folder fd, or in the working directory when fd is -1), that allows a
// user or group other than this process's user to add or remove entries in
// it, or to change its ACL or owner, by which one could give itself those
// rights, and -1 when there is none: on macOS a KAUTH_ACE_PERMIT entry with
// KAUTH_VNODE_ADD_FILE, ADD_SUBDIRECTORY, DELETE_CHILD, WRITE_SECURITY,
// TAKE_OWNERSHIP, KAUTH_ACE_GENERIC_ALL or KAUTH_ACE_GENERIC_WRITE whose
// applicable GUID is not the user's own, as mbr_uid_to_uuid(3) gives it (a
// group's, everyone's or another user's), whether the entry applies to the
// folder or only to what is made in it. Entries that only allow reading, and
// deny entries, do not count. On Linux it is always -1: a POSIX ACL's
// named entries take effect only within its mask, which stat shows as the
// group's permission bits (acl(5)), so the mode already says whether anyone
// but the owner may write in the folder. The error is the call's errno, or
// ErrUnsupportedACL for an ACL whose layout FileSecurity does not recognize.
func OthersMayWrite(fd int, path string) (entry int, err error) { return othersMayWrite(fd, path) }

// OthersMaySearch is [OthersMayWrite] for the right to search a folder: the
// index of the first entry of the ACL of the folder fd, or of path itself,
// that allows a user or group other than this process's user to search it,
// and -1 when there is none: on macOS a KAUTH_ACE_PERMIT entry with
// KAUTH_VNODE_SEARCH, KAUTH_ACE_GENERIC_EXECUTE or KAUTH_ACE_GENERIC_ALL
// whose applicable GUID is not the user's own. On Linux it is always -1: the
// mode's group bits hold the ACL's mask, so they say whether a named entry
// lets anyone but the owner search it.
func OthersMaySearch(fd int, path string) (entry int, err error) {
	return othersMaySearch(fd, path)
}

// sameFolder reports whether stat, as fstatat(2) read it, is of the folder
// judged describes: a folder with its device and inode.
func sameFolder(judged os.FileInfo, stat *unix.Stat_t) bool {
	sys, ok := judged.Sys().(*syscall.Stat_t)
	return ok && judged.IsDir() && stat.Mode&unix.S_IFMT == unix.S_IFDIR &&
		sys.Dev == stat.Dev && sys.Ino == stat.Ino
}

// ModeBits is mode as chmod(2) takes it, and as chmod and ls show it: its
// permission bits with S_ISUID, S_ISGID and S_ISVTX for its setuid, setgid
// and sticky bits, so a sticky folder any user can write in is 01777.
func ModeBits(mode os.FileMode) uint32 {
	bits := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		bits |= unix.S_ISUID
	}
	if mode&os.ModeSetgid != 0 {
		bits |= unix.S_ISGID
	}
	if mode&os.ModeSticky != 0 {
		bits |= unix.S_ISVTX
	}
	return bits
}
