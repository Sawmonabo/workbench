package platform

import "golang.org/x/sys/unix"

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
// ExtendedAttributeCount counts.
func fileSecurity(_ int, _ string) (entries []ACE, present bool, err error) {
	return nil, false, nil
}

// fileFlags reports 0 without a call: a Linux file's status holds no file
// flags.
func fileFlags(_ int, _ string) (uint32, error) { return 0, nil }

// hiddenFlag is 0: Linux has no file flags.
const hiddenFlag = 0
