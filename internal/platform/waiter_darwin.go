package platform

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// waiter waits through kqueue's EVFILT_PROC. Any process the caller can see
// may be watched and followed: the kernel checks permission only for
// NOTE_EXITSTATUS, which is never asked for. Without a kqueue it just sleeps.
type waiter struct {
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

// newWaiter opens the kqueue a waiter waits on; kq is -1 when it cannot.
func newWaiter() waiter {
	kq, err := unix.Kqueue()
	if err != nil {
		kq = -1
	}
	return waiter{kq: kq, watched: map[int]watch{}, events: make([]unix.Kevent_t, 64)}
}

// watch adds a one-shot NOTE_EXIT knote for pid, or notes the watch on the
// knote of a followed process, which is already watched.
func (w *waiter) watch(pid int) Attach {
	now, ok := w.watched[pid]
	switch {
	case w.kq < 0:
		return Unsupported
	case ok:
		now.exit = true
		w.watched[pid] = now
		return Attached
	}
	result := attachResult(w.register(pid, unix.EV_ADD|unix.EV_ONESHOT, unix.NOTE_EXIT))
	if result == Attached {
		w.watched[pid] = watch{exit: true}
	}
	return result
}

// attachResult is what an EVFILT_PROC attach's error means. The kernel's
// filt_procattach (XNU bsd/kern/kern_event.c) gives ESRCH when proc_find finds
// no process to attach to: none by that ID, or one the kernel will no longer
// hand out because it is exiting (proc_prepareexit sets P_REF_DEAD, "will not
// be visible via proc_find", before the last thread ends and the process
// becomes a zombie, bsd/kern/kern_exit.c) or is still being made (P_REF_NEW,
// while it is on no process list yet). Any other error is a refusal of a
// process that is there.
func attachResult(err error) Attach {
	switch {
	case err == nil:
		return Attached
	case errors.Is(err, unix.ESRCH):
		return Gone
	}
	return Refused
}

// follow adds a NOTE_FORK, NOTE_EXEC and NOTE_EXIT knote for pid, which the
// kernel drops itself at the exit. The knote is kept after each event and
// cleared when read (EV_CLEAR), so forks between two Waits come back as one
// Event.
//
// Following a watched process replaces its one-shot exit knote. kqueue keys a
// knote by process and filter, and EV_ADD on that pair changes the events it
// reports but keeps EV_ONESHOT (measured: the knote was gone after its first
// event), so the old knote is deleted and a new one added. A watched process
// that exits in between, or whose exit was waiting to be read, is Gone, and
// the next Wait reports that exit itself, so a watched exit is never lost. A
// watched process that is Refused gets its exit watch back where the system
// allows it; one found Gone at that second try is Gone.
func (w *waiter) follow(pid int) Attach {
	now, ok := w.watched[pid]
	switch {
	case w.kq < 0:
		return Unsupported
	case now.follow:
		return Attached
	}
	if ok {
		_ = w.register(pid, unix.EV_DELETE, 0)
		delete(w.watched, pid)
	}
	result := attachResult(
		w.register(pid, unix.EV_ADD|unix.EV_CLEAR, unix.NOTE_FORK|unix.NOTE_EXEC|unix.NOTE_EXIT))
	switch {
	case result == Attached:
		w.watched[pid] = watch{exit: ok && now.exit, follow: true}
	case !ok:
	case result == Gone:
		w.exited = append(w.exited, Event{PID: pid, Exit: true})
	case w.watch(pid) == Gone:
		w.exited = append(w.exited, Event{PID: pid, Exit: true})
		return Gone
	}
	return result
}

// unfollow changes the knote of a process also watched to NOTE_EXIT alone,
// in place, and removes any other's.
func (w *waiter) unfollow(pid int) {
	now, ok := w.watched[pid]
	if !ok || !now.follow {
		return
	}
	if now.exit {
		// Changing the knote in place leaves no gap for an exit to fall
		// into; it stays EV_CLEAR, which the exit drops like a one-shot.
		switch attachResult(w.register(pid, unix.EV_ADD|unix.EV_CLEAR, unix.NOTE_EXIT)) {
		case Attached:
			w.watched[pid] = watch{exit: true}
			return
		case Gone:
			w.exited = append(w.exited, Event{PID: pid, Exit: true})
		default:
			// Refused in place: the follow is removed below, and a new
			// one-shot exit watch is tried, whose own Gone is reported.
			_ = w.register(pid, unix.EV_DELETE, 0)
			delete(w.watched, pid)
			if w.watch(pid) == Gone {
				w.exited = append(w.exited, Event{PID: pid, Exit: true})
			}
			return
		}
	}
	// Removing a knote the kernel already dropped at the exit fails with
	// ENOENT, which leaves nothing to undo.
	_ = w.register(pid, unix.EV_DELETE, 0)
	delete(w.watched, pid)
}

// register applies one change to pid's EVFILT_PROC knote and gives the
// kernel's error, nil when it took it.
func (w *waiter) register(pid int, flags int, fflags uint32) error {
	var event unix.Kevent_t
	unix.SetKevent(&event, pid, unix.EVFILT_PROC, flags)
	event.Fflags = fflags
	_, err := unix.Kevent(w.kq, []unix.Kevent_t{event}, nil, nil)
	return err
}

// wait reads the kqueue's events for up to d. An exit ends the process's
// watch and follow: the kernel posts it with EV_EOF and drops the knote,
// one-shot or not.
func (w *waiter) wait(d time.Duration) []Event {
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

// close releases the kqueue.
func (w *waiter) close() {
	if w.kq >= 0 {
		_ = unix.Close(w.kq)
	}
}
