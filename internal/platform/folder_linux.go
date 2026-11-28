package platform

import (
	"errors"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// openFolderAt is openat(2) of path in the folder dir with O_PATH,
// O_DIRECTORY and O_NOFOLLOW.
func openFolderAt(dir int, path string) (*os.File, error) {
	fd, err := unix.Openat(
		dir,
		path,
		unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

// chmodFolder is fchmodat2(fd, "", mode, AT_EMPTY_PATH), and, where the
// kernel lacks fchmodat2 (x/sys gives EOPNOTSUPP for its ENOSYS) or a
// seccomp filter refuses it (EPERM, ENOSYS), chmod of /proc/self/fd/N.
func chmodFolder(f *os.File, mode os.FileMode) error {
	bits := ModeBits(mode)
	fd := int(f.Fd())
	err := unix.Fchmodat(fd, "", bits, unix.AT_EMPTY_PATH)
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) ||
		errors.Is(err, unix.EPERM) {
		err = unix.Chmod("/proc/self/fd/"+strconv.Itoa(fd), bits)
	}
	return err
}

// chmodFolderAt is fstatat(2) with AT_SYMLINK_NOFOLLOW, openFolderAt and
// chmodFolder.
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
	folder, err := openFolderAt(fd, name)
	if err != nil {
		return nil, err
	}
	if err = chmodFolder(folder, mode); err != nil {
		_ = folder.Close()
		return nil, err
	}
	return folder, nil
}

// othersMayWrite is -1: the mode's group bits hold the ACL's mask.
func othersMayWrite(int, string) (int, error) { return -1, nil }

// othersMaySearch is -1: the mode's group bits hold the ACL's mask.
func othersMaySearch(int, string) (int, error) { return -1, nil }

// modeLocks reads the two attributes with statx(2).
func modeLocks(path string) (uint32, error) {
	var stat unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, 0, &stat); err != nil {
		return 0, err
	}
	var flags uint32
	if stat.Attributes&unix.STATX_ATTR_IMMUTABLE != 0 {
		flags |= immutableFlag
	}
	if stat.Attributes&unix.STATX_ATTR_APPEND != 0 {
		flags |= appendFlag
	}
	return flags, nil
}
