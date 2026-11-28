package display

// This file is byte sizes, by the two rules the package comment states:
// memory, and the data the guard keeps, in binary units; downloads in
// decimal ones.

import (
	"fmt"
	"strings"
)

// MemoryMBLimit is where a memory size moves from MB to GB: half a MB under
// 1 GB, so a size that rounds to 1024 MB prints in GB.
const MemoryMBLimit = 1<<30 - 1<<19

// Memory is a measurement, a reading that moves (memory used or available, a
// command's size and peak, swap in use), as the stop reason, the log viewer
// and the live view show it: whole MB under 1 GB, else GB with two decimals
// ("289 MB", "1.52 GB"). GB and MB are binary, as Activity Monitor counts them.
func Memory(bytes uint64) string {
	if bytes < MemoryMBLimit {
		return fmt.Sprintf("%.0f MB", float64(bytes)/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", float64(bytes)/(1<<30))
}

// MemoryCapacity is a fixed total (installed memory, swap total, the stop
// floor, a cgroup limit): whole MB under 1 GB, else whole GB when the value is
// whole at two decimals and two decimals otherwise ("256 MB", "16 GB",
// "1.86 GB").
func MemoryCapacity(bytes uint64) string {
	if bytes < MemoryMBLimit {
		return Memory(bytes)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.2f", float64(bytes)/(1<<30)), ".00") + " GB"
}

// DataSize is a size of text or of a file as the guard shows memory (see
// [Memory]: binary MB and GB, as Activity Monitor counts them), and under
// a MB in whole KB of 1,024 bytes, rounded up, so a size is never 0 KB
// ("37 KB", "46 KB", "3 MB").
func DataSize(bytes int64) string {
	if kb := (max(bytes, 0) + 1023) / 1024; kb < 1024 {
		return Number(kb) + " KB"
	}
	return Memory(uint64(bytes))
}

// Transfer is done of total bytes of a download in decimal megabytes with one
// decimal, or in whole kilobytes for a total under a megabyte ("12.3/100.0
// MB", "512 kB"), as Finder shows sizes.
func Transfer(done, total int64) string {
	unit, scale, format := "MB", 1e6, "%.1f"
	if total < 1e6 {
		unit, scale, format = "kB", 1e3, "%.0f"
	}
	amount := func(n int64) string { return fmt.Sprintf(format, float64(n)/scale) }
	if done == total {
		return amount(total) + " " + unit
	}
	return amount(done) + "/" + amount(total) + " " + unit
}
