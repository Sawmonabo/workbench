package platform

import (
	"errors"
	"io/fs"

	"golang.org/x/sys/unix"
)

// Process is one process. Start is the kernel's start time in the system's
// own units (StartSecond is one second of them) and, with the PID, names the
// process: a later process reusing the PID has another Start. UID is the
// process's effective user.
type Process struct {
	PID, PPID, PGID int
	UID             int
	Start           uint64
	Name            string
	Zombie          bool
}

// Lineage is the kernel's record of where a process came from, where the
// system keeps one (KnowsOriginalParent): Version names the process itself
// and ParentVersion the process that forked it, as that parent's Version was
// when it forked, so it no longer equals the parent's Version once the parent
// execs. It stays recorded after that parent exits and init adopts the child.
type Lineage struct{ Version, ParentVersion uint32 }

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
