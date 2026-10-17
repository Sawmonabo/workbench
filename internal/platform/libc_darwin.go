package platform

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The libSystem functions x/sys/unix does not wrap. cgo would break
// CGO_ENABLED=0 release builds, so they are called as x/sys calls libSystem:
// through the runtime's syscall.syscall6 and the trampolines in libc_darwin.s,
// whose addresses these variables hold.
var procPidRusage uintptr

// Getattrlist is the address of libSystem's getattrlist trampoline, exported
// while the ACL read in package operation calls it through LibcCall6.
var Getattrlist uintptr

// Fgetattrlist is the address of libSystem's fgetattrlist trampoline,
// exported while the ACL read in package operation calls it through LibcCall6.
var Fgetattrlist uintptr

//go:cgo_import_dynamic libc_getattrlist getattrlist "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_fgetattrlist fgetattrlist "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_proc_pid_rusage proc_pid_rusage "/usr/lib/libSystem.B.dylib"

// LibcCall6 is the runtime's syscall.syscall6, which calls a libSystem
// function on the system stack and returns errno when it returns -1.
//
//go:linkname LibcCall6 syscall.syscall6
func LibcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err unix.Errno)

// Footprint is the process's physical footprint, the figure Activity
// Monitor shows: proc_pid_rusage with RUSAGE_INFO_V0 (0), whose
// ri_phys_footprint is the uint64 at offset 72 of the 96-byte rusage_info_v0.
func Footprint(pid int) (uint64, error) {
	var info [96]byte
	_, _, errno := LibcCall6(
		procPidRusage,
		uintptr(pid),
		0,
		uintptr(unsafe.Pointer(&info[0])),
		0,
		0,
		0,
	)
	if errno != 0 {
		return 0, fmt.Errorf("proc_pid_rusage %d: %w", pid, errno)
	}
	return binary.LittleEndian.Uint64(info[72:80]), nil
}
