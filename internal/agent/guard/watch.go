package guard

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// idleExit is how long the watcher runs with no note and no tracked command
// or unit before it exits (see Watch).
const idleExit = 10 * time.Minute

type watcher struct {
	f        folders
	release  func()
	lockFile *os.File
	lockInfo os.FileInfo
	tracked  map[string]command
	// usage is each tracked command's memory, by call, kept as long as it is
	// tracked; largestPeak is the largest peak any command reached since this
	// watcher, or the one it took over from, started.
	usage       map[string]*usage
	largestPeak uint64
	seen        map[string]seenNote
	stopped     []proc    // the last stopped command's processes
	quietFrom   time.Time // when they were all gone
	// passed are commands never chosen again: a dry-run one already logged,
	// one still running after a stop gave up on it, or one whose stops kept
	// signalling nothing.
	passed map[string]bool
	// refusals counts a call's consecutive stops that signalled nothing while
	// its command still ran.
	refusals map[string]int
	// ended are calls whose tracked command has ended. A call runs once, so
	// its note, which stays until notes reads it ended, its agent exits or it
	// is dropped posted or abandoned (see notes), is never matched again.
	ended map[string]bool
	// lastEnded is, for each agent, the latest NotedStart of its notes that
	// went away while this watcher ran.
	lastEnded map[procKey]uint64
	// setsCallIDs are the Codex agents seen with a child carrying a call ID
	// of its own (Codex 0.163.0 and later), while they run.
	setsCallIDs map[procKey]bool
	// records are what is kept of each call whose note was read, while its
	// agent runs (see records).
	records *records
	// known are the agents the guard knows, each with its kind, from their
	// agent files (see readAgentFiles) and their notes, until they are gone:
	// every one that runs is protected, with or without a note.
	known map[procKey]kind
	// stamps are each process's stamp, read once per program it runs
	// and dropped when the process is gone; stampAgents are the live
	// processes a stamp read names as its agent that started before the
	// stamped process, protected while they live (see stampOf). classified is
	// each child of the init process unitRootWhy has judged, with its verdict,
	// while it runs (see judged). ancestry is where each process of a tracked
	// tree was seen and what it came from (see recordLineage). lineageLost is
	// set while the kernel's original-parent record is found unreadable.
	stamps      map[procKey]cachedStamp
	stampAgents map[procKey]bool
	classified  map[procKey]judged
	ancestry    *ancestry
	lineageLost bool
	idleSince   time.Time
	expired     time.Time
	waiter      *platform.Waiter
	// glimpse is what the watcher follows between ticks, nil until it
	// follows something (see followAgents).
	glimpse *glimpse
	// unsized are processes whose footprint could not be read, so each is
	// logged once, not every tick.
	unsized map[procKey]bool
	// agentChildren are the agents' live children at the last tick, each with
	// the first tick it was seen at since.
	agentChildren map[procKey]time.Time
	// underFloor is set once a pressure episode's agent commands were logged
	// as too small to stop, and cleared when memory recovers.
	underFloor bool
	// softSince and criticalSince are when a soft condition and macOS's
	// critical level began, zero while they do not hold (see demand).
	softSince, criticalSince time.Time
	// round counts ticks; missing holds the round each needed process (an
	// agent of a note or a known agent, a tracked command's root) was first
	// absent in.
	round   int
	missing map[procKey]int
	// unreadNotes and unreadPIDs are notes and processes that could not be
	// read, so each is logged once, not every tick.
	unreadNotes map[string]bool
	unreadPIDs  map[int]bool
	// calm is set by a tick that read memory calling for nothing, with no
	// hold under way: a moment a handover may happen at.
	calm bool
	// server is the socket the agent guard view connects to, nil when it could not be
	// served; view is the last snapshot a tick built for it.
	server *server
	view   Snapshot
	// manual are the stops people asked for, by call, while they are followed
	// (see followManual); stoppedBy names who stopped each tracked command
	// that was signalled, guard or you.
	manual    map[string]*manualStop
	stoppedBy map[string]string
	// unwritten are the commands that finished, oldest first, until the
	// history writer reports them written, at most pendingLimit of them (see
	// keepWithin); dropped counts those dropped since the list was last
	// empty. history is the writer, which runs while this watcher holds the
	// lock. letGo is set while carryOn reads the notes with the lock let go.
	unwritten []Finished
	dropped   int
	history   historyWriter
	letGo     bool
	// unitsSaved is each unit's record as it is on disk: what saveUnits last
	// wrote of it, or what restoreUnits read of a record it took on;
	// unitsMade is set once saveUnits has made the units folder; boot is the
	// system start the records name, read once; unitSaveFailed is set once a
	// failed read or save of them is logged, until a save goes through;
	// unitsRead is set once a tick has taken on what the watcher before it
	// left, carried or in the records (see restoreUnits).
	unitsSaved     map[string][]byte
	unitsMade      bool
	boot           string
	unitSaveFailed bool
	unitsRead      bool
	// list, tickSnap and tickBelow are the process list, snapshot and parent
	// index each tick builds again in the last tick's (see tickSnapshot,
	// snapshot.belowInto). Only tick builds them, and nothing keeps them, or
	// a slice of the index, past the tick: what is kept of a process, such as
	// a tracked command's root or a stopped tree, is a copy of it, and every
	// tree is a new slice.
	list      platform.ProcessList
	tickSnap  snapshot
	tickBelow parents
	handoff
}

// Watch is the watcher, at most one per user, started detached by the pre
// hook. It finds each noted command, watches machine memory, and near
// out-of-memory stops the largest agent command and records why. It returns
// after ten minutes with no note and no tracked command or unit, when its
// lock file is no longer the one it locked, once it has handed over to
// another release's watcher (see handOver), or at its next tick after ctx
// ends; a stop under way finishes first. It has no terminal and logs only to
// the guard log. It works from /, so it never holds the folder of the session
// that started it. version is the release it runs, for the log.
func Watch(ctx context.Context, version string) {
	// The watcher must keep working when the machine is short of memory, so
	// it keeps its own small. Its heap grows to its live heap plus GC percent
	// of it before each collection, however little it allocates between: at
	// 50 the heap under a busy build stays about a quarter smaller than at the
	// default 100, for a collection every one to three seconds. The limit is
	// the backstop: it makes the collector run more often only once the live
	// heap passes about 16 MiB, well above the 10 MiB of a busy build with
	// the ancestry store full.
	debug.SetMemoryLimit(32 << 20)
	debug.SetGCPercent(50)
	// The watcher looks up no service or protocol by name, and nor does the
	// SQLite driver's C library its history writer opens the history through,
	// so it lets go of the tables that library built at start-up for such a
	// lookup (see platform).
	platform.ReleaseServiceTables()
	state, err := stateFolder()
	if err != nil {
		return
	}
	w := &watcher{
		// Only located: nothing in the folder is opened before the lock is
		// this watcher's, and, handed the watch, before the word to start.
		f:         locate(state, "watch"),
		release:   func() {},
		tracked:   map[string]command{},
		usage:     map[string]*usage{},
		seen:      map[string]seenNote{},
		passed:    map[string]bool{},
		ended:     map[string]bool{},
		lastEnded: map[procKey]uint64{},
		refusals:  map[string]int{},
		manual:    map[string]*manualStop{},
		stoppedBy: map[string]string{},

		unitsSaved:  map[string][]byte{},
		setsCallIDs: map[procKey]bool{},
		records:     &records{},
		known:       map[procKey]kind{},
		stamps:      map[procKey]cachedStamp{},
		stampAgents: map[procKey]bool{},
		classified:  map[procKey]judged{},
		ancestry:    newAncestry(platform.KnowsOriginalParent),
		unsized:     map[procKey]bool{},
		idleSince:   time.Now(),
		waiter:      platform.NewWaiter(),

		missing:     map[procKey]int{},
		unreadNotes: map[string]bool{},
		unreadPIDs:  map[int]bool{},
		handoff:     handoff{version: version},
	}
	defer w.waiter.Close()
	if !w.takeLock() {
		return
	}
	// The socket file is removed on the way out while the lock is still this
	// watcher's, except when the lock was lost: another watcher may serve
	// the path by then. A watcher that handed over carried what it had not
	// written; any other gives the writer drainWait to write it first.
	unlink, handed := true, false
	defer func() {
		w.dropHandover()
		if !handed {
			w.stopHistory()
		}
		// Checked again now, not only at the last tick: a state folder
		// replaced since then holds another watcher's socket at this path.
		w.closeServer(unlink && w.ownsLock())
		w.release()
	}()
	moved, moveErr := w.moveFolder()
	if !w.openFolder(ctx) {
		return
	}
	w.rotateOutput()
	w.f.event(slog.LevelInfo, EventStarted, "watcher started", "release", version)
	if moved {
		w.f.event(slog.LevelInfo, EventMoved, "moved the guard folder to "+w.f.root)
	}
	w.history.f = w.f
	if moveErr != nil {
		// The watcher works in the old folder as long as it runs.
		w.history.lasts("folder", slog.LevelError, EventError, fmt.Sprintf(
			"cannot move the guard folder %s to %s; the watcher works in it where it is",
			oldRoot(w.f.state), newRoot(w.f.state)), "error", moveErr)
	}
	// Only now, in the folder it stays in, so the history is never held open
	// across the folder's move (see moveFolder); a folder that stays at its
	// old path, or that openFolder followed there, keeps the history there.
	w.history.start(w.f.root)
	w.identify()
	if err := w.listen(); err != nil {
		w.listenFailed(err)
	}
	for ctx.Err() == nil {
		if !w.ownsLock() {
			w.f.event(slog.LevelWarn, EventExited, "the lock file changed; watcher exiting")
			unlink = false
			return
		}
		next := w.tick()
		if w.server != nil && !w.view.At.IsZero() {
			w.server.broadcast(w.view)
		}
		if w.handOver() {
			handed = true
			return
		}
		if w.pending != nil {
			next = min(next, handoverPoll)
		}
		if w.pending == nil && time.Since(w.idleSince) >= idleExit && !w.carryOn() {
			return
		}
		w.wait(func() bool { return ctx.Err() != nil }, next)
	}
	w.f.event(slog.LevelInfo, EventExited, "stopped by a signal; watcher exiting")
}

// rotateOutput bounds the watcher's output file (see [operation.RotateOutput])
// and, when this watcher writes to it, starts it with the output record,
// which times the lines the file gets after it (see [ReadLog]).
func (w *watcher) rotateOutput() {
	detached, err := operation.RotateOutput(w.f.output(), logLimit)
	if err != nil {
		w.f.event(slog.LevelError, EventError, "cannot rotate the watcher's output file",
			"error", err)
	}
	if detached {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Info("watcher output",
			"event", EventOutput, "part", "watch", "pid", os.Getpid())
	}
}

// openFolder opens the guard folder whose lock this watcher holds (see
// folders.open), once the lock is its own and, handed the watch, once it has
// read the word to start, so the watcher before it has let go. The folder's
// path is the one Root gave before the lock, or the new one moveFolder moved
// it to; what fails there is a check of the folder (not private, a link, a
// file system that is not the machine's own, another user's) or a read the
// system refused for now (out of memory or file descriptors). A watcher that
// gave up then would leave every note without one, and lose what it was
// handed, until the next pre hook, whose own open meets the same folder. So
// it keeps the lock and what it was handed, logs why once, and tries again
// each second until it can, with the folder where Root says when the lock
// there is the one it holds, so a folder found at another path is followed.
// It gives up only when ctx ends or the lock file is no longer the one it
// locked, as the watch does, and reports whether it opened the folder.
func (w *watcher) openFolder(ctx context.Context) bool {
	failed := false
	for {
		err := w.f.open()
		switch {
		case err == nil && failed:
			w.f.event(slog.LevelInfo, EventError, "the guard folder can be opened again")
			fallthrough
		case err == nil:
			return true
		case !failed:
			failed = true
			w.f.event(
				slog.LevelError,
				EventError,
				"cannot open the guard folder; the watcher keeps its lock and tries again each second",
				"error",
				err,
			)
		}
		w.wait(func() bool { return ctx.Err() != nil }, time.Second)
		if ctx.Err() != nil {
			w.f.event(slog.LevelInfo, EventExited, "stopped by a signal; watcher exiting")
			return false
		}
		if g := locate(w.f.state, w.f.part); g.root != w.f.root {
			if info, err := os.Stat(g.lock()); err == nil && os.SameFile(info, w.lockInfo) {
				g.logger = w.f.logger
				w.f = g
			}
		}
		if !w.ownsLock() {
			w.f.event(slog.LevelWarn, EventExited, "the lock file changed; watcher exiting")
			return false
		}
	}
}

// lock takes the lock in the guard folder anew, making the folder first only
// at its new path. A lock it finds held it tries again for about 100 ms
// before it gives up (see [operation.TakeLockIn]): whatever asks whether a
// watcher runs (each pre hook, doctor, migration 0007, Root) takes a shared
// lock on a free one for an instant, and a watcher that met that instant
// would otherwise exit and leave the notes without one until the next hook.
func (w *watcher) lock() bool {
	if w.f.makeRoot() != nil {
		return false
	}
	f, held, err := operation.TakeLockIn(w.f.lock())
	if err != nil || held {
		return false
	}
	info, err := os.Stat(w.f.lock())
	if err != nil {
		_ = f.Close()
		return false
	}
	w.lockFile, w.release, w.lockInfo = f, func() { _ = f.Close() }, info
	return true
}

// ownsLock reports whether the lock file is still the one this watcher
// locked (same device and inode).
func (w *watcher) ownsLock() bool {
	info, err := os.Stat(w.f.lock())
	return err == nil && os.SameFile(info, w.lockInfo)
}

// carryOn releases the lock at idle exit, then reads the notes once more. A
// live note written meanwhile, a note of a call that ended meanwhile, which
// it leaves for the watcher that holds the lock to put into the history (see
// letGo), or a process list or notes folder it cannot read, makes it take the
// lock again and carry on, unless another watcher took it first; so a note
// written as a watcher stops is never left without one.
// Only notes that parse and whose agent runs count, so a broken note cannot
// keep the watcher alive. It logs why it does not carry on. The socket is
// closed and its file removed before the lock goes, and served anew once the
// lock is taken again; so is the history writer stopped and started again,
// since only the lock's holder writes the history.
func (w *watcher) carryOn() bool {
	w.closeServer(true)
	w.inheritedListener = nil
	w.history.drain(drainWait)
	w.release()
	w.release = func() {}
	if s, err := w.snapshot(); err == nil {
		w.letGo = true
		notes, err := w.notes(s)
		w.letGo = false
		if err == nil && len(notes) == 0 {
			text := fmt.Sprintf("idle for %.0f minutes; watcher exiting", idleExit.Minutes())
			w.f.event(slog.LevelInfo, EventIdle, text)
			return false
		}
	}
	if !w.lock() {
		w.f.event(slog.LevelInfo, EventIdle,
			"idle, and the lock is held by another watcher or cannot be taken; watcher exiting")
		return false
	}
	w.idleSince = time.Now()
	w.history.start(w.f.root)
	if err := w.listen(); err != nil {
		w.listenFailed(err)
	}
	return true
}

// stopHistory gives the history writer drainWait to write the finished
// commands it has not written, as the watcher exits, and logs how many it did
// not write, and how many were dropped before it could, when any.
func (w *watcher) stopHistory() {
	left := w.history.drain(drainWait)
	w.takeWritten()
	if left > 0 || w.dropped > 0 {
		w.f.event(
			slog.LevelWarn,
			EventError,
			fmt.Sprintf(
				"finished commands not written to the guard history as the watcher exits: %d",
				left+w.dropped,
			),
			"unwritten",
			left,
			"dropped",
			w.dropped,
			"error",
			w.history.failure(),
		)
	}
}
