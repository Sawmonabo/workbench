package platform

import (
	"encoding/binary"

	"golang.org/x/sys/unix"
)

// MachineMemory reads the kernel's pressure level, the RAM, the pages in use
// (usedPages) times hw.pagesize and vm.swapusage (struct xsw_usage: total,
// avail, used as uint64).
func MachineMemory() (Memory, error) {
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

// CgroupMemory reports false: macOS has no cgroups, so no process is confined
// to less memory than the machine's.
func CgroupMemory(_ Memory) (Memory, bool) {
	return Memory{}, false
}
