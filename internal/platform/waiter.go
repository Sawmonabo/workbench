package platform

import "time"

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

// Waiter sleeps between ticks and wakes as soon as a watched process exits,
// or, where the system can follow forks (FollowsForks), a followed one forks
// or execs. Where it has nothing to wait on it just sleeps. Make one with
// NewWaiter.
type Waiter struct{ waiter }

// NewWaiter makes a Waiter that watches nothing yet.
func NewWaiter() *Waiter { return &Waiter{newWaiter()} }

// Watch makes Wait return when pid exits, and says what it did (see Attach).
// A followed process is already watched.
func (w *Waiter) Watch(pid int) Attach { return w.watch(pid) }

// Follow makes Wait return when pid forks, execs or exits, until Unfollow or
// its exit, and says what it did (see Attach). Forks between two Waits come
// back as one Event. Following a watched process never loses its exit: one
// that is Refused keeps its exit watch where the system allows it, and when
// the process is Gone the next Wait reports its exit. Where forks cannot be
// followed (FollowsForks) it is Unsupported and does nothing.
func (w *Waiter) Follow(pid int) Attach { return w.follow(pid) }

// Unfollow stops following pid. A process also watched stays watched for its
// exit where the system allows it, and when it is found gone the next Wait
// reports its exit.
func (w *Waiter) Unfollow(pid int) { w.unfollow(pid) }

// Wait sleeps for d or until a watched process exits or a followed one forks
// or execs, and returns what it woke for, nil when d ran out. An exit ends
// the process's watch and follow.
func (w *Waiter) Wait(d time.Duration) []Event { return w.wait(d) }

// Close releases what the Waiter holds of the system: its kqueue on macOS,
// and on Linux the pidfds still watched.
func (w *Waiter) Close() { w.close() }
