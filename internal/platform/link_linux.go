package platform

import (
	"strconv"

	"golang.org/x/sys/unix"
)

// openLink is openat(2) of name with O_PATH and O_NOFOLLOW, then fstat to
// check that it opened a link.
func openLink(dir int, name string) (*Link, error) {
	fd, err := unix.Openat(dir, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err == nil && stat.Mode&unix.S_IFMT != unix.S_IFLNK {
		err = unix.EINVAL
	}
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &Link{fd: fd}, nil
}

// heldLink is the name of the link fd holds: /proc/self/fd/N, which the
// calls that follow a link take to that link.
func heldLink(fd int) string { return "/proc/self/fd/" + strconv.Itoa(fd) }

// listLinkAttributes is listxattr(2) of heldLink.
func listLinkAttributes(fd int, data []byte) (int, error) {
	return unix.Listxattr(heldLink(fd), data)
}

// linkAttribute is getxattr(2) of heldLink.
func linkAttribute(fd int, name string, data []byte) (int, error) {
	return unix.Getxattr(heldLink(fd), name, data)
}

// setLinkAttribute is setxattr(2) of heldLink.
func setLinkAttribute(fd int, name string, value []byte) error {
	return unix.Setxattr(heldLink(fd), name, value, 0)
}

// removeLinkAttribute is removexattr(2) of heldLink.
func removeLinkAttribute(fd int, name string) error {
	return unix.Removexattr(heldLink(fd), name)
}
