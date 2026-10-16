//go:build linux

package guard

import (
	"bytes"
	"errors"
	"os"
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
		return proc{}, errors.New("unreadable stat")
	}
	fields := strings.Fields(string(data[end+2:]))
	if len(fields) < 20 {
		return proc{}, errors.New("short stat")
	}
	ppid, err1 := strconv.Atoi(fields[1])
	pgid, err2 := strconv.Atoi(fields[2])
	start, err3 := strconv.ParseUint(fields[19], 10, 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return proc{}, err
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

func snapshotNow() (snapshot, error) {
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
		if p, err := processInfo(pid); err == nil {
			s[pid] = p
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

// readMemory reads /proc/meminfo and, when the kernel has it,
// /proc/pressure/memory.
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
		FullAvg10: -1,
	}
	if psi, err := os.ReadFile("/proc/pressure/memory"); err == nil {
		for line := range strings.SplitSeq(string(psi), "\n") {
			if rest, ok := strings.CutPrefix(line, "full "); ok {
				for field := range strings.FieldsSeq(rest) {
					if value, ok := strings.CutPrefix(field, "avg10="); ok {
						m.FullAvg10, _ = strconv.ParseFloat(value, 64)
					}
				}
			}
		}
	}
	return m, nil
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

// send signals t once its start time still matches the snapshot and it is not
// a zombie, so it reports true only for a live process. It goes through a
// pidfd, which never reaches a later process that reuses the PID. The pidfd is
// a refinement, not a requirement: when pidfd_open fails for any reason but
// ESRCH (no such process), such as ENOMEM or EMFILE near out-of-memory, it
// signals by PID after the same start-time check, as earlyoom does without a
// pidfd. A group is signalled with kill(-pgid). After SIGKILL through a pidfd,
// process_mrelease frees the memory at once, as earlyoom does; a kernel
// without it just frees it at exit.
func send(t target, sig unix.Signal) bool {
	fd, err := unix.PidfdOpen(t.PID, 0)
	if errors.Is(err, unix.ESRCH) {
		return false
	}
	havePidfd := err == nil
	if havePidfd {
		defer func() { _ = unix.Close(fd) }()
	}
	if p, err := processInfo(t.PID); err != nil || p.Start != t.Start || p.Zombie {
		return false
	}
	switch {
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
