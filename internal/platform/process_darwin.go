package platform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// StartSecond is one second in Process.Start's units: microseconds of
// kinfo_proc's p_starttime. That start time is the wall clock when the
// process began, so a clock step between two processes' starts shifts one
// against the other.
const StartSecond = 1_000_000

// StartTime is a Process.Start as a wall-clock time: Start already is one,
// in microseconds since the epoch.
func StartTime(start uint64) time.Time {
	return time.UnixMicro(int64(start))
}

// darwinZombie is SZOMB, kinfo_proc's p_stat for a zombie.
const darwinZombie = 5

// darwinExeced is P_EXEC, kinfo_proc's p_flag bit "Process called exec"
// (sys/proc.h): exec_mach_imgact sets it at every exec and posix_spawn, and
// fork copies only a fixed list of other flags into the child, so a forked
// child that has not exec'd has it clear (XNU bsd/kern/kern_exec.c,
// kern_fork.c).
const darwinExeced = 0x00004000

func fromKinfo(k *unix.KinfoProc) Process {
	started := k.Proc.P_starttime
	return Process{
		PID:       int(k.Proc.P_pid),
		PPID:      int(k.Eproc.Ppid),
		PGID:      int(k.Eproc.Pgid),
		UID:       int(k.Eproc.Ucred.Uid),
		Start:     uint64(started.Sec)*1_000_000 + uint64(started.Usec),
		Name:      unix.ByteSliceToString(k.Proc.P_comm[:]),
		Zombie:    k.Proc.P_stat == darwinZombie,
		NotExeced: k.Proc.P_flag&darwinExeced == 0,
	}
}

// Processes lists every process. kern.proc.all reads them in one call, so
// none is left out for want of a read.
func Processes(unreadable func(pid int, err error)) ([]Process, error) {
	var l ProcessList
	return l.Read(unreadable)
}

// ProcessList lists every process again and again, as Processes does,
// keeping the kernel's records and the list it gives between reads, so a
// caller that lists the processes each second does not make a megabyte of
// them anew each time on a busy machine (648 bytes a process). The list Read
// gives is valid only until the next Read: a caller copies what it keeps.
// The zero value is ready to use; a ProcessList is not for concurrent use.
type ProcessList struct {
	kinfo []unix.KinfoProc
	out   []Process
}

// kernProcAll is the MIB of kern.proc.all: CTL_KERN (1), KERN_PROC (14),
// KERN_PROC_ALL (0) in sys/sysctl.h.
var kernProcAll = [3]int32{1, 14, 0}

// Read lists every process, as Processes does, into the list's buffers. A
// buffer too small for every record fails with ENOMEM, and is then sized
// again by asking the kernel how much it needs, plus an eighth for the
// processes that start meanwhile. A buffer four times larger than the
// records it last held is let go, so a build that ends does not leave its
// megabyte held.
func (l *ProcessList) Read(_ func(pid int, err error)) ([]Process, error) {
	for {
		if len(l.kinfo) == 0 {
			var size uintptr
			if err := readProcs(nil, &size); err != nil {
				return nil, err
			}
			count := int(size / unix.SizeofKinfoProc)
			l.kinfo = make([]unix.KinfoProc, max(count+count/8, 1))
		}
		size := uintptr(len(l.kinfo)) * unix.SizeofKinfoProc
		err := readProcs(&l.kinfo[0], &size)
		if errors.Is(err, unix.ENOMEM) {
			// The process table grew past the buffer.
			l.kinfo = nil
			continue
		}
		if err != nil {
			return nil, err
		}
		if size%unix.SizeofKinfoProc != 0 {
			return nil, fmt.Errorf(
				"kern.proc.all returned %d bytes, not a multiple of %d", size, unix.SizeofKinfoProc)
		}
		all := l.kinfo[:size/unix.SizeofKinfoProc]
		out := l.out[:0]
		for i := range all {
			out = append(out, fromKinfo(&all[i]))
		}
		l.out = out
		if len(all)*4 < len(l.kinfo) {
			l.kinfo, l.out = nil, nil
		}
		return out, nil
	}
}

// readProcs reads kern.proc.all into buffer, or with a nil buffer asks the size
// it needs, through libSystem's sysctl; size is the buffer's size in bytes
// and becomes the bytes written, or needed.
func readProcs(buffer *unix.KinfoProc, size *uintptr) error {
	_, _, errno := libcCall6(
		sysctl,
		uintptr(unsafe.Pointer(&kernProcAll[0])),
		uintptr(len(kernProcAll)),
		uintptr(unsafe.Pointer(buffer)),
		uintptr(unsafe.Pointer(size)),
		0,
		0,
	)
	if errno != 0 {
		return fmt.Errorf("kern.proc.all: %w", errno)
	}
	return nil
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
// it wrote as a C int, and 0 on failure with an errno libcCall6 does not
// read, so a shorter result is the error. For a process that has exited or
// is a zombie, which has no working folder left, the error wraps ESRCH, which
// Exited reports as on Linux.
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
	// Only the low 32 bits of the returned register hold the C int.
	if int32(written) < size {
		p, err := ProcessInfo(pid)
		switch {
		case err != nil:
			return "", err
		case p.Zombie:
			return "", fmt.Errorf("proc_pidinfo %d: %w", pid, unix.ESRCH)
		}
		return "", fmt.Errorf("proc_pidinfo %d: unreadable", pid)
	}
	return unix.ByteSliceToString(info[pathAt : pathAt+pathMax]), nil
}

// Args is the process's command line as the kernel counts it, nil when it
// cannot be read: the first p_argc strings of kern.procargs2. It is for
// matching and deciding, never for showing, which is ShownArgs: a process
// that rewrote its title can make its last strings environment strings.
//
// kern.procargs2 does not say where the arguments end. The kernel saves only
// their count at exec (p_argc) and the length of the whole string area, which
// holds the arguments followed at once by the environment strings, and it
// reads the area from the process's own memory as it is now. A process that
// writes its title over its arguments, as /usr/bin/perl does for $0 by padding
// with spaces up to the last NUL, leaves fewer NUL-separated strings there, so
// the first p_argc strings run on into the environment: the kernel counts
// them the same way when it cuts the environment of a restricted program, so
// the strings it returns end with environment strings too.
func Args(pid int) []string {
	args, _ := procArgs(pid)
	return args
}

// ShownArgs is the process's command line for showing to a person: Args
// without any string that might be an environment string, and cut, true when
// it left something out, so a view can mark the line as shortened. No parse
// can tell an environment string that a rewritten title let in (see Args)
// from an argument, and the environment follows the arguments, so what runs
// into it is always at the end: ShownArgs drops the trailing strings shaped
// like NAME=value and, when it drops any, keeps only the first argument,
// since the area may have been rewritten and what lay between may be the tail
// of an environment value. The cost: a real trailing argument of that shape
// cuts the line to its program, so `make CC=clang` shows as `make` and
// `bash -lc 'CI=1 npm test'` as `bash`. The case left: a process that writes
// its title on past its arguments into its environment without clearing the
// rest leaves the tail of a value, which shows when it is the last of the
// p_argc strings.
func ShownArgs(pid int) (args []string, cut bool) {
	return withoutEnvironment(Args(pid))
}

// withoutEnvironment is args without the trailing strings that may be
// environment strings, and with only its first argument when it had any, and
// whether it left anything out.
func withoutEnvironment(args []string) ([]string, bool) {
	keep := len(args)
	for keep > 0 && environmentShaped(args[keep-1]) {
		keep--
	}
	switch {
	case keep == len(args):
		return args, false
	case keep == 0:
		return nil, true
	}
	return args[:1], true
}

// environmentShaped reports whether s has the shape of an environment string:
// a name before its first "=" that is not empty, does not start with "-" and
// holds no space, quote or "/", which no option or path argument has before
// an "=".
func environmentShaped(s string) bool {
	name, _, ok := strings.Cut(s, "=")
	return ok && name != "" && !strings.HasPrefix(name, "-") &&
		!strings.ContainsAny(name, " \t\"'/")
}

// Environ is the variable name in the environment the process started with.
// It is "" when absent or unreadable.
func Environ(pid int, name string) string {
	return ReadEnvironment(pid, name).Value(name)
}

// ReadEnvironment reads the named variables of the environment the process
// started with, once: the environment strings that follow the first p_argc
// strings in kern.procargs2, up to the empty string that ends them. Readable
// is false when the kernel gives no environment at all, as it does for a
// restricted program such as /bin/sleep or /bin/zsh, or when the process
// cannot be read.
//
// A process that rewrote its title can leave fewer strings in its argument
// area than p_argc (see Args), so the first environment strings are counted
// as arguments and are missing from the environment proper. Those are kept
// apart, from the trailing strings of the first p_argc that are shaped like
// variables (see environmentShaped), and only Narrowing gives them.
func ReadEnvironment(pid int, names ...string) Environment {
	args, rest := procArgs(pid)
	e := Environment{values: map[string]string{}, inArgs: map[string]string{}}
	for len(rest) > 0 {
		end := bytes.IndexByte(rest, 0)
		if end <= 0 {
			break
		}
		e.Readable = true
		keepNamed(e.values, string(rest[:end]), names)
		rest = rest[end+1:]
	}
	first := len(args)
	for first > 0 && environmentShaped(args[first-1]) {
		first--
	}
	for _, arg := range args[first:] {
		keepNamed(e.inArgs, arg, names)
	}
	return e
}

// keepNamed keeps entry, a NAME=value string, in values when its name is
// among names and not kept yet: the first of a name counts, as for getenv.
func keepNamed(values map[string]string, entry string, names []string) {
	name, value, ok := strings.Cut(entry, "=")
	if !ok || !slices.Contains(names, name) {
		return
	}
	if _, seen := values[name]; !seen {
		values[name] = value
	}
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
// or a followed one forks or execs, through kqueue's EVFILT_PROC. Any process
// the caller can see may be watched and followed: the kernel checks
// permission only for NOTE_EXITSTATUS, which is never asked for. Without a
// kqueue it just sleeps.
type Waiter struct {
	kq int
	// watched holds what was asked for each process the kqueue has a knote
	// for: watching (Watch) and following (Follow).
	watched map[int]watch
	events  []unix.Kevent_t
	// exited holds the exits of watched processes that Follow or Unfollow
	// found gone, for the next Wait to report.
	exited []Event
}

type watch struct{ exit, follow bool }

// NewWaiter opens the kqueue a Waiter waits on.
func NewWaiter() *Waiter {
	kq, err := unix.Kqueue()
	if err != nil {
		kq = -1
	}
	return &Waiter{kq: kq, watched: map[int]watch{}, events: make([]unix.Kevent_t, 64)}
}

// Watch makes Wait return when pid exits. A followed process is already
// watched.
func (w *Waiter) Watch(pid int) {
	now, ok := w.watched[pid]
	if w.kq < 0 || ok {
		if ok && !now.exit {
			now.exit = true
			w.watched[pid] = now
		}
		return
	}
	if w.register(pid, unix.EV_ADD|unix.EV_ONESHOT, unix.NOTE_EXIT) {
		w.watched[pid] = watch{exit: true}
	}
}

// Follow makes Wait return when pid forks, execs or exits, until Unfollow or
// its exit, after which the kernel drops the watch itself. It reports false
// when the kernel refuses, as it does for a process that is gone, or when
// the system cannot follow forks (FollowsForks). The knote is kept after each
// event and cleared when read (EV_CLEAR), so forks between two Waits come back
// as one Event.
//
// Following a watched process replaces its one-shot exit knote. kqueue keys a
// knote by process and filter, and EV_ADD on that pair changes the events it
// reports but keeps EV_ONESHOT (measured: the knote was gone after its first
// event), so the old knote is deleted and a new one added. A process that
// exits in between, or whose exit was waiting to be read, refuses the new
// knote; the next Wait then reports that exit itself, so a watched exit is
// never lost.
func (w *Waiter) Follow(pid int) bool {
	now, ok := w.watched[pid]
	if w.kq < 0 {
		return false
	}
	if now.follow {
		return true
	}
	if ok {
		w.register(pid, unix.EV_DELETE, 0)
		delete(w.watched, pid)
	}
	if !w.register(pid, unix.EV_ADD|unix.EV_CLEAR, unix.NOTE_FORK|unix.NOTE_EXEC|unix.NOTE_EXIT) {
		if ok {
			w.exited = append(w.exited, Event{PID: pid, Exit: true})
		}
		return false
	}
	w.watched[pid] = watch{exit: ok && now.exit, follow: true}
	return true
}

// Unfollow stops following pid. A process also watched stays watched for its
// exit; any other has its knote removed.
func (w *Waiter) Unfollow(pid int) {
	now, ok := w.watched[pid]
	if !ok || !now.follow {
		return
	}
	if now.exit {
		// Changing the knote in place leaves no gap for an exit to fall
		// into; it stays EV_CLEAR, which the exit drops like a one-shot.
		if w.register(pid, unix.EV_ADD|unix.EV_CLEAR, unix.NOTE_EXIT) {
			w.watched[pid] = watch{exit: true}
			return
		}
		w.exited = append(w.exited, Event{PID: pid, Exit: true})
	}
	// Removing a knote the kernel already dropped at the exit fails with
	// ENOENT, which leaves nothing to undo.
	w.register(pid, unix.EV_DELETE, 0)
	delete(w.watched, pid)
}

// register applies one change to pid's EVFILT_PROC knote and reports whether
// the kernel took it.
func (w *Waiter) register(pid int, flags int, fflags uint32) bool {
	var event unix.Kevent_t
	unix.SetKevent(&event, pid, unix.EVFILT_PROC, flags)
	event.Fflags = fflags
	_, err := unix.Kevent(w.kq, []unix.Kevent_t{event}, nil, nil)
	return err == nil
}

// Wait sleeps for d or until a watched process exits or a followed one forks
// or execs, and returns what it woke for, nil when d ran out. An exit ends
// the process's watch and follow: the kernel posts it with EV_EOF and drops
// the knote, one-shot or not.
func (w *Waiter) Wait(d time.Duration) []Event {
	if w.kq < 0 {
		time.Sleep(d)
		return nil
	}
	// An exit Follow or Unfollow learned of is reported at once, with
	// whatever else is ready.
	out := w.exited
	w.exited = nil
	if len(out) > 0 {
		d = 0
	}
	timeout := unix.NsecToTimespec(d.Nanoseconds())
	n, err := unix.Kevent(w.kq, nil, w.events, &timeout)
	if err != nil || n <= 0 {
		return out
	}
	for _, event := range w.events[:n] {
		pid := int(event.Ident)
		e := Event{
			PID:  pid,
			Fork: event.Fflags&unix.NOTE_FORK != 0,
			Exec: event.Fflags&unix.NOTE_EXEC != 0,
			Exit: event.Fflags&unix.NOTE_EXIT != 0 || event.Flags&unix.EV_EOF != 0,
		}
		if e.Exit {
			delete(w.watched, pid)
		}
		out = append(out, e)
	}
	return out
}

// Children is the PIDs of pid's children now, zombies included, from
// libproc's proc_listchildpids, which answers for any process without
// privileges and returns a count of PIDs (it divides proc_listpids'
// PROC_PPID_ONLY byte count by the size of one). It is one kernel walk of
// the process list that copies out only the matches, cheaper and quicker
// than kern.proc.all filtered by parent, so a child that forks and exits at
// once is more often still there. A full buffer is read again with twice the
// room. It is nil when pid has no children or cannot be read.
func Children(pid int) []int {
	for room := 64; room <= 1<<16; room *= 2 {
		buffer := make([]int32, room)
		count, _, errno := libcCall6(
			procListChildPids,
			uintptr(pid),
			uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(room*4),
			0,
			0,
			0,
		)
		// Only the low 32 bits of the returned register hold the C int.
		n := int(int32(count))
		if errno != 0 || n <= 0 {
			return nil
		}
		if n < room {
			out := make([]int, n)
			for i, child := range buffer[:n] {
				out[i] = int(child)
			}
			return out
		}
	}
	return nil
}

// Close releases the kqueue.
func (w *Waiter) Close() {
	if w.kq >= 0 {
		_ = unix.Close(w.kq)
	}
}
