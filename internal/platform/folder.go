package platform

import "os"

// OpenFolder opens the folder at path to read its status and ACL and to
// change its mode (see ChmodFolder), never following a link and needing no
// right on the folder itself that its owner may lack: on Linux with O_PATH,
// which needs none; on macOS, which has no such open, with O_SEARCH, or
// O_RDONLY for a folder its owner may read but not search. A link or a file
// at path fails with ENOTDIR or ELOOP, and on macOS a folder its owner may
// neither read nor search fails with EACCES. The error is the call's errno.
func OpenFolder(path string) (*os.File, error) { return openFolder(path) }

// ChmodFolder gives the folder f, opened by OpenFolder, mode: its permission
// bits and setuid, setgid and sticky bits. On macOS fchmod(2) changes it
// through f. On Linux, where fchmod(2) refuses an O_PATH descriptor, it is
// fchmodat2(2) with AT_EMPTY_PATH, which Linux 6.6 added, or before that a
// chmod of /proc/self/fd/N, the link to the folder f holds, as glibc changes
// a file it has only an O_PATH descriptor for. Either changes the folder f
// holds, whatever is at its path by then. The error is the call's errno.
func ChmodFolder(f *os.File, mode os.FileMode) error { return chmodFolder(f, mode) }

// OthersMayWrite is the index, as FileSecurity lists it (and ACLEntryRemoval
// takes it), of the first entry of the ACL of the folder fd, or of path
// itself, not a link's target, when path is not empty, that allows a user or
// group other than this process's user to add or remove entries in it, or to
// change its ACL or owner, by which one could give itself those rights, and
// -1 when there is none: on macOS a KAUTH_ACE_PERMIT entry with
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
