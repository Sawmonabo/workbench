//go:build darwin

package guard

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// darwinZombie is SZOMB, kinfo_proc's p_stat for a zombie.
const darwinZombie = 5

// startSlack is one second in proc.Start's units: microseconds of
// kinfo_proc's p_starttime. That start time is the wall clock when the
// process began, which the kernel records for the pre hook and for the
// command alike, so only a clock step between the two can shift them.
const startSlack = 1_000_000

func fromKinfo(k *unix.KinfoProc) proc {
	started := k.Proc.P_starttime
	return proc{
		PID:    int(k.Proc.P_pid),
		PPID:   int(k.Eproc.Ppid),
		PGID:   int(k.Eproc.Pgid),
		Start:  uint64(started.Sec)*1_000_000 + uint64(started.Usec),
		Name:   unix.ByteSliceToString(k.Proc.P_comm[:]),
		Zombie: k.Proc.P_stat == darwinZombie,
	}
}

// snapshotNow lists every process. kern.proc.all reads them in one call, so
// none is left out for want of a read.
func snapshotNow(func(pid int, err error)) (snapshot, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	s := make(snapshot, len(all))
	for i := range all {
		p := fromKinfo(&all[i])
		s[p.PID] = p
	}
	return s, nil
}

func processInfo(pid int) (proc, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return proc{}, fmt.Errorf("kern.proc.pid %d: %w", pid, err)
	}
	return fromKinfo(k), nil
}

func readArgs(pid int) []string {
	args, _ := procArgs(pid)
	return args
}

// readCallID is CODEX_TOOL_CALL_ID among the environment strings that follow
// the arguments in kern.procargs2, up to the empty string that ends them.
func readCallID(pid int) string {
	_, rest := procArgs(pid)
	for len(rest) > 0 {
		end := bytes.IndexByte(rest, 0)
		if end <= 0 {
			return ""
		}
		if id, ok := strings.CutPrefix(string(rest[:end]), callIDVariable+"="); ok {
			return id
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

// readMemory reads the kernel's pressure level, the RAM and vm.swapusage
// (struct xsw_usage: total, avail, used as uint64).
func readMemory() (memory, error) {
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	if err != nil {
		return memory{}, err
	}
	installed, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return memory{}, err
	}
	m := memory{Level: int(level), Installed: installed, FullAvg10: -1}
	if swap, err := unix.SysctlRaw("vm.swapusage"); err == nil && len(swap) >= 24 {
		m.SwapTotal = binary.LittleEndian.Uint64(swap[0:8])
		m.SwapFree = binary.LittleEndian.Uint64(swap[8:16])
	}
	return m, nil
}

// footprint is the process's physical footprint, the figure Activity Monitor
// shows.
func footprint(pid int) (uint64, error) {
	return operation.PhysicalFootprint(pid)
}

// send signals t once its start time still matches the snapshot it came
// from; a process that exited, is a zombie or whose PID was reused is left
// alone, so it reports true only for a live process.
func send(t target, sig unix.Signal) bool {
	p, err := processInfo(t.PID)
	if err != nil || p.Start != t.Start || p.Zombie {
		return false
	}
	pid := t.PID
	if t.Group {
		pid = -pid
	}
	return unix.Kill(pid, sig) == nil
}

// waiter sleeps between ticks and wakes as soon as a watched agent exits,
// through kqueue's EVFILT_PROC NOTE_EXIT. Without a kqueue it just sleeps.
type waiter struct {
	kq      int
	watched map[int]bool
}

func newWaiter() *waiter {
	kq, err := unix.Kqueue()
	if err != nil {
		kq = -1
	}
	return &waiter{kq: kq, watched: map[int]bool{}}
}

func (w *waiter) watch(pid int) {
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

func (w *waiter) wait(d time.Duration) {
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

func (w *waiter) close() {
	if w.kq >= 0 {
		_ = unix.Close(w.kq)
	}
}
