package platform

import (
	"encoding/binary"
	"unsafe"

	"golang.org/x/sys/unix"
)

// nativeFilesystem reports whether path, or its first existing ancestor, is
// on APFS or HFS, by the file system type name statfs gives.
func nativeFilesystem(path string) (bool, error) {
	stat, err := existingStatfs(path)
	if err != nil {
		return false, err
	}
	name := unix.ByteSliceToString(stat.Fstypename[:])
	return name == "apfs" || name == "hfs", nil
}

// renameNoReplace is renameatx_np with RENAME_EXCL.
func renameNoReplace(from, to string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_EXCL)
}

// renameAt is renameatx_np, with RENAME_EXCL when exclusive.
func renameAt(fd int, from, to string, exclusive bool) error {
	flags := uint32(0)
	if exclusive {
		flags = unix.RENAME_EXCL
	}
	return unix.RenameatxNp(fd, from, fd, to, flags)
}

// fileFlags is the st_flags of fstat, or of lstat when path is not empty.
func fileFlags(fd int, path string) (uint32, error) {
	var stat unix.Stat_t
	var err error
	if path == "" {
		err = unix.Fstat(fd, &stat)
	} else {
		err = unix.Lstat(path, &stat)
	}
	return stat.Flags, err
}

// hiddenFlag is UF_HIDDEN, the flag by which Finder hides a file.
const hiddenFlag = unix.UF_HIDDEN

// keptFlags is none: a new file has no flags.
const keptFlags uint32 = 0

// folderFlags is UF_HIDDEN alone.
const folderFlags uint32 = hiddenFlag

// fileSecurity reads the ACL with getattrlist, or fgetattrlist for fd alone:
// Darwin does not expose ACLs through listxattr. getattrlist's
// extended-security attribute contains kauth_filesec (sys/kauth.h): magic,
// owner and group GUIDs, then the entry count (KAUTH_FILESEC_NOACL when there
// is no ACL), flags and 24-byte entries. An error is the call's errno, or
// ErrUnsupportedACL for an unexpected layout, so a caller fails closed.
//
// x/sys/unix wraps setattrlist but not getattrlist or fgetattrlist, so they
// go through libcCall6; switch once x/sys adds the wrappers. Each pointer
// becomes a uintptr inside the call's argument list, as the unsafe rules
// require.
func fileSecurity(fd int, path string) (entries []ACE, present bool, err error) {
	attributes := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Commonattr:  unix.ATTR_CMN_EXTENDED_SECURITY,
	}
	var data [8192]byte
	var errno unix.Errno
	if path == "" {
		_, _, errno = libcCall6(
			fgetattrlist,
			uintptr(fd),
			uintptr(unsafe.Pointer(&attributes)),
			uintptr(unsafe.Pointer(&data[0])),
			uintptr(len(data)),
			unix.FSOPT_NOFOLLOW,
			0,
		)
	} else {
		pointer, pathErr := unix.BytePtrFromString(path)
		if pathErr != nil {
			return nil, false, pathErr
		}
		_, _, errno = libcCall6(
			getattrlist,
			uintptr(unsafe.Pointer(pointer)),
			uintptr(unsafe.Pointer(&attributes)),
			uintptr(unsafe.Pointer(&data[0])),
			uintptr(len(data)),
			unix.FSOPT_NOFOLLOW,
			0,
		)
	}
	if errno != 0 {
		return nil, false, errno
	}
	length := int(binary.LittleEndian.Uint32(data[0:4]))
	offset := 4 + int(int32(binary.LittleEndian.Uint32(data[4:8])))
	size := int(binary.LittleEndian.Uint32(data[8:12]))
	if size == 0 {
		return nil, false, nil
	}
	if length > len(data) || offset < 12 || size < 44 || offset+size > length {
		return nil, false, ErrUnsupportedACL
	}
	security := data[offset : offset+size]
	count := binary.LittleEndian.Uint32(security[36:40])
	if binary.LittleEndian.Uint32(security[:4]) != 0x012cc16d {
		return nil, false, ErrUnsupportedACL
	}
	if count == 0xffffffff {
		return nil, false, nil
	}
	if count > 128 || size < 44+24*int(count) {
		return nil, false, ErrUnsupportedACL
	}
	entries = make([]ACE, count)
	for i := range entries {
		entry := security[44+24*i:]
		entries[i] = ACE{
			Flags:  binary.LittleEndian.Uint32(entry[16:20]),
			Rights: binary.LittleEndian.Uint32(entry[20:24]),
		}
	}
	return entries, true, nil
}
