package guard

// This file is the history's writer: one goroutine in the watcher, beside the
// tick, that holds the only connection that writes the history. The tick hands
// it each command that finished and goes on, so a slow disk, likely just when
// memory is short, never delays a tick or a stop. Command text may hold a
// token, so nothing here logs it.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"sync"
	"time"
	"unsafe"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

const (
	// pendingLimit is the most the finished commands the history does not
	// hold yet may take in memory, by their fields, text, graphs and
	// processes (see size): past it the oldest are dropped.
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
// between them (see History.Trim). As often, while the history cannot be used
// too, it cuts the guard log: once it has read the time kept, it deletes the
// records older than the time kept it last read (see CutLogBefore), and it
// logs again each record of the watcher's conditions that last which the cut
// would delete or which is older than the oldest record the log holds (see
// lasts). After a failure it tries again every writeRetry, and logs the
// failure once, until a write succeeds. A history it cannot use (see
// ErrHistoryUnusable), found by any of its operations, it moves aside (see
// MoveDamaged), keeping at most two copies, starts a new one with the time
// kept it last read, logs both paths, and writes the commands it has not
// written to the new one. Its zero value takes commands and writes
// nothing until start. Only
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
	// failed is why the last write failed, as people read it (see
	// ShowPaths), "" once one succeeded.
	failed string
	// damaged are the copies kept of histories that could not be used, the
	// newest first, none while no copy is kept.
	damaged []DamagedCopy
	// lasting are the records of the watcher's conditions that last, by name
	// (see lasts).
	lasting map[string]*lastingRecord
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

// damagedCopies are the copies kept of histories that could not be used, the
// newest first; none when there is none.
func (h *historyWriter) damagedCopies() []DamagedCopy {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.damaged
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
	h.failed = ""
	h.mu.Unlock()
	if h.ended("history") {
		h.f.event(slog.LevelInfo, EventError, "the guard history can be written again")
	}
}

// fail marks the last write to the history in the guard folder root failed
// with err, a condition that lasts until a write succeeds, logged when the
// one before succeeded (see lasts): the log's error and the snapshot say it
// as people read it (see ShowPaths), and its path field names the history in
// full.
func (h *historyWriter) fail(root string, err error) {
	shown := ShowPaths(err.Error(), root)
	h.mu.Lock()
	h.failed = shown
	h.mu.Unlock()
	h.lasts("history", slog.LevelError, EventError,
		"cannot write the guard history; trying again every ten seconds",
		"error", shown, "path", HistoryFile(root))
}

// lastingRecord is the record of a condition that lasts, and logged, the time
// the record last logged carries, wall clock only, as the guard log holds it.
type lastingRecord struct {
	level      slog.Level
	event, msg string
	args       []any
	logged     time.Time
}

// lasts logs the record of the watcher's condition name, one that lasts
// until ended, such as a socket it cannot serve, which doctor and the view
// send people to the log for (see WatcherSilentBody). A condition that lasts
// already is not logged again: its record becomes this one, unlogged. Once a
// minute the writer's cut of the log logs it again (see writerRun.cutLog):
// before a cut by the time kept that would delete it, so a condition that
// lasts longer than the time kept still has its record in the log, and after
// a cut to the log's newest half, which any process appending past 1 MiB
// makes (see [operation.AppendLog]), deleted it, so the record is missing at
// most a minute then. A cut by `agent guard history --keep` waits for the
// writer's (see CutLogUnwatched).
func (h *historyWriter) lasts(name string, level slog.Level, event, msg string, args ...any) {
	h.mu.Lock()
	record, lasting := h.lasting[name]
	if !lasting {
		if h.lasting == nil {
			h.lasting = map[string]*lastingRecord{}
		}
		record = &lastingRecord{logged: time.Now().Round(0)}
		h.lasting[name] = record
	}
	record.level, record.event, record.msg, record.args = level, event, msg, args
	logged := record.logged
	h.mu.Unlock()
	if !lasting {
		h.f.eventAt(logged, level, event, msg, args...)
	}
}

// ended forgets the condition name, which no longer lasts, and reports
// whether it lasted (see lasts).
func (h *historyWriter) ended(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, lasting := h.lasting[name]
	delete(h.lasting, name)
	return lasting
}

// relogLasting logs again the record of each condition that lasts whose
// record last logged carries a time before since: one a cut of the log before
// since would delete, or one older than the oldest record the log holds,
// which a cut already deleted.
func (h *historyWriter) relogLasting(since time.Time) {
	h.mu.Lock()
	var due []lastingRecord
	for _, record := range h.lasting {
		if record.logged.Before(since) {
			record.logged = time.Now().Round(0)
			due = append(due, *record)
		}
	}
	h.mu.Unlock()
	for _, record := range due {
		h.f.eventAt(record.logged, record.level, record.event, record.msg, record.args...)
	}
}

// noteCopy records the copies of histories that could not be used that the
// guard folder root keeps, each with the time it was moved aside (see
// MoveDamaged), so a watcher started since says it too; and each no more once
// a trim deleted it.
func (h *historyWriter) noteCopy(root string) {
	copies := HistoryDamagedCopies(root)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.damaged = copies
}

// writerRun is one run of the writer's goroutine: the history it holds open,
// nil until it opens it, and the time kept it last read, 0 before any, and
// when it read it; when to try again after a failure, zero while none; when
// the last history it could not use was moved aside by it; when the next trim
// starts and whether the last trim step had more to delete; when the guard
// log is next cut. The goroutine has no context: it stops only when drain
// asks, after writing the commands it holds (see historyWriter.run), so its
// opens, reads and writes of the history run with context.Background().
type writerRun struct {
	h        *historyWriter
	root     string
	history  *History
	keep     time.Duration
	keptAt   time.Time
	retry    time.Time
	replaced time.Time
	trimAt   time.Time
	trimming bool
	cutAt    time.Time
}

// step does what is due at now, if anything, and reports whether it did:
// open the history, cut the guard log, write the oldest command not written,
// or one trim step. The history opens before the first cut, so that cut
// deletes by the time kept it reads. After a failure nothing is due until
// writeRetry has passed but the cut of the log, which needs no history.
func (r *writerRun) step(now time.Time) bool {
	switch {
	case r.history == nil && !now.Before(r.retry):
		r.open()
		return true
	case !now.Before(r.cutAt):
		r.cutLog(now)
		return true
	case now.Before(r.retry):
		return false
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
	if !now.Before(r.trimAt) {
		r.trim(now)
		return true
	}
	return false
}

// open opens the history, making it when it is missing, reads the time kept
// and notes the copies of histories that could not be used.
func (r *writerRun) open() {
	history, err := OpenHistory(context.Background(), r.root, true)
	if err != nil {
		r.failed(err)
		return
	}
	r.history = history
	if err := r.readKeep(); err != nil {
		r.failed(err)
		return
	}
	r.h.noteCopy(r.root)
}

// readKeep reads the time kept from the history open, noting when.
func (r *writerRun) readKeep() error {
	keep, err := r.history.Keep(context.Background())
	if err == nil {
		r.keep, r.keptAt = keep, time.Now()
	}
	return err
}

// trim runs one trim step at now. The first step of a trim checks the file
// (see History.Check) and reads the time kept, so a history started again
// after damage keeps it (see replace). A step with more to delete is followed
// by the next once the history has been left to another writer as long as
// the step held it (see TrimPause); once a step has nothing more to delete,
// the next trim is trimEvery later and the copies of histories that could not
// be used, which a trim deletes once each is older than the time kept, are
// noted again. A file the check fails, one that is gone, replaced
// or whose header is not a database's, is a history that cannot be used (see
// failed).
func (r *writerRun) trim(now time.Time) {
	ctx := context.Background()
	if !r.trimming {
		if err := r.history.Check(); err != nil {
			r.failed(err)
			return
		}
		if err := r.readKeep(); err != nil {
			r.failed(err)
			return
		}
	}
	_, again, err := r.history.Trim(ctx, now)
	if err != nil {
		r.trimming = false
		r.failed(err)
		return
	}
	r.succeeded()
	r.trimming = again
	if again {
		r.trimAt = time.Now().Add(TrimPause(time.Since(now)))
		return
	}
	r.trimAt = now.Add(trimEvery)
	r.h.noteCopy(r.root)
}

// cutLog reads the time kept again, so a longer one set meanwhile keeps its
// lines, and deletes the guard log's records older than it (see
// CutLogBefore), or than the one last read when the history cannot be read
// now, after logging again each record of a condition that lasts that the
// cut would delete (see lasts). Until a time kept was read it deletes
// nothing, so a history that was never read never makes it guess one. Then
// it logs again each record of a condition that lasts older than the oldest
// record the log holds, the one in its first line with a time, which a cut
// to the log's newest half deleted; with no such record, every one. The next
// cut is due trimEvery later. It is best effort, as a write of the log is: a
// log that cannot be cut or read is tried again at the next cut.
func (r *writerRun) cutLog(now time.Time) {
	r.cutAt = now.Add(trimEvery)
	if r.history != nil {
		_ = r.readKeep()
	}
	var since time.Time
	if r.keep > 0 {
		since = now.Add(-r.keep)
		r.h.relogLasting(since)
	}
	oldest, err := CutLogBefore(r.root, since)
	if err != nil {
		return
	}
	if oldest.IsZero() {
		oldest = now
	}
	r.h.relogLasting(oldest)
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
	r.h.fail(r.root, err)
}

// replace reads the time kept once more from the history, which cause found
// it cannot use, then closes it and moves it aside (see MoveDamaged), keeping
// the oldest copy younger than the time kept (24 hours when it read none) and
// the new one. It starts a new history with the time kept it last read and
// logs both paths, the copy the move deleted and the time the new one keeps:
// as the old one did, when that last read answered, which it does when the
// path names another file or none, or the damage left the setting readable;
// else the time it read before and when, since a time set after that is in
// the old file alone; 24 hours when it read none. A history
// that is no longer there is not moved, and the new one is logged alone:
// closing wrote the rows of the WAL into the file it held, wherever that is
// now, and deleted that file's -wal and -shm (see History.Close), and any
// other side file still at its name, of no database there, is deleted too,
// since SQLite would read it as the new one's. It gives the error of a move
// or start that failed.
func (r *writerRun) replace(cause error) error {
	fresh := r.history != nil && r.readKeep() == nil
	r.close()
	from := HistoryFile(r.root)
	to, replaced, err := MoveDamaged(r.root, time.Now().Add(-cmp.Or(r.keep, HistoryKeepDefault)))
	gone := to == "" && errors.Is(err, fs.ErrNotExist)
	if to == "" && !gone {
		return fmt.Errorf("cannot move the guard history that cannot be used aside: %w", err)
	}
	for _, side := range sqlitefile.SideFiles(from) {
		if !gone {
			break
		}
		if err := operation.RemoveIfThere(side); err != nil {
			return err
		}
	}
	history, err := OpenHistory(context.Background(), r.root, true)
	if err != nil {
		return err
	}
	var kept string
	switch {
	case r.keep == 0:
		kept = "it keeps finished commands 24 hours: the time kept was never read"
	case fresh:
		kept = "it keeps finished commands " + KeepText(r.keep) + ", as the old one did"
	default:
		kept = fmt.Sprintf(
			"it keeps finished commands %s, the time kept the old one had when last read, at %s",
			KeepText(r.keep), r.keptAt.Local().Format(display.DayTimeSeconds))
	}
	if r.keep != 0 && r.keep != HistoryKeepDefault {
		if err := history.SetKeep(context.Background(), r.keep); err != nil {
			_ = history.Close()
			return err
		}
	}
	r.history = history
	if r.keep != 0 {
		r.keptAt = time.Now()
	}
	why := ShowPaths(cause.Error(), r.root)
	if gone {
		r.h.f.event(slog.LevelWarn, EventError, fmt.Sprintf(
			"the guard history %s is gone; started a new one; %s",
			ShowPaths(from, r.root), kept), "error", why, "path", from)
		return nil
	}
	r.h.noteCopy(r.root)
	moved := "moved it to " + ShowPaths(to, r.root)
	if !replaced.IsZero() {
		moved += fmt.Sprintf(
			" in place of the copy set aside at %s, keeping %s, the oldest,",
			replaced.Local().Format(display.DayTimeSeconds),
			ShowPaths(olderDamagedFile(r.root), r.root),
		)
	}
	r.h.f.event(slog.LevelWarn, EventError, fmt.Sprintf(
		"the guard history %s cannot be used; %s and started a new one; %s",
		ShowPaths(from, r.root), moved, kept),
		"error", why, "path", from, "moved_to", to)
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
// or the retry, the next trim or the next cut of the log is due.
func (r *writerRun) wait(wake <-chan struct{}) {
	next := r.trimAt
	if now := time.Now(); now.Before(r.retry) {
		next = r.retry
	}
	if r.cutAt.Before(next) {
		next = r.cutAt
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

// size is about how many bytes f takes in memory: its own fields, its text,
// its graph and its processes, each a PID, a name's header and a peak beside
// the name's bytes. Measured, commands of 10,000 processes named cc1plus took
// 41 bytes a process and 5,000 short commands 526 bytes each, both within 7
// percent of what this counts.
func (f *Finished) size() int {
	n := int(unsafe.Sizeof(*f)) + len(f.ID) + len(f.Kind) + len(f.LaunchedBy) + len(f.Agent) +
		len(f.Subagent) + len(f.Project) + len(f.Cwd) + len(f.Command) + len(f.State) +
		len(f.By) + int(unsafe.Sizeof(uint64(0)))*len(f.Graph)
	for _, p := range f.Processes {
		n += int(unsafe.Sizeof(p)) + len(p.Name)
	}
	return n
}
