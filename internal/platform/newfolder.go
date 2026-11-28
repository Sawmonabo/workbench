package platform

// NewFolderMode is the permission bits mkdir(2) gives a folder this process
// makes with mode: mode less the process's umask. On Linux it reads the
// umask from the Umask line of /proc/self/status (Linux 4.7), which changes
// nothing; a folder whose parent holds a default ACL takes its mode from
// that ACL instead, and Workbench refuses such a folder before it writes
// there. macOS keeps no such record, umask(2) sets the mask to read it, and
// no new folder takes a set-group-ID bit there (FolderTakesSetgid), the one
// use of this, so it fails with errors.ErrUnsupported. The error is the
// read's.
func NewFolderMode(mode uint32) (uint32, error) { return newFolderMode(mode) }
