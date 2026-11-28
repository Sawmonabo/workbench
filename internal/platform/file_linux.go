package platform

import (
	"errors"

	"golang.org/x/sys/unix"
)

// nativeFilesystem reports whether path, or its first existing ancestor, is
// on a native Linux filesystem, by the magic number statfs gives: ext, XFS,
// Btrfs, tmpfs or overlay, including under WSL. Windows, 9P, FUSE and network
// mounts are not.
func nativeFilesystem(path string) (bool, error) {
	stat, err := existingStatfs(path)
	if err != nil {
		return false, err
	}
	switch uint64(stat.Type) {
	case 0xef53, 0x58465342, 0x9123683e, 0x01021994, 0x794c7630: // ext, XFS, Btrfs, tmpfs, overlay
		return true, nil
	default:
		return false, nil
	}
}

// renameNoReplace is renameat2 with RENAME_NOREPLACE.
func renameNoReplace(from, to string) error {
	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
}

// renameAt is renameat2 with RENAME_NOREPLACE when exclusive, otherwise
// renameat.
func renameAt(fd int, from, to string, exclusive bool) error {
	if exclusive {
		return unix.Renameat2(fd, from, fd, to, unix.RENAME_NOREPLACE)
	}
	return unix.Renameat(fd, from, fd, to)
}

// fileSecurity reports no ACL: Linux keeps POSIX ACLs as the extended
// attributes system.posix_acl_access and system.posix_acl_default, which
// ExtendedAttributeNames lists.
func fileSecurity(_ int, _ string) (entries []ACE, present bool, err error) {
	return nil, false, nil
}

// fileFlags reads fd's inode flags with FS_IOC_GETFLAGS, whose answer the
// kernel writes as a 32-bit int although the request's number is built for a
// long. A file system without inode flags answers ENOTTY (9P, procfs, FUSE),
// or EOPNOTSUPP from a FUSE server, which is 0. A path reports 0 without a
// call: the ioctl needs a descriptor of the file, and opening a link with
// O_NOFOLLOW fails with ELOOP, which is also why chattr refuses links.
func fileFlags(fd int, path string) (uint32, error) {
	if path != "" {
		return 0, nil
	}
	flags, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
	if errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EOPNOTSUPP) {
		return 0, nil
	}
	return flags, err
}

// The inode flags of include/uapi/linux/fs.h that Workbench names, which
// golang.org/x/sys does not define.
const (
	immutableFlag  = 0x00000010 // FS_IMMUTABLE_FL
	appendFlag     = 0x00000020 // FS_APPEND_FL
	encryptFlag    = 0x00000800 // FS_ENCRYPT_FL
	indexFlag      = 0x00001000 // FS_INDEX_FL
	hugeFileFlag   = 0x00040000 // FS_HUGE_FILE_FL
	extentFlag     = 0x00080000 // FS_EXTENT_FL
	inlineDataFlag = 0x10000000 // FS_INLINE_DATA_FL
)

// hiddenFlag is 0: Linux has no flag that only hides a file.
const hiddenFlag = 0

// keptFlags are the flags ext4 sets itself to record how it stores a file,
// which chattr(1) cannot set or clear, and FS_ENCRYPT_FL, which fscrypt gives
// every file made in an encrypted folder.
const keptFlags uint32 = extentFlag | indexFlag | inlineDataFlag | hugeFileFlag | encryptFlag

// folderFlags are every flag but the two that stop a folder's entries
// changing: fs/namei.c refuses a new entry in an immutable folder
// (inode_permission) and a rename or unlink in an append-only one
// (may_delete).
const folderFlags = ^uint32(immutableFlag | appendFlag)
