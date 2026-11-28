package platform

import "golang.org/x/sys/unix"

// openLink is openat(2) of name with O_SYMLINK, then fstat to check that it
// opened a link: on a file O_SYMLINK opens the file.
func openLink(dir int, name string) (*Link, error) {
	fd, err := unix.Openat(
		dir,
		name,
		unix.O_SYMLINK|unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC,
		0,
	)
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

// listLinkAttributes is flistxattr(2) of fd.
func listLinkAttributes(fd int, data []byte) (int, error) { return unix.Flistxattr(fd, data) }

// linkAttribute is fgetxattr(2) of fd.
func linkAttribute(fd int, name string, data []byte) (int, error) {
	return unix.Fgetxattr(fd, name, data)
}

// setLinkAttribute is fsetxattr(2) of fd.
func setLinkAttribute(fd int, name string, value []byte) error {
	return unix.Fsetxattr(fd, name, value, 0)
}

// removeLinkAttribute is fremovexattr(2) of fd.
func removeLinkAttribute(fd int, name string) error { return unix.Fremovexattr(fd, name) }
