package platform

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// waiter polls the watched processes' pidfds. Without a pidfd it just
// sleeps.
type waiter struct{ fds map[int]int }

// newWaiter makes a waiter with no pidfd yet.
func newWaiter() waiter { return waiter{fds: map[int]int{}} }

// watch opens pid's pidfd: pidfd_open gives ESRCH for a process that is
// gone, a kernel without it (ENOSYS) is Unsupported, and any other error
// (such as EMFILE) is Refused.
func (w *waiter) watch(pid int) Attach {
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

// follow is Unsupported and does nothing: Linux cannot follow another
// process's forks and execs here (FollowsForks).
func (*waiter) follow(int) Attach { return Unsupported }

// unfollow does nothing, since nothing is ever followed here.
func (*waiter) unfollow(int) {}

// wait polls the pidfds for up to d and returns the exits it woke for.
func (w *waiter) wait(d time.Duration) []Event {
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

// close releases the pidfds still watched.
func (w *waiter) close() {
	for _, fd := range w.fds {
		_ = unix.Close(fd)
	}
}
