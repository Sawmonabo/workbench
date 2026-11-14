package platform

import (
	"encoding/binary"
	"unsafe"
)

// ProcessLineage is the process's Lineage from proc_pidinfo with
// PROC_PIDUNIQIDENTIFIERINFO (17), a flavor absent from the public SDK
// headers that answers for another process without privileges. It
// fills struct proc_uniqidentifierinfo, whose p_idversion, the int32 at byte
// 32, is the process's own version and p_orig_ppidversion, the int32 at byte
// 36, the version of the process that forked it; the kernel keeps that one
// when launchd adopts the child. The version is one counter for the whole
// system, raised at each fork and exec, so a version names one process image
// and never a later one that reuses its PID. proc_pidinfo returns the bytes
// it wrote (56) as a C int; fewer than the 40 the two fields need, as for a
// process that has exited, reports false, which tells callers nothing about
// where the process came from.
func ProcessLineage(pid int) (Lineage, bool) {
	const (
		uniqIdentifierInfo = 17
		size               = 64
		versionAt          = 32
		parentAt           = 36
	)
	var info [size]byte
	written, _, _ := libcCall6(
		procPidinfo,
		uintptr(pid),
		uniqIdentifierInfo,
		0,
		uintptr(unsafe.Pointer(&info[0])),
		size,
		0,
	)
	// Only the low 32 bits of the returned register hold the C int.
	if int32(written) < parentAt+4 {
		return Lineage{}, false
	}
	return Lineage{
		Version:       binary.LittleEndian.Uint32(info[versionAt : versionAt+4]),
		ParentVersion: binary.LittleEndian.Uint32(info[parentAt : parentAt+4]),
	}, true
}
