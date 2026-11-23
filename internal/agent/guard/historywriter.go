package guard

// This file is the history's writer: one goroutine in the watcher, beside the
// tick, that holds the only connection that writes the history. The tick hands
// it each command that finished and goes on, so a slow disk, likely just when
// memory is short, never delays a tick or a stop. Command text may hold a
// token, so nothing here logs it.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

const (
	// pendingLimit is the most the finished commands the history does not
	// hold yet may take, by their text, graphs and processes (see size): past
	// it the oldest are dropped.
	pendingLimit = 2 << 20
	// writeRetry is how long the writer waits after a failure before it tries
	// again.
	writeRetry = 10 * time.Second
	// trimEvery is how often the writer trims the history, after the trim it
	// starts with.
	trimEvery = time.Minute
	// drainWait bounds how long a watcher that hands over or exits, or lets
	// its lock go when idle, waits for the writer to write what it has not.
	drainWait = time.Second
)

// historyWriter writes the history in the guard folder: each finished command
// handed to it (see submit), in one transaction each, oldest first, and a trim
// when it starts and once a minute, in steps with the commands handed to it
// between them (see History.Trim). After a failure it tries again every
// writeRetry, and logs the failure once, until a write succeeds. A history it
// cannot use (see ErrHistoryUnusable), found by any of its operations, it
// moves aside (see MoveDamaged), starts a new one with the time kept it last
// read, logs both paths, and writes the commands it has not written to the
// new one. Its zero value takes commands and writes nothing until start. Only
// its own goroutine touches the history; the watcher's calls only take mu,
// which no database call is made under.
type historyWriter struct {
	// f is the watcher's guard folder, for the writer's log; it is set before
	// the first start and never changed.
	f folders

	mu sync.Mutex
	// queue are the commands handed to the writer and not written, oldest
	// first, at most pendingLimit of them; the first may be being written.
	queue []Finished
	// written are the IDs of the commands written since done last ran.
	written []string
	// failed is why the last write failed, "" once one succeeded.
	failed string
	// damagedAt is when a history that could not be used was moved aside to
	// copied, zero while no such copy is kept.
	damagedAt time.Time
	copied    string
	// wake tells the goroutine that a command was handed to it or that drain
	// asks it to stop.
	wake chan struct{}
	// running is set while the goroutine runs, stopping once drain asked it
	// to stop, and stopped is closed when it has.
	running, stopping bool
	stopped           chan struct{}
}

// start starts the writer on the history in the guard folder root, when it
// does not run; one that drain asked to stop and that has not stopped yet
// carries on instead.
func (h *historyWriter) start(root string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopping = false
	if h.running {
		return
	}
	if h.wake == nil {
		h.wake = make(chan struct{}, 1)
	}
	h.running, h.stopped = true, make(chan struct{})
	go h.run(&writerRun{h: h, root: root, trimAt: time.Now()}, h.wake, h.stopped)
}

// submit hands f to the writer, never waiting on it. Past pendingLimit it
// drops the oldest it has not written, as the watcher's own list does (see
// keepWithin).
func (h *historyWriter) submit(f Finished) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.queue, _ = keepWithin(append(h.queue, f), pendingLimit)
	h.signal()
}

// signal wakes the goroutine, when it waits. The caller holds mu.
func (h *historyWriter) signal() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// done gives the IDs of the commands written since it last ran, so they leave
// the watcher's list of those the history does not hold yet.
func (h *historyWriter) done() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	written := h.written
	h.written = nil
	return written
}

// failure is why the last write failed, "" once one succeeded.
func (h *historyWriter) failure() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failed
}

// damaged is when a history that could not be used was moved aside, and
// where to, while that copy is kept; zero when there is none.
func (h *historyWriter) damaged() (time.Time, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.damagedAt, h.copied
}

// drain asks the writer to stop: it writes what it has not, without waiting
// out a retry, then closes the history. It waits at most d for that, by its
// own timer, and gives how many commands are not written. A writer still busy
// after d stops once its write returns, unless start runs first.
func (h *historyWriter) drain(d time.Duration) (left int) {
	h.mu.Lock()
	running, stopped := h.running, h.stopped
	if running {
		h.stopping = true
		h.signal()
	}
	h.mu.Unlock()
	if running {
		timer := time.NewTimer(d)
		select {
		case <-stopped:
		case <-timer.C:
		}
		timer.Stop()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.queue)
}

// run is the writer's goroutine: it does what is due, one step at a time,
// and waits when nothing is. Asked to stop, it writes what it has not, closes
// the history and ends, unless start ran meanwhile.
func (h *historyWriter) run(r *writerRun, wake <-chan struct{}, stopped chan struct{}) {
	for {
		if h.askedToStop() {
			r.last()
			r.close()
			if h.end(stopped) {
				return
			}
			continue
		}
		if !r.step(time.Now()) {
			r.wait(wake)
		}
	}
}

// askedToStop reports whether drain asked the goroutine to stop.
func (h *historyWriter) askedToStop() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopping
}

// end marks the goroutine stopped and closes stopped, unless start ran since
// drain asked it to stop; it reports whether it did.
func (h *historyWriter) end(stopped chan struct{}) bool {
	h.mu.Lock()
	if !h.stopping {
		h.mu.Unlock()
		return false
	}
	h.running = false
	h.mu.Unlock()
	close(stopped)
	return true
}

// first is the oldest command not written.
func (h *historyWriter) first() (Finished, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.queue) == 0 {
		return Finished{}, false
	}
	return h.queue[0], true
}

// queued is how many commands are not written.
func (h *historyWriter) queued() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.queue)
}

// wrote takes the command id, written, off the queue, unless it was dropped
// meanwhile, and reports it to done.
func (h *historyWriter) wrote(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.queue) > 0 && h.queue[0].ID == id {
		h.queue = slices.Delete(h.queue, 0, 1)
	}
	h.written = append(h.written, id)
}

// succeeded marks the last write a success, and logs that the history can be
// written again after a failure.
func (h *historyWriter) succeeded() {
	h.mu.Lock()
	was := h.failed
	h.failed = ""
	h.mu.Unlock()
	if was != "" {
		h.f.event(slog.LevelInfo, EventError, "the guard history can be written again")
	}
}

// fail marks the last write failed with err, logged when the one before
// succeeded.
func (h *historyWriter) fail(err error) {
	h.mu.Lock()
	was := h.failed
	h.failed = err.Error()
	h.mu.Unlock()
	if was == "" {
		h.f.event(slog.LevelError, EventError,
			"cannot write the guard history; trying again every ten seconds", "error", err)
	}
}

// setDamaged records when a history that could not be used was moved aside to
// copied; zero when no copy is kept.
func (h *historyWriter) setDamaged(at time.Time, copied string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.damagedAt, h.copied = at, copied
}

// noteCopy records the copy of a history that could not be used in the guard
// folder root, when one is kept, with the time it was moved aside (see
// MoveDamaged), so a watcher started since says it too; and none once a trim
// deleted it.
func (h *historyWriter) noteCopy(root string) {
	copied := damagedFile(root)
	if info, err := os.Lstat(copied); err == nil {
		h.setDamaged(info.ModTime(), copied)
	} else {
		h.setDamaged(time.Time{}, "")
	}
}

// writerRun is one run of the writer's goroutine: the history it holds open,
// nil until it opens it, and the time kept it last read, 0 before any; when to
// try again after a failure, zero while none; when the last history it could
// not use was moved aside by it; when the next trim starts and whether the
// last trim step had more to delete.
type writerRun struct {
	h        *historyWriter
	root     string
	history  *History
	keep     time.Duration
	retry    time.Time
	replaced time.Time
	trimAt   time.Time
	trimming bool
}

// step does what is due at now, if anything, and reports whether it did:
// open the history, write the oldest command not written, or one trim step.
// After a failure nothing is due until writeRetry has passed.
func (r *writerRun) step(now time.Time) bool {
	switch {
	case now.Before(r.retry):
		return false
	case r.history == nil:
		r.open()
		return true
	}
	if f, ok := r.h.first(); ok {
		if err := r.history.Insert(context.Background(), f); err != nil {
			r.failed(err)
		} else {
			r.h.wrote(f.ID)
			r.succeeded()
		}
		return true
	}
	if r.trimming || !now.Before(r.trimAt) {
		r.trim(now)
		return true
	}
	return false
}

// open opens the history, making it when it is missing, reads the time kept
// and notes a copy of a history that could not be used.
func (r *writerRun) open() {
	history, err := OpenHistory(r.root, true)
	if err != nil {
		r.failed(err)
		return
	}
	r.history = history
	keep, err := history.Keep(context.Background())
	if err != nil {
		r.failed(err)
		return
	}
	r.keep = keep
	r.h.noteCopy(r.root)
}

// trim runs one trim step at now. The first step of a trim checks the file
// (see History.Check) and reads the time kept, so a history started again
// after damage keeps it (see replace); once a step has nothing more to
// delete, the next trim is trimEvery later and the copy of a history that
// could not be used, which a trim deletes once it is older than the time
// kept, is noted again. A file the check fails, one that is gone, replaced
// or whose header is not a database's, is a history that cannot be used (see
// failed).
func (r *writerRun) trim(now time.Time) {
	ctx := context.Background()
	if !r.trimming {
		if err := r.history.Check(); err != nil {
			r.failed(err)
			return
		}
		keep, err := r.history.Keep(ctx)
		if err != nil {
			r.failed(err)
			return
		}
		r.keep = keep
	}
	again, err := r.history.Trim(ctx, now)
	if err != nil {
		r.trimming = false
		r.failed(err)
		return
	}
	r.succeeded()
	r.trimming = again
	if !again {
		r.trimAt = now.Add(trimEvery)
		r.h.noteCopy(r.root)
	}
}

// succeeded ends a failure: nothing waits out a retry any more.
func (r *writerRun) succeeded() {
	r.retry = time.Time{}
	r.h.succeeded()
}

// failed handles err from any operation on the history: a history that cannot
// be used, or one that is gone from under the writer, is moved aside, when it
// is there, and a new one started at once (see replace), at most once each
// writeRetry; any other failure, or one of that move, is tried again after
// writeRetry and logged once.
func (r *writerRun) failed(err error) {
	unusable := errors.Is(err, ErrHistoryUnusable) || sqlitefile.IsDamaged(err) ||
		r.history != nil && errors.Is(err, fs.ErrNotExist)
	if unusable && time.Since(r.replaced) >= writeRetry {
		r.replaced = time.Now()
		if err = r.replace(err); err == nil {
			return
		}
	}
	r.retry = time.Now().Add(writeRetry)
	r.h.fail(err)
}

// replace closes the history, which cause found it cannot use, moves it aside
// (see MoveDamaged), starts a new one with the time kept it last read (24
// hours when it read none, which it logs) and logs both paths. A history
// that is no longer there is not moved: the -wal and -shm files left of it,
// which SQLite would read as the new one's, are deleted, and the new one is
// logged alone. It gives the error of a move or start that failed.
func (r *writerRun) replace(cause error) error {
	r.close()
	from := historyFile(r.root)
	to, err := MoveDamaged(r.root)
	gone := to == "" && errors.Is(err, fs.ErrNotExist)
	if to == "" && !gone {
		return fmt.Errorf("cannot move the guard history that cannot be used aside: %w", err)
	}
	for _, side := range sideFiles {
		if !gone {
			break
		}
		if err := operation.RemoveIfThere(from + side); err != nil {
			return err
		}
	}
	history, err := OpenHistory(r.root, true)
	if err != nil {
		return err
	}
	kept := "it keeps the time kept the old one had"
	switch r.keep {
	case 0:
		kept = "the time kept was never read, so it keeps finished commands 24 hours"
	case historyKeepDefault:
	default:
		if err := history.SetKeep(context.Background(), r.keep); err != nil {
			_ = history.Close()
			return err
		}
	}
	r.history = history
	if gone {
		r.h.f.event(slog.LevelWarn, EventError, fmt.Sprintf(
			"the guard history %s is gone; started a new one; %s", from, kept), "error", cause)
		return nil
	}
	r.h.setDamaged(time.Now(), to)
	r.h.f.event(slog.LevelWarn, EventError, fmt.Sprintf(
		"the guard history %s cannot be used; moved it to %s and started a new one; %s",
		from, to, kept), "error", cause)
	return nil
}

// last writes what the writer has not, as drain asks, without waiting out a
// retry, until a write fails.
func (r *writerRun) last() {
	r.retry = time.Time{}
	for r.retry.IsZero() && r.h.queued() > 0 {
		r.step(time.Now())
	}
}

// wait waits until a command is handed to the writer, drain asks it to stop,
// or the retry or next trim is due.
func (r *writerRun) wait(wake <-chan struct{}) {
	next := r.trimAt
	if now := time.Now(); now.Before(r.retry) {
		next = r.retry
	}
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()
	select {
	case <-wake:
	case <-timer.C:
	}
}

// close closes the history, when it is open.
func (r *writerRun) close() {
	if r.history != nil {
		_ = r.history.Close()
		r.history = nil
	}
}

// keepWithin is list, oldest first, without its oldest commands past limit
// (see size), and how many it dropped. It changes list in place.
func keepWithin(list []Finished, limit int) ([]Finished, int) {
	total := 0
	for i := range list {
		total += list[i].size()
	}
	drop := 0
	for drop < len(list) && total > limit {
		total -= list[drop].size()
		drop++
	}
	return slices.Delete(list, 0, drop), drop
}

// size is about how many bytes f takes: its text, its graph and its
// processes.
func (f *Finished) size() int {
	n := len(f.ID) + len(f.Kind) + len(f.LaunchedBy) + len(f.Agent) + len(f.Subagent) +
		len(f.Project) + len(f.Cwd) + len(f.Command) + len(f.State) + len(f.By) + 8*len(f.Graph)
	for _, p := range f.Processes {
		n += 16 + len(p.Name)
	}
	return n
}
