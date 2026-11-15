package platform

import (
	"errors"
	"io/fs"

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

// Lineage is the kernel's record of where a process came from, where the
// system keeps one (KnowsOriginalParent): Version names the process itself
// and ParentVersion the process that forked it, as that parent's Version was
// when it forked, so it no longer equals the parent's Version once the parent
// execs. It stays recorded after that parent exits and init adopts the child.
type Lineage struct{ Version, ParentVersion uint32 }

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

// Attach is what Waiter.Watch or Waiter.Follow did for a process. Only
// Refused and Unsupported say anything lasting; Gone is a moment's answer.
type Attach int

const (
	// Attached is a process watched (or followed) now.
	Attached Attach = iota
	// Gone is a process the system no longer finds, so nothing was attached.
	// It has exited, or is exiting: on macOS the kernel stops handing out a
	// process the moment it starts to exit, while its process listing still
	// shows it running for a while (until its last thread ends). A process
	// still being made by fork is not handed out either, so a process listed
	// as a child may be Gone for a moment and attach later. Nothing is to be
	// remembered of it.
	Gone
	// Refused is a process that runs and that the system will not attach
	// this watch to. Whatever was watched of it before is watched still.
	Refused
	// Unsupported is a Waiter that can attach nothing of this kind: forks
	// cannot be followed on this system (FollowsForks), or it has nothing to
	// wait on.
	Unsupported
)

// Event is what a Waiter woke for on one process: PID forked a child (Fork),
// replaced its image (Exec) or exited (Exit). The kernel folds the changes
// that happen between two Waits into one Event per process, so one Fork can
// stand for several children, and an Event can carry more than one change.
// It never names the child: list them with Children.
type Event struct {
	PID              int
	Fork, Exec, Exit bool
}

// Exited reports whether err from reading a process means only that it
// exited (ENOENT or ESRCH), which is no error.
func Exited(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH)
}
