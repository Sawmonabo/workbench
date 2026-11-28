package platform

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
	// still being made by fork is not handed out either, but by the published
	// XNU source it is on no process list yet, so a listed process that is
	// Gone has begun to exit. Nothing is to be remembered of it.
	Gone
	// Refused is a process that runs and that the system will not attach
	// this watch to. Whatever was watched of it before is watched again
	// where the system allows it.
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
