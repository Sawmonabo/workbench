package platform

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// StartSecond is one second in Process.Start's units: clock ticks since boot
// (USER_HZ, 100 on every architecture Workbench ships for), the unit of
// /proc/<pid>/stat's starttime. Ticks since boot never follow a wall-clock
// step, such as WSL2's resync after the host sleeps.
const StartSecond = 100

// ProcessInfo parses /proc/<pid>/stat. The name sits between the first "("
// and the last ")", so a name holding either cannot shift the fields after
// it: state (field 3), ppid (4), pgrp (5) and starttime (22).
func ProcessInfo(pid int) (Process, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return Process{}, err
	}
	open, end := bytes.IndexByte(data, '('), bytes.LastIndexByte(data, ')')
	if open < 0 || end < open || end+2 > len(data) {
		return Process{}, fmt.Errorf("/proc/%d/stat: unreadable", pid)
	}
	fields := strings.Fields(string(data[end+2:]))
	if len(fields) < 20 {
		return Process{}, fmt.Errorf("/proc/%d/stat: short", pid)
	}
	ppid, err1 := strconv.Atoi(fields[1])
	pgid, err2 := strconv.Atoi(fields[2])
	start, err3 := strconv.ParseUint(fields[19], 10, 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return Process{}, fmt.Errorf("/proc/%d/stat: %w", pid, err)
	}
	return Process{
		PID:    pid,
		PPID:   ppid,
		PGID:   pgid,
		Start:  start,
		Name:   string(data[open+1 : end]),
		Zombie: fields[0] == "Z",
	}, nil
}

// Folder is the process's working folder as the kernel reports it, a real
// path: the target of /proc/<pid>/cwd.
func Folder(pid int) (string, error) {
	return os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
}

// Processes lists every process. One whose stat cannot be read is left
// out: it exited (ENOENT, ESRCH) or, for any other error, it is passed to
// unreadable.
func Processes(unreadable func(pid int, err error)) ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	out := make([]Process, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		p, err := ProcessInfo(pid)
		switch {
		case err == nil:
			out = append(out, p)
		case !Exited(err):
			unreadable(pid, err)
		}
	}
	return out, nil
}

// Args is the process's command line from /proc/<pid>/cmdline, nil when it
// cannot be read.
func Args(pid int) []string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}

// Environ is the variable name in /proc/<pid>/environ, the environment the
// process started with. It is "" when absent or unreadable.
func Environ(pid int, name string) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return ""
	}
	for entry := range strings.SplitSeq(string(data), "\x00") {
		if value, ok := strings.CutPrefix(entry, name+"="); ok {
			return value
		}
	}
	return ""
}

// Footprint is the process's proportional set size (Pss), which counts a
// shared page once across the processes sharing it.
func Footprint(pid int) (uint64, error) {
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

// Resident is the process's resident set size for a live display: the
// second field of /proc/<pid>/statm, in pages. The kernel counts it
// approximately, which is fine for a display; the stop decision reads
// Footprint's Pss instead.
func Resident(pid int) (uint64, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/statm")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, fmt.Errorf("/proc/%d/statm: short", pid)
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("/proc/%d/statm: %w", pid, err)
	}
	return pages * uint64(os.Getpagesize()), nil
}

// Signal sends sig to p, or with group to the process group p leads, once
// p's start time still matches, it is not a zombie and, for a group, it
// still leads that group, so it reports true only for a live process. Only
// p's PID and Start are read. It goes through a pidfd, which never reaches a
// later process that reuses the PID; a group through the leader's pidfd with
// PIDFD_SIGNAL_PROCESS_GROUP (Linux 6.9), else with kill(-pgid). The pidfd is
// a refinement, not a requirement: when pidfd_open fails for any reason but
// ESRCH (no such process), such as ENOMEM or EMFILE near out-of-memory, it
// signals by PID after the same checks, as earlyoom does without a pidfd.
// After SIGKILL through a pidfd, process_mrelease frees the memory at once,
// as earlyoom does; a kernel without it just frees it at exit.
func Signal(p Process, group bool, sig unix.Signal) bool {
	fd, err := unix.PidfdOpen(p.PID, 0)
	if errors.Is(err, unix.ESRCH) {
		return false
	}
	havePidfd := err == nil
	if havePidfd {
		defer func() { _ = unix.Close(fd) }()
	}
	if now, err := ProcessInfo(p.PID); err != nil || now.Start != p.Start || now.Zombie ||
		group && now.PGID != p.PID {
		return false
	}
	switch {
	case group && havePidfd:
		err = unix.PidfdSendSignal(fd, sig, nil, unix.PIDFD_SIGNAL_PROCESS_GROUP)
		if errors.Is(err, unix.EINVAL) {
			err = unix.Kill(-p.PID, sig)
		}
	case group:
		err = unix.Kill(-p.PID, sig)
	case havePidfd:
		err = unix.PidfdSendSignal(fd, sig, nil, 0)
	default:
		err = unix.Kill(p.PID, sig)
	}
	if err != nil {
		return false
	}
	if havePidfd && sig == unix.SIGKILL {
		_, _, _ = unix.Syscall(unix.SYS_PROCESS_MRELEASE, uintptr(fd), 0, 0)
	}
	return true
}

// Waiter sleeps between ticks and wakes as soon as a watched process exits,
// by polling the processes' pidfds. Without a pidfd it just sleeps.
type Waiter struct{ fds map[int]int }

// NewWaiter makes a Waiter that watches nothing yet.
func NewWaiter() *Waiter { return &Waiter{fds: map[int]int{}} }

// Watch makes Wait return when pid exits.
func (w *Waiter) Watch(pid int) {
	if _, ok := w.fds[pid]; ok {
		return
	}
	if fd, err := unix.PidfdOpen(pid, 0); err == nil {
		w.fds[pid] = fd
	}
}

// Wait sleeps for d or until a watched process exits.
func (w *Waiter) Wait(d time.Duration) {
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

// Close releases the pidfds still watched.
func (w *Waiter) Close() {
	for _, fd := range w.fds {
		_ = unix.Close(fd)
	}
}
