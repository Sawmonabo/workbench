package platform

// Memory is one reading of the machine's memory. On macOS Level is
// kern.memorystatus_vm_pressure_level (1 normal, 2 warning, 4 critical),
// which decides with the swap. On Linux Level is 0, Total is earlyoom's
// figure (MemAvailable + AnonPages), so Available/Total is the share user
// memory can still take, and FullAvg10 is the memory pressure file's full
// avg10, -1 when absent. Installed is the machine's RAM; a cgroup's reading
// (see CgroupMemory) has its memory limit as Installed. Used is the memory
// in use of Installed, as the system's own monitor counts it: on macOS the
// anonymous, wired and compressed pages less the purgeable ones, as Activity
// Monitor's Memory Used; on Linux MemTotal less MemAvailable, as free(1)
// since procps-ng 4.0; in a cgroup what it holds beyond inactive file cache.
type Memory struct {
	Level               int
	Available, Total    uint64
	Installed, Used     uint64
	SwapFree, SwapTotal uint64
	FullAvg10           float64
}

// MachineMemory is one reading of the machine's memory. The error is the
// system's when a figure the reading needs cannot be read.
func MachineMemory() (Memory, error) { return machineMemory() }

// CgroupMemory is the memory of the cgroup v2 at or above this process with
// the lowest memory.max, as a container's --memory or a unit's MemoryMax=
// sets it, where the kernel's OOM killer acts before the machine runs short;
// machine is the MachineMemory reading it is weighed against. Available is
// the limit less what the cgroup holds beyond inactive file cache; Total adds
// its anonymous memory (earlyoom's figure within the cgroup); the swap is the
// cgroup's when memory.swap.max is lower than the machine's. Used is what the
// cgroup holds, of the limit as Installed. ok is false without cgroup v2 or
// such a limit, when the limit is no lower than the machine's memory, and
// always on macOS, which has no cgroups.
func CgroupMemory(machine Memory) (Memory, bool) { return cgroupMemory(machine) }
