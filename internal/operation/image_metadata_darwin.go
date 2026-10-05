package operation

import (
	"encoding/binary"
	"unsafe"

	"golang.org/x/sys/unix"
)

// preservedAttributes are the extended attributes images carry verbatim. Each
// is small, grants no access and is commonly added by the system: download
// provenance and quarantine, Finder flags and the last used date. Every other
// attribute blocks planning, and so does an ACL except where folderACL admits it.
var preservedAttributes = []string{
	"com.apple.provenance",
	"com.apple.quarantine",
	"com.apple.FinderInfo",
	"com.apple.lastuseddate#PS",
}

func replaceImage(fd int, from, to string, absent bool) error {
	flags := uint32(0)
	if absent {
		flags = unix.RENAME_EXCL
	}
	return unix.RenameatxNp(fd, from, fd, to, flags)
}

func fileMetadata(fd int, path string) error { return metadata(fd, path, noACL) }

// containerMetadata is fileMetadata for a folder that holds Workbench's own
// files. Workbench never removes or replaces it, and writeDirectoryImage sets
// its mode, group and attributes through a descriptor without touching its ACL,
// so it may keep the ACL entries folderACL admits. Its flags still block.
func containerMetadata(fd int, path string) error { return metadata(fd, path, folderACL) }

func metadata(fd int, path string, acl func([]aclEntry, bool, error) error) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Flags != 0 {
		return Fail(ExitBlocked, "metadata", "Target filesystem flags cannot be preserved")
	}
	return acl(darwinACL(fd, nil))
}

// parentMetadata admits the folder a target is written into. Workbench adds,
// replaces and removes entries there but never changes the folder itself, so
// the folder may keep an ACL folderACL admits, such as the "group:everyone deny
// delete" macOS puts on the home folder and its standard folders, and the
// hidden flag macOS puts on ~/Library, which only hides the folder in Finder.
func parentMetadata(fd int, path string) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Flags&^unix.UF_HIDDEN != 0 {
		return Fail(ExitBlocked, "metadata", "Target folder filesystem flags cannot be preserved")
	}
	return folderACL(darwinACL(fd, nil))
}

// folderACL admits the ACL of a folder whose own entries Workbench leaves
// alone: no entry may pass to new entries or deny adding or removing them.
// macOS puts one admitted entry, "group:everyone deny delete", on the home
// folder and its standard folders such as ~/Library; it only stops the folder
// itself from being deleted.
func folderACL(entries []aclEntry, _ bool, err error) error {
	if err != nil {
		return err
	}
	const (
		inherits     = 1<<5 | 1<<6        // KAUTH_ACE_FILE_INHERIT, KAUTH_ACE_DIRECTORY_INHERIT
		kind         = 0xf                // KAUTH_ACE_KINDMASK
		deny         = 2                  // KAUTH_ACE_DENY
		childChanges = 1<<2 | 1<<5 | 1<<6 // KAUTH_VNODE_ADD_FILE, ADD_SUBDIRECTORY, DELETE_CHILD
	)
	for _, entry := range entries {
		if entry.flags&inherits != 0 || entry.flags&kind == deny && entry.rights&childChanges != 0 {
			return Fail(
				ExitBlocked,
				"metadata",
				"Target folder ACL passes to new entries or denies changing them",
			)
		}
	}
	return nil
}

func linkMetadata(path string) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil || stat.Flags != 0 {
		return Fail(ExitBlocked, "metadata", "Target link flags cannot be preserved")
	}
	pointer, err := unix.BytePtrFromString(path)
	if err != nil {
		return err
	}
	return noACL(darwinACL(-1, pointer))
}

// noACL rejects any ACL, even an empty one, on a target Workbench replaces:
// the replacement would not carry it.
func noACL(_ []aclEntry, present bool, err error) error {
	if err == nil && present {
		return Fail(
			ExitBlocked,
			"metadata",
			"Targets with ACLs require reviewed preservation support",
		)
	}
	return err
}

// aclEntry is one kauth_ace (sys/kauth.h) after its 16-byte applicable GUID.
type aclEntry struct{ flags, rights uint32 }

// darwinACL returns the ACL entries of fd, or of path when it is non-nil, and
// whether an ACL is present at all. Darwin does not expose ACLs through
// listxattr. getattrlist's extended-security attribute contains
// kauth_filesec (sys/kauth.h): magic, owner and group GUIDs, then the entry
// count (KAUTH_FILESEC_NOACL when there is no ACL), flags and 24-byte entries.
//
// x/sys/unix wraps setattrlist through libSystem but not getattrlist or
// fgetattrlist, and cgo's acl(3) would break CGO_ENABLED=0 release builds, so
// this uses the raw syscalls Go deprecates on darwin. An error or unexpected
// layout fails closed; recheck on each major macOS release and switch once
// x/sys adds the wrappers. Each pointer becomes a uintptr inside the Syscall6
// argument list, as the unsafe rules require.
func darwinACL(fd int, path *byte) ([]aclEntry, bool, error) {
	attributes := struct {
		Count, Reserved                       uint16
		Common, Volume, Directory, File, Fork uint32
	}{Count: 5, Common: unix.ATTR_CMN_EXTENDED_SECURITY}
	var data [8192]byte
	var errno unix.Errno
	if path == nil {
		_, _, errno = unix.Syscall6(
			unix.SYS_FGETATTRLIST, //nolint:staticcheck // See the function comment.
			uintptr(fd),
			uintptr(unsafe.Pointer(&attributes)),
			uintptr(unsafe.Pointer(&data[0])),
			uintptr(len(data)),
			unix.FSOPT_NOFOLLOW,
			0,
		)
	} else {
		_, _, errno = unix.Syscall6(
			unix.SYS_GETATTRLIST, //nolint:staticcheck // See the function comment.
			uintptr(unsafe.Pointer(path)),
			uintptr(unsafe.Pointer(&attributes)),
			uintptr(unsafe.Pointer(&data[0])),
			uintptr(len(data)),
			unix.FSOPT_NOFOLLOW,
			0,
		)
	}
	if errno != 0 {
		return nil, false, Fail(ExitBlocked, "metadata", "Cannot verify target ACL semantics")
	}
	unsupported := Fail(ExitBlocked, "metadata", "Unsupported target ACL metadata")
	length := int(binary.LittleEndian.Uint32(data[0:4]))
	offset := 4 + int(int32(binary.LittleEndian.Uint32(data[4:8])))
	size := int(binary.LittleEndian.Uint32(data[8:12]))
	if size == 0 {
		return nil, false, nil
	}
	if length > len(data) || offset < 12 || size < 44 || offset+size > length {
		return nil, false, unsupported
	}
	security := data[offset : offset+size]
	count := binary.LittleEndian.Uint32(security[36:40])
	if binary.LittleEndian.Uint32(security[:4]) != 0x012cc16d {
		return nil, false, unsupported
	}
	if count == 0xffffffff {
		return nil, false, nil
	}
	if count > 128 || size < 44+24*int(count) {
		return nil, false, unsupported
	}
	entries := make([]aclEntry, count)
	for i := range entries {
		entry := security[44+24*i:]
		entries[i] = aclEntry{
			flags:  binary.LittleEndian.Uint32(entry[16:20]),
			rights: binary.LittleEndian.Uint32(entry[20:24]),
		}
	}
	return entries, true, nil
}
