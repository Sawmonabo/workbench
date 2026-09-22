package operation

import (
	"encoding/binary"
	"unsafe"

	"golang.org/x/sys/unix"
)

func replaceImage(fd int, from, to string, absent bool) error {
	flags := uint32(0)
	if absent {
		flags = unix.RENAME_EXCL
	}
	return unix.RenameatxNp(fd, from, fd, to, flags)
}

func fileMetadata(fd int, path string) error {
	if err := privateFilesystem(path); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Flags != 0 {
		return Fail(ExitBlocked, "metadata", "Target filesystem flags cannot be preserved")
	}
	return darwinACL(fd, nil)
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
	return darwinACL(-1, pointer)
}

// darwinACL inspects fd, or path when it is non-nil. Darwin does not expose
// ACLs through listxattr. getattrlist's extended-security attribute contains
// kauth_filesec (sys/kauth.h); NOACL is the sole accepted ACL marker. An empty
// but present ACL also carries semantics and is rejected.
//
// x/sys/unix wraps setattrlist through libSystem but not getattrlist or
// fgetattrlist, and cgo's acl(3) would break CGO_ENABLED=0 release builds, so
// this uses the raw syscalls Go deprecates on darwin. An error or unexpected
// layout fails closed; recheck on each major macOS release and switch once
// x/sys adds the wrappers. Each pointer becomes a uintptr inside the Syscall6
// argument list, as the unsafe rules require.
func darwinACL(fd int, path *byte) error {
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
		return Fail(ExitBlocked, "metadata", "Cannot verify target ACL semantics")
	}
	length := int(binary.LittleEndian.Uint32(data[0:4]))
	offset := 4 + int(int32(binary.LittleEndian.Uint32(data[4:8])))
	size := int(binary.LittleEndian.Uint32(data[8:12]))
	if size == 0 {
		return nil
	}
	if length > len(data) || offset < 12 || size < 44 || offset+size > length {
		return Fail(ExitBlocked, "metadata", "Unsupported target ACL metadata")
	}
	security := data[offset : offset+size]
	if binary.LittleEndian.Uint32(security[:4]) != 0x012cc16d ||
		binary.LittleEndian.Uint32(security[36:40]) != 0xffffffff {
		return Fail(
			ExitBlocked,
			"metadata",
			"Targets with ACLs require reviewed preservation support",
		)
	}
	return nil
}
