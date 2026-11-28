package platform

// ReadRight and SearchRight are the rights to read a file or list a folder,
// and to search a folder, as [Allowed] and [ACE.Denies] take them, beside
// DeleteRight and ChildRights: on macOS KAUTH_VNODE_READ_DATA (which is
// KAUTH_VNODE_LIST_DIRECTORY) and KAUTH_VNODE_EXECUTE (which is
// KAUTH_VNODE_SEARCH) of sys/kauth.h; on Linux, where FileSecurity lists no
// entries, 0.
const (
	ReadRight   = readRight
	SearchRight = searchRight
)
