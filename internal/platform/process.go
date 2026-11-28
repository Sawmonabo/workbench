package platform

import (
	"errors"
	"io/fs"
	"time"

	"golang.org/x/sys/unix"
)

// Process is one process. Start is the kernel's start time in the system's
// own units (StartSecond is one second of them) and, with the PID, names the
// process: a later process reusing the PID has another Start. UID is the
// process's effective user. NotExeced marks a process forked and not exec'd
// since: it still runs its parent's program image, as the kernel itself
// records it.
type Process struct {
	PID, PPID, PGID int
	UID             int
	Start           uint64
	Name            string
	Zombie          bool
	NotExeced       bool
}

// StartSecond is one second in Process.Start's units. On macOS Start is the
// wall clock when the process began, in microseconds, so a clock step between
// two processes' starts shifts one against the other; on Linux it is clock
// ticks since boot, 100 a second, which never follow a wall-clock step.
const StartSecond = startSecond

// StartTime is a Process.Start as a wall-clock time. On Linux it is the zero
// time when the boot time cannot be read, it can be up to a second early, and
// a step of the wall clock after the boot time was first read shifts every
// result by the size of that step.
func StartTime(start uint64) time.Time { return startTime(start) }

// Processes lists every process. On Linux, which reads each process on its
// own, one that exits meanwhile is left out, and so is one that cannot be read
// for any other reason, which is passed to unreadable with its error; macOS
// reads them all in one call and never calls unreadable. The error is the
// system's when the process table cannot be read.
func Processes(unreadable func(pid int, err error)) ([]Process, error) {
	var l ProcessList
	return l.Read(unreadable)
}

// ProcessList lists every process again and again, as Processes does,
// keeping what it read between reads, so a caller that lists the processes
// each second does not make them anew each time. The list Read gives is valid
// only until the next Read: a caller copies what it keeps. The zero value is
// ready to use; a ProcessList is not for concurrent use.
type ProcessList struct{ processList }

// Read lists every process, as Processes does, into the list's buffers.
func (l *ProcessList) Read(unreadable func(pid int, err error)) ([]Process, error) {
	return l.read(unreadable)
}

// ProcessInfo reads one process. For a PID no process has, the error is one
// [Exited] reports. On Linux UID is the owner of /proc/<pid>, which reads 0
// for a process that is not dumpable (after a set-user-ID exec, or
// prctl(PR_SET_DUMPABLE, 0)).
func ProcessInfo(pid int) (Process, error) { return processInfo(pid) }

// Folder is the process's working folder as the kernel reports it, a real
// path. For a process that has exited the error is one [Exited] reports, and
// on macOS so is a zombie's, which has no working folder left.
func Folder(pid int) (string, error) { return folder(pid) }

// Args is the process's command line as the kernel gives it, nil when it
// cannot be read. It is for matching and deciding, never for showing, which
// is ShownArgs: a process that rewrote its title can make its last strings
// run on into its environment.
func Args(pid int) []string { return args(pid) }

// ShownArgs is the process's command line for showing to a person: Args
// without anything that might come from the environment, and cut, true when
// it left something out, so a view can mark the line as shortened. To be
// sure, it can leave out real arguments: on macOS the trailing strings shaped
// like NAME=value and, when there are any, every argument after the program,
// so `make CC=clang` shows as `make`; on Linux whatever lies past the
// argument area, and the whole line when the kernel hides that area's bounds
// and the line is one string.
func ShownArgs(pid int) (args []string, cut bool) { return shownArgs(pid) }

// Environment is one read of the named variables of a process's environment
// (see ReadEnvironment). Readable is false when the system gave no
// environment for the process, so its absent variables tell nothing.
type Environment struct {
	Readable bool
	// values are the variables the environment holds; inArgs those found
	// only among arguments a rewritten title let the environment run into.
	values, inArgs map[string]string
}

// Value is the variable name as the process's environment holds it, "" when
// it is absent or the environment unreadable. It is the only form for a
// variable whose presence can make a caller do more (tie, match or stop).
func (e Environment) Value(name string) string { return e.values[name] }

// Narrowing is the variable name as the environment holds it, else as one of
// the trailing NAME=value strings of the process's arguments holds it, where
// a rewritten title can leave the first environment strings (see
// ReadEnvironment); "" when neither does. It may therefore return a value the
// process was given as an argument, not in its environment, so it is only for
// a variable whose presence can only make a caller do less, such as an
// opt-out, never for one that ties, matches or widens anything.
func (e Environment) Narrowing(name string) string {
	if value, ok := e.values[name]; ok {
		return value
	}
	return e.inArgs[name]
}

// Environ is the variable name in the environment the process started with.
// It is "" when absent or unreadable.
func Environ(pid int, name string) string {
	return ReadEnvironment(pid, name).Value(name)
}

// ReadEnvironment reads the named variables of the environment the process
// started with, once. Readable is false when the system gives no environment
// for the process: it cannot be read, or the kernel gives none, as macOS does
// for a restricted program such as /bin/sleep or /bin/zsh and Linux for a
// zombie or a kernel thread. On Linux it is false too when a string there is
// not shaped like a variable, as after a program wrote its title over its
// environment. On macOS a rewritten title can leave the first variables among
// the arguments, which only Narrowing gives.
func ReadEnvironment(pid int, names ...string) Environment {
	return readEnvironment(pid, names...)
}

// Footprint is the memory the process holds, the figure a decision to stop it
// reads: on macOS its physical footprint, the figure Activity Monitor shows; on
// Linux its proportional set size (Pss), which counts a shared page once
// across the processes sharing it. The error is the system's when it cannot
// be read.
func Footprint(pid int) (uint64, error) { return footprint(pid) }

// Resident is the memory the process holds, for a live display: on Linux its
// resident set size, which the kernel counts approximately; on macOS, which
// has no cheaper figure worth showing, the physical footprint Footprint
// gives.
func Resident(pid int) (uint64, error) { return resident(pid) }

// Signal sends sig to p, or with group to the process group p leads, once
// p's start time still matches; a process that exited, is a zombie or whose
// PID was reused is left alone, so it reports true only for a live process.
// Only p's PID and Start are read. On Linux a group is signalled only while p
// still leads it, and where a pidfd can be opened the signal goes through it,
// which never reaches a later process that reuses the PID, and a SIGKILL then
// frees the process's memory at once where the kernel can.
func Signal(p Process, group bool, sig unix.Signal) bool { return signal(p, group, sig) }

// Exited reports whether err from reading a process means only that it
// exited (ENOENT or ESRCH), which is no error.
func Exited(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH)
}

// Children is the PIDs of pid's children now, zombies included, nil when it
// has none or they cannot be listed.
func Children(pid int) []int { return children(pid) }

// LiveChildren are the processes that are pid's children now, as [Children]
// lists them and [ProcessInfo] reads each, leaving out a zombie and a process
// whose parent is no longer pid when it is read (it was adopted, or its PID
// was reused meanwhile). They are in the order Children gives.
func LiveChildren(pid int) []Process {
	var kids []Process
	for _, child := range Children(pid) {
		p, err := ProcessInfo(child)
		if err == nil && !p.Zombie && p.PPID == pid {
			kids = append(kids, p)
		}
	}
	return kids
}
