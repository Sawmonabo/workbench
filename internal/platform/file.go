package platform

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ACE is one access control entry of a file's ACL, as FileSecurity reads it:
// on macOS a kauth_ace (sys/kauth.h) after its 16-byte applicable GUID, its
// kind and inheritance flags and the rights it allows or denies.
type ACE struct{ Flags, Rights uint32 }

// ACL is a file's access control list as FileSecurity reads it. Data is the
// list as the system keeps it, which SetFileSecurity gives another file
// exactly, or nil when the file has none; a list without entries is not nil.
// On macOS it is the kauth_filesec getattrlist returns, whose owner and group
// GUIDs are null and which setattrlist reads only for its ACL
// (vfs_attrlist.c), so writing it changes no owner. Entries are its access
// control entries, in Data's order.
type ACL struct {
	Data    []byte
	Entries []ACE
}

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

// FileSecurity returns the ACL of fd, or of path itself, not a link's
// target, when path is not empty. An error is the call's errno, or
// ErrUnsupportedACL for security data whose layout it does not recognize, so
// a caller fails closed. On Linux it always reports no ACL and no error:
// POSIX ACLs there are extended attributes (ACLsAreAttributes), which
// ExtendedAttributeNames lists.
func FileSecurity(fd int, path string) (ACL, error) { return fileSecurity(fd, path) }

// The rights Allowed and ACE.Denies take. DeleteRight is the right to delete
// the file or folder itself, which renaming another file over it, renaming it
// away and removing it each need. ChildRights are the rights to add files and
// folders to a folder and to remove them from it, and RemoveChildRight the
// last of them alone. On macOS they are the kauth rights KAUTH_VNODE_DELETE,
// KAUTH_VNODE_ADD_FILE, ADD_SUBDIRECTORY and DELETE_CHILD (sys/kauth.h); on
// Linux, where FileSecurity lists no entries, they are 0.
const (
	DeleteRight      = deleteRight
	ChildRights      = childRights
	RemoveChildRight = removeChildRight
)

// Denies reports whether e is an entry that denies any of rights, by name or
// by a generic right that holds it: on macOS a KAUTH_ACE_DENY entry with one
// of rights, KAUTH_ACE_GENERIC_ALL or KAUTH_ACE_GENERIC_WRITE, whose rights
// hold each of DeleteRight and ChildRights (KAUTH_VNODE_GENERIC_WRITE_BITS).
// Which user or group the entry names is not read: Allowed says whether it
// binds this process.
func (e ACE) Denies(rights uint32) bool { return aceDenies(e, rights) }

// Inherited reports whether e passes to the files or folders made in the
// folder that holds it: on macOS KAUTH_ACE_FILE_INHERIT or
// KAUTH_ACE_DIRECTORY_INHERIT.
func (e ACE) Inherited() bool { return aceInherited(e) }

// Allowed reports whether this process may take rights on the file or folder
// at path, or the link itself when path is one, as the system decides it now
// with the process's own user and every group it is in. On macOS it asks
// faccessat(2) with the rights in its extended access bits (_DELETE_OK,
// _RMFILE_OK and the others in sys/unistd.h are the kauth rights shifted left
// by 8), AT_EACCESS and AT_SYMLINK_NOFOLLOW: access1 in xnu's vfs_syscalls.c
// hands them to vnode_authorize, which evaluates the ACL, the mode and the
// folder's rights as rename, unlink and rmdir do (vnode_authorize_delete in
// vfs_subr.c). A refusal is false with no error. On Linux, which keeps no
// ACL entries a caller could ask about, it fails with errors.ErrUnsupported.
func Allowed(path string, rights uint32) (bool, error) { return allowed(path, rights) }

// ACLForMode is the access ACL data, as AccessACLAttribute holds it, as it
// becomes on a file chmod gives mode. On Linux chmod rewrites the owner's
// entry, the mask (or, without one, the owning group's entry) and the other
// entry from mode and keeps every named entry (posix_acl_chmod_masq in
// fs/posix_acl.c), so a file given data and then mode holds what this
// returns; data whose layout it does not know is returned as it is. On macOS,
// which keeps ACLs apart from the mode, it is data.
func ACLForMode(data []byte, mode uint32) []byte { return aclForMode(data, mode) }

// SetFileSecurity gives fd, or path itself, not a link's target, when path
// is not empty, the ACL data, as FileSecurity read it from a file, or takes
// its ACL away when data is nil. On macOS fsetattrlist, or setattrlist with
// FSOPT_NOFOLLOW for a path, writes it as ATTR_CMN_EXTENDED_SECURITY, which
// a file's owner may write whatever its mode and its ACL deny, and a list of
// KAUTH_FILESEC_NOACL entries takes it away. On Linux, whose ACLs are
// extended attributes, it fails with EOPNOTSUPP for data and does nothing for
// nil. The error is the call's errno.
func SetFileSecurity(fd int, path string, data []byte) error {
	return setFileSecurity(fd, path, data)
}

// FileFlags returns the file flags of fd, or of path itself, not a link's
// target, when path is not empty: on macOS the chflags(2) flags of its
// status; on Linux the inode flags chattr(1) sets and lsattr(1) shows, which
// FS_IOC_GETFLAGS reads through fd. A Linux file system without such flags
// reports 0. Linux reads them only through a descriptor of the file itself,
// which a link does not give, and chattr cannot set them on a link, so there
// a path reports 0 without a call. The error is the call's errno.
func FileFlags(fd int, path string) (uint32, error) { return fileFlags(fd, path) }

// SetHidden gives fd, or path itself, not a link's target, when path is not
// empty, HiddenFlag when hidden, or takes it away, and keeps every other flag
// FileFlags reads. On macOS fchflags sets it through fd, and setattrlist with
// ATTR_CMN_FLAGS and FSOPT_NOFOLLOW on a path, since no call changes a link's
// flags through a descriptor; a file's owner may set it whatever its mode
// and its ACL deny. On Linux, which has no such flag, it fails with
// EOPNOTSUPP to set it and does nothing to take it away. The error is the
// call's errno.
func SetHidden(fd int, path string, hidden bool) error { return setHidden(fd, path, hidden) }

// NewLinkMode is the permissions mode a link this process makes gets under
// its umask, which a link made by a process it starts gets too, since that
// process inherits the umask. It is read with lstat from a link made in a new folder
// in folder, and both are removed again. On macOS a link has a mode of its
// own, 0777 less the umask (symlinkat in xnu's vfs_syscalls.c), which decides
// whether its owner may read the link and its attributes. On Linux ext4,
// Btrfs and tmpfs make every link 0777, and XFS does too unless its
// deprecated fs.xfs.irix_symlink_mode takes the umask off. The error is a
// call's errno.
func NewLinkMode(folder string) (uint32, error) {
	made, err := os.MkdirTemp(folder, "link-mode-")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(made) }()
	link := filepath.Join(made, "link")
	if err = unix.Symlink(".", link); err != nil {
		return 0, err
	}
	var stat unix.Stat_t
	if err = unix.Lstat(link, &stat); err != nil {
		return 0, err
	}
	return uint32(stat.Mode) & 0o777, nil
}

// SetLinkMode gives the link name in the folder fd the permissions mode
// mode, on the link itself and never on the file it points to, with
// fchmodat and AT_SYMLINK_NOFOLLOW: macOS changes the link's mode (chmod(2));
// Linux changes no link's mode, as notify_change in fs/attr.c refuses with
// EOPNOTSUPP, the error golang.org/x/sys gives too where the kernel has no
// fchmodat2, so a caller sets one only where it differs from the link's.
func SetLinkMode(fd int, name string, mode uint32) error {
	return unix.Fchmodat(fd, name, mode, unix.AT_SYMLINK_NOFOLLOW)
}

// HiddenFlag is the file flag that only hides a file in Finder: UF_HIDDEN on
// macOS, and 0 on Linux, which has no such flag.
const HiddenFlag = hiddenFlag

// KeptFlags are the file flags a new file renamed over a file has too, so a
// replacement keeps them: none on macOS. On Linux they are the flags the file
// system sets itself to record how it stores a file, which chattr cannot set
// or clear: ext4's extents (FS_EXTENT_FL), its indexed folders
// (FS_INDEX_FL), inline data (FS_INLINE_DATA_FL) and huge files
// (FS_HUGE_FILE_FL); and FS_ENCRYPT_FL, which every file made in an encrypted
// folder takes from it; fscrypt lets no file made elsewhere be renamed into
// one.
const KeptFlags = keptFlags

// FolderFlags are the file flags a folder may carry while entries in it are
// added, replaced and removed and the folder itself is left as it is: on
// macOS HiddenFlag alone; on Linux every flag but FS_IMMUTABLE_FL, under
// which nothing can be added, and FS_APPEND_FL, under which nothing can be
// renamed or removed, a temporary file included.
const FolderFlags = folderFlags

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

// ExtendedAttributeNames are the extended attribute names of fd, or of path
// itself, not a link's target, when fd is negative. A call without a buffer
// only bounds the name list's length: on overlayfs it also counts the
// trusted.overlay.* names the read leaves out, so the names are read into a
// buffer that size. A list that grew in between fails with ERANGE.
func ExtendedAttributeNames(fd int, path string) ([]string, error) {
	list := func(names []byte) (int, error) {
		if fd < 0 {
			return unix.Llistxattr(path, names)
		}
		return unix.Flistxattr(fd, names)
	}
	size, err := list(nil)
	if err != nil || size == 0 {
		return nil, err
	}
	names := make([]byte, size)
	size, err = list(names)
	if err != nil {
		return nil, err
	}
	return strings.FieldsFunc(string(names[:size]), func(r rune) bool { return r == 0 }), nil
}
