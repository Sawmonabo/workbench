//go:build linux

package guard

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// startSlack is one second in proc.Start's units: clock ticks since boot
// (USER_HZ, 100 on every architecture Workbench ships for), the unit of
// /proc/<pid>/stat's starttime. Ticks since boot never follow a wall-clock
// step, such as WSL2's resync after the host sleeps.
const startSlack = 100

// processInfo parses /proc/<pid>/stat. The name sits between the first "("
// and the last ")", so a name holding either cannot shift the fields after
// it: state (field 3), ppid (4), pgrp (5) and starttime (22).
func processInfo(pid int) (proc, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return proc{}, err
	}
	open, end := bytes.IndexByte(data, '('), bytes.LastIndexByte(data, ')')
	if open < 0 || end < open || end+2 > len(data) {
		return proc{}, fmt.Errorf("/proc/%d/stat: unreadable", pid)
	}
	fields := strings.Fields(string(data[end+2:]))
	if len(fields) < 20 {
		return proc{}, fmt.Errorf("/proc/%d/stat: short", pid)
	}
	ppid, err1 := strconv.Atoi(fields[1])
	pgid, err2 := strconv.Atoi(fields[2])
	start, err3 := strconv.ParseUint(fields[19], 10, 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return proc{}, fmt.Errorf("/proc/%d/stat: %w", pid, err)
	}
	return proc{
		PID:    pid,
		PPID:   ppid,
		PGID:   pgid,
		Start:  start,
		Name:   string(data[open+1 : end]),
		Zombie: fields[0] == "Z",
	}, nil
}

// snapshotNow lists every process. One whose stat cannot be read is left
// out: it exited (ENOENT, ESRCH) or, for any other error, it is passed to
// unreadable.
func snapshotNow(unreadable func(pid int, err error)) (snapshot, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	s := make(snapshot, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		p, err := processInfo(pid)
		switch {
		case err == nil:
			s[pid] = p
		case !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, unix.ESRCH):
			unreadable(pid, err)
		}
	}
	return s, nil
}

func readArgs(pid int) []string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}

// readCallID is CODEX_TOOL_CALL_ID in /proc/<pid>/environ, the environment
// the process started with.
func readCallID(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return ""
	}
	for entry := range strings.SplitSeq(string(data), "\x00") {
		if id, ok := strings.CutPrefix(entry, callIDVariable+"="); ok {
			return id
		}
	}
	return ""
}

// readMemory reads /proc/meminfo and, when the kernel has it,
// /proc/pressure/memory. When this process's cgroup is confined to less
// memory (see confined), it reads that cgroup instead if it calls for more,
// or for as much on a reading that acts at once where the machine's must
// hold (soft), or for as much as firmly with less available.
func readMemory() (memory, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return memory{}, err
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
			return memory{}, errors.New("/proc/meminfo has no " + name)
		}
	}
	m := memory{
		Available: values["MemAvailable"],
		Total:     values["MemAvailable"] + values["AnonPages"],
		Installed: values["MemTotal"],
		SwapFree:  values["SwapFree"],
		SwapTotal: values["SwapTotal"],
		FullAvg10: fullAvg10("/proc/pressure/memory"),
	}
	if group, ok := confined(m); ok {
		groupAct, groupSoft := decide(group)
		machineAct, machineSoft := decide(m)
		if groupAct > machineAct || groupAct == machineAct && (machineSoft && !groupSoft ||
			machineSoft == groupSoft && group.availablePercent() <= m.availablePercent()) {
			return group, nil
		}
	}
	return m, nil
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

// confined is the memory of the cgroup v2 at or above this process with the
// lowest memory.max, as a container's --memory or a unit's MemoryMax= sets
// it: the kernel's OOM killer acts there before the machine runs short.
// Available is the limit less what the cgroup holds beyond inactive file
// cache; Total adds its anonymous memory (earlyoom's figure within the
// cgroup); the swap is the cgroup's when memory.swap.max is lower than the
// machine's. ok is false without cgroup v2 or such a limit, or when the limit
// is no lower than the machine's memory.
func confined(machine memory) (memory, bool) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return memory{}, false
	}
	var path string
	var found bool
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "0::"); ok {
			path, found = rest, true
		}
	}
	if !found {
		return memory{}, false
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
		return memory{}, false
	}
	current, err := cgroupNumber(filepath.Join(dir, "memory.current"))
	if err != nil {
		return memory{}, false
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
	m := memory{
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

// footprint is the process's proportional set size (Pss), which counts a
// shared page once across the processes sharing it.
func footprint(pid int) (uint64, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/smaps_rollup")
	if err != nil {
		return 0, err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "Pss:"); ok {
			value := strings.TrimSuffix(strings.TrimSpace(rest), " kB")
			kib, err := strconv.ParseUint(value, 10, 64)
			return kib * 1024, err
		}
	}
	return 0, errors.New("smaps_rollup has no Pss line")
}

// send signals t once its start time still matches the snapshot, it is not a
// zombie and, for a group, it still leads that group, so it reports true only
// for a live process. It goes through a pidfd, which never reaches a later
// process that reuses the PID; a group through the leader's pidfd with
// PIDFD_SIGNAL_PROCESS_GROUP (Linux 6.9), else with kill(-pgid). The pidfd is
// a refinement, not a requirement: when pidfd_open fails for any reason but
// ESRCH (no such process), such as ENOMEM or EMFILE near out-of-memory, it
// signals by PID after the same checks, as earlyoom does without a pidfd.
// After SIGKILL through a pidfd, process_mrelease frees the memory at once,
// as earlyoom does; a kernel without it just frees it at exit.
func send(t target, sig unix.Signal) bool {
	fd, err := unix.PidfdOpen(t.PID, 0)
	if errors.Is(err, unix.ESRCH) {
		return false
	}
	havePidfd := err == nil
	if havePidfd {
		defer func() { _ = unix.Close(fd) }()
	}
	if p, err := processInfo(t.PID); err != nil || p.Start != t.Start || p.Zombie ||
		t.Group && p.PGID != t.PID {
		return false
	}
	switch {
	case t.Group && havePidfd:
		err = unix.PidfdSendSignal(fd, sig, nil, unix.PIDFD_SIGNAL_PROCESS_GROUP)
		if errors.Is(err, unix.EINVAL) {
			err = unix.Kill(-t.PID, sig)
		}
	case t.Group:
		err = unix.Kill(-t.PID, sig)
	case havePidfd:
		err = unix.PidfdSendSignal(fd, sig, nil, 0)
	default:
		err = unix.Kill(t.PID, sig)
	}
	if err != nil {
		return false
	}
	if havePidfd && sig == unix.SIGKILL {
		_, _, _ = unix.Syscall(unix.SYS_PROCESS_MRELEASE, uintptr(fd), 0, 0)
	}
	return true
}

// waiter sleeps between ticks and wakes as soon as a watched agent exits,
// by polling the agents' pidfds. Without a pidfd it just sleeps.
type waiter struct{ fds map[int]int }

func newWaiter() *waiter { return &waiter{fds: map[int]int{}} }

func (w *waiter) watch(pid int) {
	if _, ok := w.fds[pid]; ok {
		return
	}
	if fd, err := unix.PidfdOpen(pid, 0); err == nil {
		w.fds[pid] = fd
	}
}

func (w *waiter) wait(d time.Duration) {
	if len(w.fds) == 0 {
		time.Sleep(d)
		return
	}
	polls := make([]unix.PollFd, 0, len(w.fds))
	pids := make([]int, 0, len(w.fds))
	for pid, fd := range w.fds {
		polls = append(polls, unix.PollFd{Fd: int32(fd), Events: unix.POLLIN})
		pids = append(pids, pid)
	}
	if _, err := unix.Poll(polls, int(d.Milliseconds())); err != nil {
		if !errors.Is(err, unix.EINTR) {
			time.Sleep(d)
		}
		return
	}
	for i, poll := range polls {
		if poll.Revents != 0 {
			_ = unix.Close(int(poll.Fd))
			delete(w.fds, pids[i])
		}
	}
}

func (w *waiter) close() {
	for _, fd := range w.fds {
		_ = unix.Close(fd)
	}
}
