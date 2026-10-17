package guard

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/operation"
)

const (
	idleExit  = 10 * time.Minute
	cooldown  = 10 * time.Second
	killAfter = 5 * time.Second
	giveUp    = 10 * time.Second
	killKeep  = 24 * time.Hour
	// childLinger is how long a child of an agent must be seen alive before it
	// may stand for a waiting note's command: one tick at the normal pace.
	childLinger = time.Second
	// abandonAfter is how long a note that never matched must wait before a
	// later call of its agent ending lets it go: Claude Code's longest
	// foreground Bash timeout, so a call still waiting at a permission prompt
	// beside a parallel call that finished keeps its note.
	abandonAfter = 10 * time.Minute

	refusalLimit = 3
)

// seenNote is a note the watcher has read: its command, agent with its kind
// and NotedStart, read when this watcher first read it, matched once it was
// tracked, childSeen as markChildSeen sets it, and posted and postedRound
// from the first tick it was read marked posted. One that goes away unmatched
// with childSeen is logged.
type seenNote struct {
	command     string
	agent       procKey
	kind        kind
	noted       uint64
	read        time.Time
	matched     bool
	childSeen   bool
	posted      time.Time
	postedRound int
}

type watcher struct {
	f         folders
	release   func()
	lockFile  *os.File
	lockInfo  os.FileInfo
	tracked   map[string]command
	seen      map[string]seenNote
	stopped   []proc    // the last stopped command's processes
	quietFrom time.Time // when they were all gone
	// passed are commands never chosen again: a dry-run one already logged,
	// one still running after a stop gave up on it, or one whose stops kept
	// signalling nothing.
	passed map[string]bool
	// refusals counts a call's consecutive stops that signalled nothing while
	// its command still ran.
	refusals map[string]int
	// ended are calls whose tracked command has ended. A call runs once, so
	// its note, which stays until the post hook, the agent's exit or its
	// abandonment (see notes), is never matched again.
	ended map[string]bool
	// lastEnded is, for each agent, the latest NotedStart of its notes that
	// went away while this watcher ran.
	lastEnded map[procKey]uint64
	// setsCallIDs are the Codex agents seen with a child carrying a call ID
	// of its own (Codex 0.163.0 and later), while they run.
	setsCallIDs map[procKey]bool
	idleSince   time.Time
	expired     time.Time
	waiter      *waiter
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
	// agent of a note, a tracked command's root) was first absent in.
	round   int
	missing map[procKey]int
	// unreadNotes and unreadPIDs are notes and processes that could not be
	// read, so each is logged once, not every tick.
	unreadNotes map[string]bool
	unreadPIDs  map[int]bool
	// calm is set by a tick that read memory calling for nothing, with no
	// hold under way: a moment a handover may happen at.
	calm bool
	handoff
}

// procKey names one process: its PID and start time.
type procKey struct {
	pid   int
	start uint64
}

// Watch is the watcher, at most one per user, started detached by the pre
// hook. It finds each noted command, watches machine memory, and near
// out-of-memory stops the largest agent command and records why. It returns
// after ten minutes with no note and no tracked command, when its lock file is
// no longer the one it locked, once it has handed over to another release's
// watcher (see handOver), or at its next tick after ctx ends; a stop under way
// finishes first. It has no terminal and logs only to the guard log. It works
// from /, so it never holds the folder of the session that started it. version
// is the release it runs, for the log.
func Watch(ctx context.Context, version string) {
	debug.SetMemoryLimit(32 << 20)
	f, err := openFolders("watch")
	if err != nil {
		return
	}
	w := &watcher{
		f:         f,
		release:   func() {},
		tracked:   map[string]command{},
		seen:      map[string]seenNote{},
		passed:    map[string]bool{},
		ended:     map[string]bool{},
		lastEnded: map[procKey]uint64{},
		refusals:  map[string]int{},

		setsCallIDs: map[procKey]bool{},
		unsized:     map[procKey]bool{},
		idleSince:   time.Now(),
		waiter:      newWaiter(),

		missing:     map[procKey]int{},
		unreadNotes: map[string]bool{},
		unreadPIDs:  map[int]bool{},
		handoff:     handoff{version: version},
	}
	defer w.waiter.close()
	if !w.takeLock() {
		return
	}
	defer func() {
		w.dropHandover()
		w.release()
	}()
	detached, err := operation.RotateOutput(f.output(), logLimit)
	if err != nil {
		f.event(
			slog.LevelError,
			EventError,
			"cannot rotate the watcher's output file",
			"error",
			err,
		)
	}
	if detached {
		// The output record times the lines the output file gets after it
		// (see [ReadLog]).
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Info("watcher output",
			"event", EventOutput, "part", "watch", "pid", os.Getpid())
	}
	f.event(slog.LevelInfo, EventStarted, "watcher started", "release", version)
	w.identify()
	for ctx.Err() == nil {
		if !w.ownsLock() {
			f.event(slog.LevelWarn, EventExited, "the lock file changed; watcher exiting")
			return
		}
		next := w.tick()
		if w.handOver() {
			return
		}
		if w.pending != nil {
			next = min(next, handoverPoll)
		}
		if w.pending == nil && time.Since(w.idleSince) >= idleExit && !w.carryOn() {
			return
		}
		w.waiter.wait(next)
	}
	f.event(slog.LevelInfo, EventExited, "stopped by a signal; watcher exiting")
}

func (w *watcher) lock() bool {
	f, held, err := operation.TryLockFile(w.f.lock())
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
// live note written meanwhile, or a process list or notes folder it cannot
// read, makes it take the lock again and carry on, unless another watcher took
// it first; so a note written as a watcher stops is never left without one.
// Only notes that parse and whose agent runs count, so a broken note cannot
// keep the watcher alive. It logs why it does not carry on.
func (w *watcher) carryOn() bool {
	w.release()
	w.release = func() {}
	if s, err := w.snapshot(); err == nil {
		if notes, err := w.notes(s); err == nil && len(notes) == 0 {
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
	return true
}

// tick runs one round and returns the time to the next. When the process
// list or the notes folder cannot be read, it does nothing else this round.
func (w *watcher) tick() time.Duration {
	w.round++
	w.calm = false
	// Entries from before the last round were acted on or are no longer checked.
	for key, first := range w.missing {
		if first < w.round-1 {
			delete(w.missing, key)
		}
	}
	s, err := w.snapshot()
	if err != nil {
		w.f.event(slog.LevelError, EventError, "cannot list processes", "error", err)
		return time.Second
	}
	notes, err := w.notes(s)
	if err != nil {
		w.f.event(slog.LevelError, EventError, "cannot read the notes", "error", err)
		return time.Second
	}
	w.forget(s)
	if w.inherited != nil {
		w.adopt(s, notes)
		w.inherited = nil
	}
	agents, protected := w.agents(s, notes)
	codexAgents := map[int]bool{}
	for _, n := range notes {
		codexAgents[n.AgentPID] = codexAgents[n.AgentPID] || n.Kind == codex
	}
	for key := range w.setsCallIDs {
		if !s.alive(key.pid, key.start) {
			delete(w.setsCallIDs, key)
		}
	}
	for _, agent := range agents {
		w.waiter.watch(agent)
		loadArgs(s, agent, codexAgents[agent])
		if !codexAgents[agent] {
			continue
		}
		for _, child := range s.children(agent) {
			if child.CallID != "" && child.CallID != s[agent].CallID {
				w.setsCallIDs[procKey{agent, s[agent].Start}] = true
			}
		}
	}
	taken := map[int]bool{}
	for _, c := range w.tracked {
		taken[c.Root.PID] = true
	}
	for _, c := range match(s, w.untracked(notes), taken, w.setsCallIDs) {
		taken[c.Root.PID] = true
		w.tracked[c.Note.ToolUseID] = c
		seen := w.seen[c.Note.ToolUseID]
		seen.matched = true
		w.seen[c.Note.ToolUseID] = seen
		w.f.event(slog.LevelInfo, EventTracking, "tracking", callAttrs(c)...)
	}
	w.markChildSeen(s, agents, notes, taken)
	if len(notes) > 0 || len(w.tracked) > 0 {
		w.idleSince = time.Now()
	}
	w.expireKills()
	m, err := readMemory()
	if err != nil {
		w.f.event(slog.LevelError, EventError, "cannot read memory", "error", err)
		return time.Second
	}
	act := w.demand(m, time.Now())
	if act == nothing {
		w.underFloor = false
	}
	w.calm = act == nothing && w.softSince.IsZero() && w.criticalSince.IsZero()
	if !w.coolingDown(s) && act != nothing && len(w.tracked) > 0 {
		w.stopLargest(s, protected, act, m)
	}
	return delay(m)
}

// demand is what memory calls for at now, after the holds that keep a brief
// dip from stopping anything: a soft condition calls for a polite stop only once it has lasted softHold,
// and macOS's critical level calls for a forced stop once it has lasted
// criticalHold.
func (w *watcher) demand(m memory, now time.Time) action {
	act, soft := decide(m)
	if !soft {
		w.softSince = time.Time{}
	} else if w.softSince.IsZero() {
		w.softSince = now
	}
	if soft && now.Sub(w.softSince) < softHold {
		act = nothing
	}
	if m.Level < criticalLevel {
		w.criticalSince = time.Time{}
	} else if w.criticalSince.IsZero() {
		w.criticalSince = now
	}
	if m.Level >= criticalLevel && now.Sub(w.criticalSince) >= criticalHold {
		act = forced
	}
	return act
}

// notes reads the notes on disk. A note that cannot be read (for want of
// memory or file descriptors, say) is left for a later tick, its state kept. A
// note that does not parse is deleted: the pre hook renames a complete file
// into place, so a broken one never heals. So is a note whose agent (PID and
// start time) is gone (see gone). A posted note, a background call's that its
// post hook kept, is deleted once its command has ended or, when no command
// matched it, childLinger after it was first read posted and once match has
// looked for it in two rounds: its command started before the post hook, so a
// later process is never its command. A note that
// was never posted and whose command never matched or has ended is
// abandoned, and deleted, once a note
// its agent wrote later has gone and abandonAfter has passed since it was
// first read: a call that was denied or cut off gets no post hook, and its
// note would otherwise keep the watcher running for as long as its agent
// does. A note that goes away without ever being matched is logged.
func (w *watcher) notes(s snapshot) ([]note, error) {
	ids, err := w.f.noteIDs()
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	var notes []note
	now := time.Now()
	for _, id := range ids {
		path := w.f.note(id)
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			present[id] = true
			if !w.unreadNotes[id] {
				w.unreadNotes[id] = true
				w.f.event(slog.LevelError, EventError, "cannot read a note",
					"call", id, "error", err)
			}
			continue
		}
		delete(w.unreadNotes, id)
		var n note
		if json.Unmarshal(data, &n) != nil || n.ToolUseID != id ||
			w.gone(s, n.AgentPID, n.AgentStart) {
			_ = os.Remove(path)
			continue
		}
		seen, ok := w.seen[id]
		if !ok {
			seen = seenNote{
				command: n.Command,
				agent:   procKey{n.AgentPID, n.AgentStart},
				kind:    n.Kind,
				noted:   n.NotedStart,
				read:    now,
			}
		}
		if n.Posted && seen.posted.IsZero() {
			seen.posted, seen.postedRound = now, w.round
		}
		w.seen[id] = seen
		if n.Posted && (w.ended[id] || !seen.matched && now.Sub(seen.posted) >= childLinger &&
			w.round > seen.postedRound+1) {
			_ = os.Remove(path)
			continue
		}
		if !n.Posted && (!seen.matched || w.ended[id]) && n.NotedStart < w.lastEnded[seen.agent] &&
			now.Sub(seen.read) >= abandonAfter {
			w.f.event(slog.LevelWarn, EventAbandoned,
				"abandoned without a post hook after a later call of its agent ended",
				"call", id, "agent", n.Kind, "command", clip(n.Command))
			_ = os.Remove(path)
			delete(w.seen, id)
			delete(w.ended, id)
			continue
		}
		present[id] = true
		notes = append(notes, n)
	}
	for id, seen := range w.seen {
		if !present[id] {
			w.lastEnded[seen.agent] = max(w.lastEnded[seen.agent], seen.noted)
			if !seen.matched && seen.childSeen {
				w.f.event(slog.LevelInfo, EventUnmatched, "ended without matching a command",
					"call", id, "agent", seen.kind, "command", clip(seen.command))
			}
			delete(w.seen, id)
			delete(w.ended, id)
		}
	}
	for id := range w.unreadNotes {
		if !present[id] {
			delete(w.unreadNotes, id)
		}
	}
	for key := range w.lastEnded {
		if !s.alive(key.pid, key.start) {
			delete(w.lastEnded, key)
		}
	}
	return notes, nil
}

// gone reports whether the process pid with this start time is absent from s
// and was already absent in an earlier round. A Linux snapshot leaves out a
// process whose /proc entry could not be read, so one absent reading never
// deletes an agent's notes or forgets a tracked command.
func (w *watcher) gone(s snapshot, pid int, start uint64) bool {
	key := procKey{pid, start}
	if s.alive(pid, start) {
		delete(w.missing, key)
		return false
	}
	first, ok := w.missing[key]
	if !ok {
		w.missing[key] = w.round
	}
	return ok && first < w.round
}

// snapshot is snapshotNow, logging once each process left out for a reason
// other than its exit.
func (w *watcher) snapshot() (snapshot, error) {
	return snapshotNow(func(pid int, err error) {
		if !w.unreadPIDs[pid] {
			w.unreadPIDs[pid] = true
			w.f.event(slog.LevelError, EventError, "cannot read a process",
				"process", pid, "error", err)
		}
	})
}

// exited reports whether err from reading a process means only that it
// exited, which is no error.
func exited(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH)
}

// callAttrs are the log attributes of a tracked call.
func callAttrs(c command) []any {
	return []any{
		"call", c.Note.ToolUseID, "agent", c.Note.Kind, "root_pid", c.Root.PID,
		"command", clip(c.Note.Command),
	}
}

// markChildSeen sets childSeen on each waiting note whose agent has a child
// that started after the note, is not in taken (a tracked command's root) and
// has been seen alive, with the same start time, at every tick for at least
// childLinger: its command may have run in a shape match does not know. The
// span is in time, not ticks, because ticks quicken to 100 ms under memory
// pressure. The pre hook's own shell started before NotedStart and never
// counts.
func (w *watcher) markChildSeen(s snapshot, agents []int, notes []note, taken map[int]bool) {
	before, now := w.agentChildren, time.Now()
	w.agentChildren = map[procKey]time.Time{}
	for _, agent := range agents {
		for _, p := range s.children(agent) {
			if key := (procKey{p.PID, p.Start}); !p.Zombie {
				w.agentChildren[key] = cmp.Or(before[key], now)
			}
		}
	}
	for _, n := range w.untracked(notes) {
		seen := w.seen[n.ToolUseID]
		if !seen.childSeen && slices.ContainsFunc(s.children(n.AgentPID), func(p proc) bool {
			first, ok := w.agentChildren[procKey{p.PID, p.Start}]
			return ok && p.Start > n.NotedStart && now.Sub(first) >= childLinger && !taken[p.PID]
		}) {
			seen.childSeen = true
			w.seen[n.ToolUseID] = seen
		}
	}
}

// forget stops tracking commands that ended or whose agent is gone (see
// gone). A call whose command ended is remembered until its note goes, so the
// note cannot claim a later command with the same text.
func (w *watcher) forget(s snapshot) {
	for key := range w.unsized {
		if !s.alive(key.pid, key.start) {
			delete(w.unsized, key)
		}
	}
	for id, c := range w.tracked {
		rootGone := w.gone(s, c.Root.PID, c.Root.Start)
		agentGone := w.gone(s, c.Note.AgentPID, c.Note.AgentStart)
		if rootGone || agentGone {
			delete(w.tracked, id)
			delete(w.passed, id)
			delete(w.refusals, id)
			// Only a call whose note is still on disk is remembered: when the
			// post hook already removed it, nothing can match it again and
			// notes would never clear the entry.
			if _, noted := w.seen[id]; noted && !agentGone {
				w.ended[id] = true
			}
			w.f.event(slog.LevelInfo, EventEnded, "ended", callAttrs(c)...)
		}
	}
}

// agents are the live agents of the notes and tracked commands. protected is
// them and the watcher, each PID with its start time: never signalled, nor
// anything under them.
func (w *watcher) agents(s snapshot, notes []note) ([]int, map[int]uint64) {
	protected := map[int]uint64{}
	if self, ok := s[os.Getpid()]; ok {
		protected[self.PID] = self.Start
	}
	var agents []int
	add := func(pid int, start uint64) {
		if _, ok := protected[pid]; !ok && s.alive(pid, start) {
			protected[pid] = start
			agents = append(agents, pid)
		}
	}
	for _, n := range notes {
		add(n.AgentPID, n.AgentStart)
	}
	for _, c := range w.tracked {
		add(c.Note.AgentPID, c.Note.AgentStart)
	}
	return agents, protected
}

// untracked are the notes still waiting for their command: not tracked, and
// not a call whose command already ran.
func (w *watcher) untracked(notes []note) []note {
	var out []note
	for _, n := range notes {
		if _, ok := w.tracked[n.ToolUseID]; !ok && !w.ended[n.ToolUseID] {
			out = append(out, n)
		}
	}
	return out
}

// expireKills deletes kill records over a day old, at most once a minute,
// and stale temporary files (see listFolder).
func (w *watcher) expireKills() {
	if time.Since(w.expired) < time.Minute {
		return
	}
	w.expired = time.Now()
	entries, _ := listFolder(w.f.killed)
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > killKeep {
			_ = os.Remove(filepath.Join(w.f.killed, entry.Name()))
		}
	}
}

// coolingDown is true from a stop until the stopped command's processes are
// gone and ten more seconds have passed, so the pressure figures catch up.
func (w *watcher) coolingDown(s snapshot) bool {
	if w.stopped == nil {
		return false
	}
	for _, p := range w.stopped {
		if s.alive(p.PID, p.Start) {
			w.quietFrom = time.Time{}
			return true
		}
	}
	if w.quietFrom.IsZero() {
		w.quietFrom = time.Now()
	}
	if time.Since(w.quietFrom) < cooldown {
		return true
	}
	w.stopped = nil
	return false
}

// stopLargest stops the tracked command with the largest footprint over its
// tree (without protected processes); a tie goes to the older command. A
// passed call is left out: a dry-run one already logged, one a stop gave up
// on, or one whose stops signalled nothing three times in a row. So is one
// whose root is absent from this snapshot. A polite stop leaves the commands
// running when together they hold less than the memory floor, and logs that
// once a pressure episode.
func (w *watcher) stopLargest(s snapshot, protected map[int]uint64, act action, m memory) {
	skip := outside(protected)
	var chosen command
	var chosenSize, total uint64
	var members []proc
	for id, c := range w.tracked {
		if w.passed[id] || !s.alive(c.Root.PID, c.Root.Start) {
			continue
		}
		tree := s.tree(c.Root.PID, skip)
		var size uint64
		for _, p := range tree {
			if p.Zombie {
				continue
			}
			bytes, err := footprint(p.PID)
			if key := (procKey{p.PID, p.Start}); err != nil && !exited(err) && !w.unsized[key] {
				w.unsized[key] = true
				w.f.event(
					slog.LevelError,
					EventError,
					"cannot read a process's size (counted as 0)",
					"process",
					p.PID,
					"error",
					err,
				)
			}
			size += bytes
		}
		total += size
		if members == nil || size > chosenSize ||
			size == chosenSize && c.Root.Start < chosen.Root.Start {
			chosen, chosenSize, members = c, size, tree
		}
	}
	switch {
	case len(members) == 0:
	case act == polite && total < m.floor():
		if !w.underFloor {
			w.underFloor = true
			w.f.event(slog.LevelWarn, EventMemory,
				"memory is low, but the agent commands are too small to stop",
				"size", total, "floor", m.floor(), "memory", m)
		}
	default:
		w.stop(chosen, chosenSize, len(members)-1)
	}
}

// stop re-checks memory, snapshots the command again, records the reason and
// signals it. It reads the notes again first, so an agent that left its first
// note since the tick is protected. The record is written before any signal,
// because the agent's post hook may run the moment the command dies; it is
// deleted when nothing was signalled. With WORKBENCH_GUARD=dry-run on the
// note it only logs.
func (w *watcher) stop(c command, size uint64, children int) {
	m, err := readMemory()
	if err != nil {
		w.f.event(slog.LevelError, EventError, "cannot read memory; not stopping",
			append(callAttrs(c), "error", err)...)
		return
	}
	if act, _ := decide(m); act == nothing {
		w.f.event(slog.LevelInfo, EventMemory, "memory recovered before the stop",
			append(callAttrs(c), "memory", m)...)
		return
	}
	s, err := w.snapshot()
	if err != nil {
		return
	}
	notes, err := w.notes(s)
	if err != nil {
		w.f.event(slog.LevelError, EventError, "cannot read the notes; not stopping",
			append(callAttrs(c), "error", err)...)
		return
	}
	_, protected := w.agents(s, notes)
	chosen := targets(s, c.Root, protected)
	if len(chosen) == 0 {
		// Nothing to signal: the root is gone or changed (the next tick stops
		// tracking it) or is protected. Counted like a refused stop, so a
		// command that can never be stopped is not chosen every tick.
		w.refused(c)
		return
	}
	text := reason(c.Note.Command, children, size, m)
	stopping := append(callAttrs(c),
		"size", size, "children", children, "reason", text, "memory", m)
	if c.Note.DryRun {
		w.passed[c.Note.ToolUseID] = true
		w.f.event(slog.LevelWarn, EventStopping, "would stop (dry run)",
			append(stopping, "dry_run", true)...)
		return
	}
	w.stopped = s.tree(c.Root.PID, outside(protected))
	w.quietFrom = time.Time{}
	record := w.f.record(c.Note.ToolUseID)
	if err = writeJSON(
		record,
		kill{ToolUseID: c.Note.ToolUseID, Reason: text, At: time.Now()},
	); err != nil {
		// The machine comes first: the stop goes ahead and the agent gets no reason.
		w.f.event(slog.LevelError, EventError,
			"cannot record the stop; stopping anyway, the agent will be told nothing",
			append(callAttrs(c), "error", err)...)
	}
	w.f.event(slog.LevelWarn, EventStopping, "stopping", stopping...)
	if w.signal(chosen, c) {
		delete(w.refusals, c.Note.ToolUseID)
		return
	}
	_ = os.Remove(record)
	// Nothing was signalled, so there is nothing to wait for: no cooldown.
	w.stopped = nil
	w.refused(c)
}

// refused handles a stop that signalled nothing. A command that is gone, or
// that cannot be checked, is no refusal: a transient failure (out of memory
// or file descriptors) must not pass over the largest command for good. One
// still running with its start time refused the signal (a root-owned process,
// say); after three such stops in a row the call is passed over, so it is not
// retried every tick.
func (w *watcher) refused(c command) {
	id := c.Note.ToolUseID
	s, err := w.snapshot()
	if err != nil || !s.alive(c.Root.PID, c.Root.Start) {
		w.f.event(slog.LevelInfo, EventRefused,
			"nothing signalled: the command ended or could not be checked", callAttrs(c)...)
		return
	}
	w.refusals[id]++
	if w.refusals[id] < refusalLimit {
		text := fmt.Sprintf("running, but nothing could be signalled (%d of %d)",
			w.refusals[id], refusalLimit)
		w.f.event(slog.LevelWarn, EventRefused, text, callAttrs(c)...)
		return
	}
	w.passed[id] = true
	w.f.event(slog.LevelWarn, EventRefused,
		fmt.Sprintf("refused %d stops in a row; not chosen again", refusalLimit), callAttrs(c)...)
}

// signal sends SIGTERM, then SIGKILL once memory calls for a forced stop or
// after five seconds, polling every 100 ms until the command's processes are
// gone, and gives up after ten seconds. It reports whether any live target
// was signalled.
func (w *watcher) signal(chosen []target, c command) bool {
	sent := false
	for _, t := range chosen {
		sent = send(t, unix.SIGTERM) || sent
	}
	if !sent {
		return false
	}
	killed := false
	for start := time.Now(); time.Since(start) < giveUp; time.Sleep(100 * time.Millisecond) {
		if s, err := w.snapshot(); err == nil && !w.anyAlive(s) {
			return true
		}
		if !killed && w.escalateNow(start) {
			if killed = w.escalate(); killed {
				w.f.event(slog.LevelWarn, EventForced, "sent SIGKILL", callAttrs(c)...)
			}
		}
	}
	w.abandon(c)
	return true
}

// escalateNow is true after five seconds or once memory calls for a forced
// stop; memory is read only until then.
func (w *watcher) escalateNow(start time.Time) bool {
	if time.Since(start) >= killAfter {
		return true
	}
	m, err := readMemory()
	return err == nil && w.demand(m, time.Now()) == forced
}

// escalate sends SIGKILL, by PID, once to each process escalation chooses. It
// does not signal the group: a shell that died of SIGTERM was the group
// leader, and once it is reaped the group is no longer known to be the
// command's, while a child that ignored SIGTERM lives on under PID 1. It
// reports false when the processes or notes could not be read, so the next
// poll tries again.
func (w *watcher) escalate() bool {
	s, err := w.snapshot()
	if err != nil {
		return false
	}
	notes, err := w.notes(s)
	if err != nil {
		return false
	}
	_, protected := w.agents(s, notes)
	kill, wait := escalation(s, w.stopped, protected)
	w.stopped = wait
	for _, p := range kill {
		if !p.Zombie {
			send(target{PID: p.PID, Start: p.Start}, unix.SIGKILL)
		}
	}
	return true
}

// escalation chooses, from one snapshot, what escalate kills: every stopped
// process still alive and anything started since under one, leaving out
// protected processes (the agents of the notes on disk now, the watcher) with
// everything under them, and each stopped process that is protected now or
// sits under one that is (see dropProtected). wait is the stopped processes to
// wait for: those kept, and the new ones. A process started under one that has
// since exited is out of reach: nothing links it to the command any more.
func escalation(s snapshot, stopped []proc, protected map[int]uint64) (kill, wait []proc) {
	wait = dropProtected(slices.Clone(stopped), protected)
	known := map[int]bool{}
	for _, p := range wait {
		known[p.PID] = true
	}
	// The stopped processes' trees overlap, so a process already collected is
	// left out with everything under it.
	collected := map[int]bool{}
	excluded := outside(protected)
	skip := func(q proc) bool { return excluded(q) || collected[q.PID] }
	for _, p := range wait {
		if !s.alive(p.PID, p.Start) {
			continue
		}
		for _, q := range s.tree(p.PID, skip) {
			collected[q.PID] = true
			kill = append(kill, q)
		}
	}
	for _, p := range kill {
		if !known[p.PID] {
			known[p.PID] = true
			wait = append(wait, p)
		}
	}
	return kill, wait
}

// abandon gives up on a stopped command that is still running after ten
// seconds, such as one in uninterruptible I/O: it logs what survived, ends
// the cooldown and passes the call over from now on, so one process that
// cannot be stopped never keeps the watcher from acting again.
func (w *watcher) abandon(c command) {
	var left []int
	if s, err := w.snapshot(); err == nil {
		if notes, err := w.notes(s); err == nil {
			_, protected := w.agents(s, notes)
			for _, p := range dropProtected(w.stopped, protected) {
				if s.alive(p.PID, p.Start) {
					left = append(left, p.PID)
				}
			}
		}
	}
	text := fmt.Sprintf("still running %.0f seconds after the stop; not chosen again",
		giveUp.Seconds())
	w.f.event(slog.LevelWarn, EventGaveUp, text, append(callAttrs(c), "still_running", left)...)
	w.stopped = nil
	w.passed[c.Note.ToolUseID] = true
}

// loadArgs reads the command lines matching looks at: the agent's, its direct
// children's and, below a sandbox launcher, what the launcher started. For a
// Codex agent it also reads the CallID of the agent and its direct children;
// no other part of any environment is kept. Sizes need neither, so the rest
// of the tree is not read.
func loadArgs(s snapshot, agent int, callIDs bool) {
	seen := map[int]bool{}
	var read func(pid int, expand bool)
	read = func(pid int, expand bool) {
		p, ok := s[pid]
		if !ok || seen[pid] {
			return
		}
		seen[pid] = true
		p.Args = readArgs(pid)
		if callIDs && (pid == agent || p.PPID == agent) {
			p.CallID = readCallID(pid)
		}
		s[pid] = p
		if expand || len(p.Args) > 0 && launchers[filepath.Base(p.Args[0])] {
			for _, child := range s.children(pid) {
				read(child.PID, false)
			}
		}
	}
	read(agent, true)
}

// alive reports whether the process pid with this start time still runs; a
// zombie holds no memory and counts as gone.
func (s snapshot) alive(pid int, start uint64) bool {
	p, ok := s[pid]
	return ok && p.Start == start && !p.Zombie
}

// anyAlive reports whether a stopped process still runs, leaving out the
// agents of the notes on disk now and everything under them. Notes it cannot
// read count as a process still running.
func (w *watcher) anyAlive(s snapshot) bool {
	notes, err := w.notes(s)
	if err != nil {
		return true
	}
	_, protected := w.agents(s, notes)
	w.stopped = dropProtected(w.stopped, protected)
	for _, p := range w.stopped {
		if s.alive(p.PID, p.Start) {
			return true
		}
	}
	return false
}

// dropProtected removes, in place, from the stopped processes each one that
// is protected now and each one whose parents within them lead to one that
// is: an agent may leave its first note after its command's processes were
// taken. The cooldown then no longer waits on that agent either.
func dropProtected(stopped []proc, protected map[int]uint64) []proc {
	excluded := outside(protected)
	byPID := make(map[int]proc, len(stopped))
	for _, p := range stopped {
		byPID[p.PID] = p
	}
	underProtected := func(p proc) bool {
		for seen := map[int]bool{}; !seen[p.PID]; {
			if excluded(p) {
				return true
			}
			seen[p.PID] = true
			parent, ok := byPID[p.PPID]
			if !ok {
				return false
			}
			p = parent
		}
		return false
	}
	return slices.DeleteFunc(stopped, underProtected)
}
