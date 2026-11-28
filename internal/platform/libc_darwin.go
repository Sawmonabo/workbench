package platform

import (
	_ "unsafe"

	"golang.org/x/sys/unix"
)

// The libSystem functions x/sys/unix does not wrap, and sysctl, which it
// wraps only with a buffer it makes anew at every call. cgo would break
// CGO_ENABLED=0 release builds, so they are called as x/sys calls libSystem:
// through the runtime's syscall.syscall6 and the trampolines in libc_darwin.s,
// whose addresses these variables hold.
var (
	getattrlist, getattrlistat, fgetattrlist, fsetattrlist uintptr
	procPidRusage, procPidinfo, procListChildPids          uintptr
	hostStatistics64, machHostSelf, sysctl, mbrUIDToUUID   uintptr
)

//go:cgo_import_dynamic libc_getattrlist getattrlist "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_getattrlistat getattrlistat "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_fgetattrlist fgetattrlist "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_fsetattrlist fsetattrlist "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_proc_pid_rusage proc_pid_rusage "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_proc_pidinfo proc_pidinfo "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_proc_listchildpids proc_listchildpids "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_host_statistics64 host_statistics64 "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_host_self mach_host_self "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_sysctl sysctl "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mbr_uid_to_uuid mbr_uid_to_uuid "/usr/lib/libSystem.B.dylib"

// libcCall6 is the runtime's syscall.syscall6, which calls a libSystem
// function on the system stack and returns errno when it returns -1.
//
//go:linkname libcCall6 syscall.syscall6
func libcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err unix.Errno)
