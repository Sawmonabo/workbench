package platform

import (
	"encoding/binary"
	"unsafe"

	"golang.org/x/sys/unix"
)

// SwapNames swaps the files at a and b, of any kind, in one system call, so
// no moment exists in which either name is missing: renamex_np with
// RENAME_SWAP. Both must exist on one file system. A file system without the
// flag fails it with ErrSwapUnsupported (see swapError).
func SwapNames(a, b string) error {
	return swapError(a, b, unix.RenamexNp(a, b, unix.RENAME_SWAP))
}

// CanSwap reports whether the file system holding path, or its first
// existing folder above, can swap two names (see SwapNames), without trying:
// whether its volume has VOL_CAP_INT_RENAME_SWAP, read with getattrlist on
// the volume's mount point, as renamex_np(2) says. APFS has it and HFS+ does
// not. The error is a statfs or getattrlist failure.
func CanSwap(path string) (bool, error) {
	stat, err := existingStatfs(path)
	if err != nil {
		return false, err
	}
	root, err := unix.BytePtrFromString(unix.ByteSliceToString(stat.Mntonname[:]))
	if err != nil {
		return false, err
	}
	attributes := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Volattr:     unix.ATTR_VOL_INFO | unix.ATTR_VOL_CAPABILITIES,
	}
	// The length, then a vol_capabilities_attr_t: capabilities[4] and
	// valid[4], each a u_int32_t.
	var data [36]byte
	if _, _, errno := libcCall6(
		getattrlist,
		uintptr(unsafe.Pointer(root)),
		uintptr(unsafe.Pointer(&attributes)),
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		0,
		0,
	); errno != 0 {
		return false, errno
	}
	// Index 1 of each array is VOL_CAPABILITIES_INTERFACES.
	capabilities := binary.LittleEndian.Uint32(data[8:12])
	valid := binary.LittleEndian.Uint32(data[24:28])
	return capabilities&valid&volCapRenameSwap != 0, nil
}

// volCapRenameSwap is VOL_CAP_INT_RENAME_SWAP of <sys/attr.h>, which x/sys
// does not define.
const volCapRenameSwap = 0x00040000
