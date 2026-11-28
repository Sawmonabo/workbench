package platform

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// Waiter sleeps between ticks and wakes as soon as a watched process exits,
// by polling the processes' pidfds. Without a pidfd it just sleeps.
type Waiter struct{ fds map[int]int }

// NewWaiter makes a Waiter that watches nothing yet.
func NewWaiter() *Waiter { return &Waiter{fds: map[int]int{}} }

// Watch makes Wait return when pid exits, and says what it did (see Attach):
// pidfd_open gives ESRCH for a process that is gone, a kernel without it
// (ENOSYS) is Unsupported, and any other error (such as EMFILE) is Refused.
func (w *Waiter) Watch(pid int) Attach {
	if _, ok := w.fds[pid]; ok {
		return Attached
	}
	fd, err := unix.PidfdOpen(pid, 0)
	switch {
	case err == nil:
		w.fds[pid] = fd
		return Attached
	case errors.Is(err, unix.ESRCH):
		return Gone
	case errors.Is(err, unix.ENOSYS):
		return Unsupported
	}
	return Refused
}

// Follow is Unsupported and does nothing: Linux cannot follow another
// process's forks and execs here (FollowsForks).
func (*Waiter) Follow(int) Attach { return Unsupported }

// Unfollow does nothing, since nothing is ever followed here.
func (*Waiter) Unfollow(int) {}

// Wait sleeps for d or until a watched process exits, and returns the exits
// it woke for, nil when d ran out.
func (w *Waiter) Wait(d time.Duration) []Event {
	if len(w.fds) == 0 {
		time.Sleep(d)
		return nil
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
		return nil
	}
	var out []Event
	for i, poll := range polls {
		if poll.Revents != 0 {
			_ = unix.Close(int(poll.Fd))
			delete(w.fds, pids[i])
			out = append(out, Event{PID: pids[i], Exit: true})
		}
	}
	return out
}

// Close releases the pidfds still watched.
func (w *Waiter) Close() {
	for _, fd := range w.fds {
		_ = unix.Close(fd)
	}
}
