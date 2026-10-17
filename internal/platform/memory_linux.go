package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MachineMemory reads /proc/meminfo and, when the kernel has it,
// /proc/pressure/memory.
func MachineMemory() (Memory, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return Memory{}, err
	}
	values := map[string]uint64{}
	for line := range strings.SplitSeq(string(data), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if fields := strings.Fields(rest); ok && len(fields) > 0 {
			if kib, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
				values[name] = kib * 1024
			}
		}
	}
	for _, name := range []string{"MemTotal", "MemAvailable", "AnonPages", "SwapTotal", "SwapFree"} {
		if _, ok := values[name]; !ok {
			return Memory{}, errors.New("/proc/meminfo has no " + name)
		}
	}
	return Memory{
		Available: values["MemAvailable"],
		Total:     values["MemAvailable"] + values["AnonPages"],
		Installed: values["MemTotal"],
		SwapFree:  values["SwapFree"],
		SwapTotal: values["SwapTotal"],
		FullAvg10: fullAvg10("/proc/pressure/memory"),
	}, nil
}

// cgroupRoot is where cgroup v2 is mounted.
const cgroupRoot = "/sys/fs/cgroup"

// fullAvg10 is the full avg10 of a pressure file, -1 when absent.
func fullAvg10(path string) float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "full "); ok {
			for field := range strings.FieldsSeq(rest) {
				if value, ok := strings.CutPrefix(field, "avg10="); ok {
					if v, err := strconv.ParseFloat(value, 64); err == nil {
						return v
					}
				}
			}
		}
	}
	return -1
}

// cgroupNumber reads a one-number cgroup file; "max" is an error.
func cgroupNumber(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}

// CgroupMemory is the memory of the cgroup v2 at or above this process with
// the lowest memory.max, as a container's --memory or a unit's MemoryMax=
// sets it: the kernel's OOM killer acts there before the machine runs short.
// Available is the limit less what the cgroup holds beyond inactive file
// cache; Total adds its anonymous memory (earlyoom's figure within the
// cgroup); the swap is the cgroup's when memory.swap.max is lower than the
// machine's. ok is false without cgroup v2 or such a limit, or when the limit
// is no lower than the machine's memory.
func CgroupMemory(machine Memory) (Memory, bool) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return Memory{}, false
	}
	var path string
	var found bool
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "0::"); ok {
			path, found = rest, true
		}
	}
	if !found {
		return Memory{}, false
	}
	var dir string
	var limit uint64
	for d := filepath.Join(cgroupRoot, path); strings.HasPrefix(d, cgroupRoot); d = filepath.Dir(d) {
		if v, err := cgroupNumber(
			filepath.Join(d, "memory.max"),
		); err == nil &&
			(dir == "" || v < limit) {
			dir, limit = d, v
		}
		if d == cgroupRoot {
			break
		}
	}
	if dir == "" || limit >= machine.Installed {
		return Memory{}, false
	}
	current, err := cgroupNumber(filepath.Join(dir, "memory.current"))
	if err != nil {
		return Memory{}, false
	}
	stat := map[string]uint64{}
	if data, err := os.ReadFile(filepath.Join(dir, "memory.stat")); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if name, value, ok := strings.Cut(line, " "); ok {
				if v, err := strconv.ParseUint(value, 10, 64); err == nil {
					stat[name] = v
				}
			}
		}
	}
	held := current - min(current, stat["inactive_file"])
	available := limit - min(limit, held)
	m := Memory{
		Available: available, Total: available + stat["anon"], Installed: limit,
		SwapTotal: machine.SwapTotal, SwapFree: machine.SwapFree,
		FullAvg10: fullAvg10(filepath.Join(dir, "memory.pressure")),
	}
	if swapMax, err := cgroupNumber(
		filepath.Join(dir, "memory.swap.max"),
	); err == nil &&
		swapMax < machine.SwapTotal {
		swapUsed, _ := cgroupNumber(filepath.Join(dir, "memory.swap.current"))
		m.SwapTotal = swapMax
		m.SwapFree = swapMax - min(swapMax, swapUsed)
	}
	return m, true
}
