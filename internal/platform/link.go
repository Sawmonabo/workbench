package platform

import "golang.org/x/sys/unix"

// Link is a link opened as itself, not the file it points to: Workbench
// writes a link's extended attributes, hidden flag and ACL through it, so
// every write reaches the one link it opened, whatever a later rename puts
// at its path. On macOS it is a descriptor opened with O_SYMLINK, "for the
// symbolic link itself, not what it links to" (open(2)), which fsetxattr,
// fchflags and fsetattrlist take as the link; opening one takes its owner's
// read permission. On Linux it is a descriptor opened with O_PATH and
// O_NOFOLLOW, which "refer[s] to the symbolic link" (open(2)) and which the
// extended attribute calls refuse (EBADF) up to the setxattrat family of
// Linux 6.13: they reach the link as /proc/self/fd/N, with the calls that
// follow a link, since that name leads to the file the descriptor holds,
// the link itself (proc_pid_fd(5)). A Linux link has no hidden flag and no
// ACL.
type Link struct{ fd int }

// OpenLink opens name in the folder dir as the link itself, and fails with
// EINVAL where name is no link. The error is a call's errno.
func OpenLink(dir int, name string) (*Link, error) { return openLink(dir, name) }

// Close closes l.
func (l *Link) Close() error { return unix.Close(l.fd) }

// Stat is the status of the link l holds.
func (l *Link) Stat(stat *unix.Stat_t) error { return unix.Fstat(l.fd, stat) }

// Attributes lists the names of l's extended attributes into data, as
// listxattr(2) does; the error is the call's errno.
func (l *Link) Attributes(data []byte) (int, error) { return listLinkAttributes(l.fd, data) }

// Attribute reads l's extended attribute name into data, as getxattr(2)
// does; the error is the call's errno.
func (l *Link) Attribute(name string, data []byte) (int, error) {
	return linkAttribute(l.fd, name, data)
}

// SetAttribute gives l the extended attribute name with value; the error is
// the call's errno.
func (l *Link) SetAttribute(name string, value []byte) error {
	return setLinkAttribute(l.fd, name, value)
}

// RemoveAttribute takes l's extended attribute name off; the error is the
// call's errno.
func (l *Link) RemoveAttribute(name string) error { return removeLinkAttribute(l.fd, name) }

// SetHidden gives l HiddenFlag when hidden, or takes it away, as SetHidden
// does for a descriptor.
func (l *Link) SetHidden(hidden bool) error { return setHidden(l.fd, hidden) }

// SetSecurity gives l the security data of an image, as SetFileSecurity does
// for a descriptor.
func (l *Link) SetSecurity(data []byte) error { return setFileSecurity(l.fd, data) }
