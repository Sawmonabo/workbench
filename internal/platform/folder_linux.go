package platform

import (
	"errors"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// openFolder is open(2) with O_PATH, O_DIRECTORY and O_NOFOLLOW.
func openFolder(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

// chmodFolder is fchmodat2(fd, "", mode, AT_EMPTY_PATH), and, where the
// kernel lacks fchmodat2 (x/sys gives EOPNOTSUPP for its ENOSYS) or a
// seccomp filter refuses it (EPERM, ENOSYS), chmod of /proc/self/fd/N.
func chmodFolder(f *os.File, mode os.FileMode) error {
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
	fd := int(f.Fd())
	err := unix.Fchmodat(fd, "", bits, unix.AT_EMPTY_PATH)
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) ||
		errors.Is(err, unix.EPERM) {
		err = unix.Chmod("/proc/self/fd/"+strconv.Itoa(fd), bits)
	}
	return err
}

// othersMayWrite is false: the mode's group bits hold the ACL's mask.
func othersMayWrite(int, string) (bool, error) { return false, nil }
