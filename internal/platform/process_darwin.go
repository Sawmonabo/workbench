package platform

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// StartSecond is one second in Process.Start's units: microseconds of
// kinfo_proc's p_starttime. That start time is the wall clock when the
// process began, which the kernel records for the pre hook and for the
// command alike, so only a clock step between the two can shift them.
const StartSecond = 1_000_000

// darwinZombie is SZOMB, kinfo_proc's p_stat for a zombie.
const darwinZombie = 5

func fromKinfo(k *unix.KinfoProc) Process {
	started := k.Proc.P_starttime
	return Process{
		PID:    int(k.Proc.P_pid),
		PPID:   int(k.Eproc.Ppid),
		PGID:   int(k.Eproc.Pgid),
		Start:  uint64(started.Sec)*1_000_000 + uint64(started.Usec),
		Name:   unix.ByteSliceToString(k.Proc.P_comm[:]),
		Zombie: k.Proc.P_stat == darwinZombie,
	}
}

// Processes lists every process. kern.proc.all reads them in one call, so
// none is left out for want of a read.
func Processes(_ func(pid int, err error)) ([]Process, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	out := make([]Process, len(all))
	for i := range all {
		out[i] = fromKinfo(&all[i])
	}
	return out, nil
}

// ProcessInfo reads one process through kern.proc.pid. For a PID no process
// has, the sysctl returns no record and no error, because the kernel's
// sysctl_prochandle walks the processes without reporting whether one
// matched, so that case wraps ESRCH, which Exited reports as on Linux.
func ProcessInfo(pid int) (Process, error) {
	found, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		return Process{}, fmt.Errorf("kern.proc.pid %d: %w", pid, err)
	}
	if len(found) == 0 {
		return Process{}, fmt.Errorf("kern.proc.pid %d: %w", pid, unix.ESRCH)
	}
	return fromKinfo(&found[0]), nil
}

// Folder is the process's working folder as the kernel reports it, a real
// path: proc_pidinfo with PROC_PIDVNODEPATHINFO (9) fills the 2352-byte
// proc_vnodepathinfo, whose pvi_cdir.vip_path is the NUL-terminated path at
// byte 152, at most MAXPATHLEN (1024) bytes. proc_pidinfo returns the bytes
// it wrote, and 0 on failure with an errno libcCall6 does not read, so a
// shorter result is the error: ProcessInfo's when the process has exited,
// which Exited reports.
func Folder(pid int) (string, error) {
	const (
		vnodePathInfo = 9
		size          = 2352
		pathAt        = 152
		pathMax       = 1024
	)
	var info [size]byte
	written, _, _ := libcCall6(
		procPidinfo,
		uintptr(pid),
		vnodePathInfo,
		0,
		uintptr(unsafe.Pointer(&info[0])),
		size,
		0,
	)
	if int(written) < size {
		if _, err := ProcessInfo(pid); err != nil {
			return "", err
		}
		return "", fmt.Errorf("proc_pidinfo %d: unreadable", pid)
	}
	return unix.ByteSliceToString(info[pathAt : pathAt+pathMax]), nil
}

// Args is the process's command line, nil when it cannot be read.
func Args(pid int) []string {
	args, _ := procArgs(pid)
	return args
}

// Environ is the variable name among the environment strings that follow
// the arguments in kern.procargs2, up to the empty string that ends them:
// the environment the process started with. It is "" when absent or
// unreadable.
func Environ(pid int, name string) string {
	_, rest := procArgs(pid)
	for len(rest) > 0 {
		end := bytes.IndexByte(rest, 0)
		if end <= 0 {
			return ""
		}
		if value, ok := strings.CutPrefix(string(rest[:end]), name+"="); ok {
			return value
		}
		rest = rest[end+1:]
	}
	return ""
}

// procArgs reads kern.procargs2: argc as a 32-bit integer, the executable
// path, NUL padding, then argc NUL-terminated arguments, and returns those
// arguments and what follows them.
func procArgs(pid int) ([]string, []byte) {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(data) < 4 {
		return nil, nil
	}
	argc := int(binary.LittleEndian.Uint32(data[:4]))
	rest := data[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil, nil
	}
	rest = bytes.TrimLeft(rest[end:], "\x00")
	args := make([]string, 0, argc)
	for len(args) < argc && len(rest) > 0 {
		end = bytes.IndexByte(rest, 0)
		if end < 0 {
			return append(args, string(rest)), nil
		}
		args = append(args, string(rest[:end]))
		rest = rest[end+1:]
	}
	return args, rest
}

// Resident is the memory the process holds for a live display. macOS has no
// cheaper figure worth showing beside the physical footprint, so it is that
// footprint again.
func Resident(pid int) (uint64, error) {
	return Footprint(pid)
}

// Signal sends sig to p, or with group to the process group p leads, once
// p's start time still matches; a process that exited, is a zombie or whose
// PID was reused is left alone, so it reports true only for a live process.
// Only p's PID and Start are read.
func Signal(p Process, group bool, sig unix.Signal) bool {
	now, err := ProcessInfo(p.PID)
	if err != nil || now.Start != p.Start || now.Zombie {
		return false
	}
	pid := p.PID
	if group {
		pid = -pid
	}
	return unix.Kill(pid, sig) == nil
}

// Waiter sleeps between ticks and wakes as soon as a watched process exits,
// through kqueue's EVFILT_PROC NOTE_EXIT. Without a kqueue it just sleeps.
type Waiter struct {
	kq      int
	watched map[int]bool
}

// NewWaiter opens the kqueue a Waiter waits on.
func NewWaiter() *Waiter {
	kq, err := unix.Kqueue()
	if err != nil {
		kq = -1
	}
	return &Waiter{kq: kq, watched: map[int]bool{}}
}

// Watch makes Wait return when pid exits.
func (w *Waiter) Watch(pid int) {
	if w.kq < 0 || w.watched[pid] {
		return
	}
	var event unix.Kevent_t
	unix.SetKevent(&event, pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	event.Fflags = unix.NOTE_EXIT
	if _, err := unix.Kevent(w.kq, []unix.Kevent_t{event}, nil, nil); err == nil {
		w.watched[pid] = true
	}
}

// Wait sleeps for d or until a watched process exits.
func (w *Waiter) Wait(d time.Duration) {
	if w.kq < 0 {
		time.Sleep(d)
		return
	}
	events := make([]unix.Kevent_t, 8)
	timeout := unix.NsecToTimespec(d.Nanoseconds())
	n, err := unix.Kevent(w.kq, nil, events, &timeout)
	if err != nil {
		return
	}
	for _, event := range events[:n] {
		delete(w.watched, int(event.Ident))
	}
}

// Close releases the kqueue.
func (w *Waiter) Close() {
	if w.kq >= 0 {
		_ = unix.Close(w.kq)
	}
}
