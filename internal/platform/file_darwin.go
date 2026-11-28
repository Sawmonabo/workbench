package platform

import (
	"encoding/binary"
	"errors"
	"slices"
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

// fileSecurity reads the ACL with getattrlistat and FSOPT_NOFOLLOW, relative
// to the folder fd or, when fd is -1, to the working directory (AT_FDCWD,
// which getattrlist(2) says makes it getattrlist), or fgetattrlist for fd
// alone: Darwin does not expose ACLs through listxattr. getattrlist's
// extended-security attribute contains kauth_filesec (sys/kauth.h): magic,
// owner and group GUIDs, then the entry count (KAUTH_FILESEC_NOACL when there
// is no ACL), flags and 24-byte entries. An error is the call's errno, or
// ErrUnsupportedACL for an unexpected layout, so a caller fails closed.
//
// ACL's Data is the kauth_filesec up to its last entry, the size setattrlist
// takes; a list shorter than its entry count, or of more than 128 entries
// (KAUTH_ACL_MAX_ENTRIES), fails as unsupported.
//
// x/sys/unix wraps setattrlist but not getattrlistat or fgetattrlist, so they
// go through libcCall6; switch once x/sys adds the wrappers. Each pointer
// becomes a uintptr inside the call's argument list, as the unsafe rules
// require.
func fileSecurity(fd int, path string) (ACL, error) {
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
		pointer, err := unix.BytePtrFromString(path)
		if err != nil {
			return ACL{}, err
		}
		if fd < 0 {
			fd = unix.AT_FDCWD
		}
		_, _, errno = libcCall6(
			getattrlistat,
			uintptr(fd),
			uintptr(unsafe.Pointer(pointer)),
			uintptr(unsafe.Pointer(&attributes)),
			uintptr(unsafe.Pointer(&data[0])),
			uintptr(len(data)),
			unix.FSOPT_NOFOLLOW,
		)
	}
	if errno != 0 {
		return ACL{}, errno
	}
	length := int(binary.LittleEndian.Uint32(data[0:4]))
	offset := 4 + int(int32(binary.LittleEndian.Uint32(data[4:8])))
	size := int(binary.LittleEndian.Uint32(data[8:12]))
	if size == 0 {
		return ACL{}, nil
	}
	if length > len(data) || offset < 12 || size < filesecHeader || offset+size > length {
		return ACL{}, ErrUnsupportedACL
	}
	security := data[offset : offset+size]
	count := binary.LittleEndian.Uint32(security[36:40])
	if binary.LittleEndian.Uint32(security[:4]) != filesecMagic {
		return ACL{}, ErrUnsupportedACL
	}
	if count == filesecNoACL {
		return ACL{}, nil
	}
	if count > 128 || size < filesecHeader+aceSize*int(count) {
		return ACL{}, ErrUnsupportedACL
	}
	acl := ACL{
		Data:    slices.Clone(security[:filesecHeader+aceSize*int(count)]),
		Entries: make([]ACE, count),
	}
	for i := range acl.Entries {
		entry := security[filesecHeader+aceSize*i:]
		acl.Entries[i] = ACE{
			Flags:  binary.LittleEndian.Uint32(entry[16:20]),
			Rights: binary.LittleEndian.Uint32(entry[20:24]),
		}
	}
	return acl, nil
}

// The kauth_filesec layout of sys/kauth.h: its magic, the entry count that
// means no ACL, the bytes before the first entry (the magic, owner and group
// GUIDs, entry count and flags) and one kauth_ace's.
const (
	filesecMagic  = 0x012cc16d
	filesecNoACL  = 0xffffffff
	filesecHeader = 44
	aceSize       = 24
)

// The kauth rights and entry flags of sys/kauth.h the ACE methods and Allowed
// read: KAUTH_VNODE_DELETE; KAUTH_VNODE_ADD_FILE, ADD_SUBDIRECTORY and
// DELETE_CHILD; KAUTH_ACE_GENERIC_ALL and KAUTH_ACE_GENERIC_WRITE, which hold
// all of those (KAUTH_VNODE_GENERIC_WRITE_BITS); KAUTH_ACE_KINDMASK and
// KAUTH_ACE_DENY; KAUTH_ACE_FILE_INHERIT and KAUTH_ACE_DIRECTORY_INHERIT.
const (
	deleteRight      uint32 = 1 << 4
	removeChildRight uint32 = 1 << 6
	childRights      uint32 = 1<<2 | 1<<5 | removeChildRight
	genericRights    uint32 = 1<<21 | 1<<23
	aceKind          uint32 = 0xf
	aceDeny          uint32 = 2
	aceInherits      uint32 = 1<<5 | 1<<6
)

// aceDenies reports a KAUTH_ACE_DENY entry with one of rights or a generic
// right that holds them.
func aceDenies(e ACE, rights uint32) bool {
	return e.Flags&aceKind == aceDeny && e.Rights&(rights|genericRights) != 0
}

// aceInherited reports an entry with an inherit flag.
func aceInherited(e ACE) bool { return e.Flags&aceInherits != 0 }

// aceOnlyInherit is KAUTH_ACE_ONLY_INHERIT, an entry that only passes to what
// is made in a folder and that kauth_acl_evaluate skips.
const aceOnlyInherit uint32 = 1 << 8

// wellKnown is the 12 bytes every well-known GUID begins with, and the codes
// of its last four, big-endian, that kauth_wellknown_guid in xnu's
// kern_credential.c names: everyone, nobody, the file's owner and its group.
// Any other code is an ordinary GUID, such as a group's whose directory
// record gives it none and that ends in its group ID instead.
var wellKnown = [12]byte{0xab, 0xcd, 0xef, 0xab, 0xcd, 0xef, 0xab, 0xcd, 0xef, 0xab, 0xcd, 0xef}

const (
	everybodyCode uint32 = 0x0000000c
	nobodyCode    uint32 = 0xfffffffe
	ownerCode     uint32 = 0x0000000a
	groupCode     uint32 = 0x00000010
)

// aclBinds is kauth_acl_evaluate's rule for whether a's entry numbered entry
// applies to this process, its GUID the entry's first 16 bytes in Data.
// mbr_uid_to_uuid and mbr_check_membership (membership.h) return 0 or an
// error code as their value, not -1 and errno, as Go's own getpwnam_r call in
// internal/syscall/unix/user_darwin.go reads it, so the result is the call's
// first return.
func aclBinds(a ACL, entry int) bool {
	at := filesecHeader + aceSize*entry
	if entry < 0 || entry >= len(a.Entries) || at+aceSize > len(a.Data) {
		return true
	}
	if a.Entries[entry].Flags&aceOnlyInherit != 0 {
		return false
	}
	holder := [16]byte(a.Data[at : at+16])
	if [12]byte(holder[:12]) == wellKnown {
		switch binary.BigEndian.Uint32(holder[12:]) {
		case everybodyCode, ownerCode, groupCode:
			return true
		case nobodyCode:
			return false
		}
	}
	var user [16]byte
	if rc, _, _ := libcCall6(
		mbrUIDToUUID,
		uintptr(unix.Geteuid()),
		uintptr(unsafe.Pointer(&user[0])),
		0, 0, 0, 0,
	); int32(rc) != 0 || user == holder {
		return true
	}
	var member int32
	rc, _, _ := libcCall6(
		mbrCheckMembership,
		uintptr(unsafe.Pointer(&user[0])),
		uintptr(unsafe.Pointer(&holder[0])),
		uintptr(unsafe.Pointer(&member)),
		0, 0, 0,
	)
	return int32(rc) != 0 || member != 0
}

// aclForMode is data: chmod leaves a macOS ACL as it is.
func aclForMode(data []byte, _ uint32) []byte { return data }

// allowed is faccessat(AT_FDCWD, path, rights<<8, AT_EACCESS|
// AT_SYMLINK_NOFOLLOW): EACCES and EPERM are a refusal, any other error the
// call's errno.
func allowed(path string, rights uint32) (bool, error) {
	err := unix.Faccessat(unix.AT_FDCWD, path, rights<<8, unix.AT_EACCESS|unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		return false, nil
	}
	return false, err
}

// setFileSecurity writes data, or a kauth_filesec of KAUTH_FILESEC_NOACL
// entries for nil, as ATTR_CMN_EXTENDED_SECURITY with fsetattrlist, or
// setattrlist with FSOPT_NOFOLLOW for a path. Its buffer is the attribute's
// attrreference, whose offset counts from the reference itself, then the
// kauth_filesec, whose size setattrlist checks against the entry count it
// holds (vfs_attrlist.c). x/sys/unix wraps setattrlist but not fsetattrlist,
// which goes through libcCall6, its pointers becoming uintptrs inside the
// call's argument list, as the unsafe rules require.
func setFileSecurity(fd int, path string, data []byte) error {
	if data == nil {
		data = make([]byte, filesecHeader)
		binary.LittleEndian.PutUint32(data[:4], filesecMagic)
		binary.LittleEndian.PutUint32(data[36:40], filesecNoACL)
	}
	buffer := make([]byte, 8+len(data))
	binary.LittleEndian.PutUint32(buffer[:4], 8)
	binary.LittleEndian.PutUint32(buffer[4:8], uint32(len(data)))
	copy(buffer[8:], data)
	return setAttributes(fd, path, unix.ATTR_CMN_EXTENDED_SECURITY, buffer)
}

// setHidden sets or clears UF_HIDDEN in the flags fstat, or lstat for a
// path, reads, with fchflags, or setattrlist's ATTR_CMN_FLAGS and
// FSOPT_NOFOLLOW for a path, which is how libSystem's lchflags changes a
// link's own flags.
func setHidden(fd int, path string, hidden bool) error {
	flags, err := fileFlags(fd, path)
	if err != nil {
		return err
	}
	if hidden {
		flags |= hiddenFlag
	} else {
		flags &^= hiddenFlag
	}
	if path == "" {
		return unix.Fchflags(fd, int(flags))
	}
	buffer := make([]byte, 4)
	binary.LittleEndian.PutUint32(buffer, flags)
	return setAttributes(-1, path, unix.ATTR_CMN_FLAGS, buffer)
}

// setAttributes writes the common attribute common from buffer with
// fsetattrlist on fd, or setattrlist with FSOPT_NOFOLLOW on path when path is
// not empty.
func setAttributes(fd int, path string, common uint32, buffer []byte) error {
	attributes := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: common}
	if path != "" {
		return unix.Setattrlist(path, &attributes, buffer, unix.FSOPT_NOFOLLOW)
	}
	_, _, errno := libcCall6(
		fsetattrlist,
		uintptr(fd),
		uintptr(unsafe.Pointer(&attributes)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		0,
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}
