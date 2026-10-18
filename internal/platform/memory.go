package platform

// Memory is one reading of the machine's memory. On macOS Level is
// kern.memorystatus_vm_pressure_level (1 normal, 2 warning, 4 critical),
// which decides with the swap. On Linux Level is 0, Total is earlyoom's
// figure (MemAvailable + AnonPages), so Available/Total is the share user
// memory can still take, and FullAvg10 is the memory pressure file's full
// avg10, -1 when absent. Installed is the machine's RAM; a cgroup's reading
// (see CgroupMemory) has its memory limit as Installed.
type Memory struct {
	Level               int
	Available, Total    uint64
	Installed           uint64
	SwapFree, SwapTotal uint64
	FullAvg10           float64
}
