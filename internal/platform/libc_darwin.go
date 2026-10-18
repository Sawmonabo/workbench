package platform

import (
	"encoding/binary"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The libSystem functions x/sys/unix does not wrap. cgo would break
// CGO_ENABLED=0 release builds, so they are called as x/sys calls libSystem:
// through the runtime's syscall.syscall6 and the trampolines in libc_darwin.s,
// whose addresses these variables hold.
var (
	getattrlist, fgetattrlist, procPidRusage, procPidinfo uintptr
	hostStatistics64, machHostSelf                        uintptr
)

//go:cgo_import_dynamic libc_getattrlist getattrlist "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_fgetattrlist fgetattrlist "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_proc_pid_rusage proc_pid_rusage "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_proc_pidinfo proc_pidinfo "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_host_statistics64 host_statistics64 "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_host_self mach_host_self "/usr/lib/libSystem.B.dylib"

// libcCall6 is the runtime's syscall.syscall6, which calls a libSystem
// function on the system stack and returns errno when it returns -1.
//
//go:linkname libcCall6 syscall.syscall6
func libcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err unix.Errno)

// Footprint is the process's physical footprint, the figure Activity
// Monitor shows: proc_pid_rusage with RUSAGE_INFO_V0 (0), whose
// ri_phys_footprint is the uint64 at offset 72 of the 96-byte rusage_info_v0.
func Footprint(pid int) (uint64, error) {
	var info [96]byte
	_, _, errno := libcCall6(
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

// hostPort is mach_host_self(), the host's name port. Each call adds a
// reference to the send right, so it is taken once for the process rather
// than on every reading of a watcher that reads each second for days. The
// mach_port_t is the low 32 bits of the returned register.
var hostPort = sync.OnceValue(func() uintptr {
	port, _, _ := libcCall6(machHostSelf, 0, 0, 0, 0, 0, 0)
	return uintptr(uint32(port))
})

// usedPages is the pages in use as Activity Monitor's Memory Used counts
// them: App Memory (the anonymous pages less the purgeable ones) plus Wired
// plus Compressed. host_statistics64 with HOST_VM_INFO64 (4) fills struct
// vm_statistics64 (mach/vm_statistics.h), 416 bytes in its latest revision,
// with count HOST_VM_INFO64_COUNT, its size in integer_t (104); the kernel
// fills the revisions it knows and refuses a count below revision 0's. Read
// as natural_t (uint32): wire_count at byte 12, purgeable_count at 88,
// compressor_page_count at 128 and internal_page_count at 140. It returns a
// kern_return_t in the low 32 bits, 0 on success, and sets no errno.
func usedPages() (uint64, error) {
	const (
		hostVMInfo64 = 4
		size         = 416
		wireAt       = 12
		purgeableAt  = 88
		compressorAt = 128
		internalAt   = 140
	)
	var info [size]byte
	count := uint32(size / 4)
	kr, _, _ := libcCall6(
		hostStatistics64,
		hostPort(),
		hostVMInfo64,
		uintptr(unsafe.Pointer(&info[0])),
		uintptr(unsafe.Pointer(&count)),
		0,
		0,
	)
	if code := int32(kr); code != 0 {
		return 0, fmt.Errorf("host_statistics64: kern_return %d", code)
	}
	if int(count)*4 < internalAt+4 {
		return 0, fmt.Errorf(
			"host_statistics64: %d words, fewer than the %d read",
			count,
			internalAt/4+1,
		)
	}
	page := func(at int) uint64 { return uint64(binary.LittleEndian.Uint32(info[at : at+4])) }
	internal, purgeable := page(internalAt), page(purgeableAt)
	return internal - min(internal, purgeable) + page(wireAt) + page(compressorAt), nil
}
