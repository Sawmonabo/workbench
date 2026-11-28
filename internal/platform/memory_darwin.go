package platform

import (
	"encoding/binary"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// machineMemory reads the kernel's pressure level, the RAM, the pages in use
// (usedPages) times hw.pagesize and vm.swapusage (struct xsw_usage: total,
// avail, used as uint64).
func machineMemory() (Memory, error) {
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	if err != nil {
		return Memory{}, err
	}
	installed, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return Memory{}, err
	}
	pages, err := usedPages()
	if err != nil {
		return Memory{}, err
	}
	pagesize, err := unix.SysctlUint32("hw.pagesize")
	if err != nil {
		return Memory{}, err
	}
	m := Memory{
		Level: int(level), Installed: installed,
		Used: pages * uint64(pagesize), FullAvg10: -1,
	}
	if swap, err := unix.SysctlRaw("vm.swapusage"); err == nil && len(swap) >= 24 {
		m.SwapTotal = binary.LittleEndian.Uint64(swap[0:8])
		m.SwapFree = binary.LittleEndian.Uint64(swap[8:16])
	}
	return m, nil
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
// kern_return_t in the low 32 bits, 0 on success, and sets no errno; a count
// written back short of internal_page_count is an error too.
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
			"host_statistics64 filled %d words, fewer than the %d the reading needs",
			count,
			internalAt/4+1,
		)
	}
	page := func(at int) uint64 { return uint64(binary.LittleEndian.Uint32(info[at : at+4])) }
	internal, purgeable := page(internalAt), page(purgeableAt)
	return internal - min(internal, purgeable) + page(wireAt) + page(compressorAt), nil
}

// cgroupMemory reports false: macOS has no cgroups, so no process is confined
// to less memory than the machine's.
func cgroupMemory(_ Memory) (Memory, bool) {
	return Memory{}, false
}
