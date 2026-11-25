package guard

import (
	"log/slog"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// tick runs one round and returns the time to the next. When the process
// list or the notes folder cannot be read, it does nothing else this round.
// It follows the stops people asked for and takes new ones with the same
// snapshot, finds and tracks detached units beside the noted commands (see
// findUnits), and keeps what it saw in view.
func (w *watcher) tick() time.Duration {
	w.round++
	w.calm = false
	// Entries from before the last round were acted on or are no longer checked.
	for key, first := range w.missing {
		if first < w.round-1 {
			delete(w.missing, key)
		}
	}
	s, err := w.tickSnapshot()
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
	switch {
	case w.inherited != nil:
		w.adopt(s, notes)
		w.dropStrayUnits()
		w.inherited, w.unitsRead = nil, true
	case !w.unitsRead:
		w.unitsRead = w.restoreUnits(s)
	}
	defer w.saveUnits()
	agents, protected := w.agents(s, notes)
	w.followManual(s, protected)
	w.drainStops(s, notes, protected)
	w.readAgents(s, agents, notes)
	taken := map[int]bool{}
	for _, c := range w.tracked {
		taken[c.Root.PID] = true
	}
	for _, c := range match(s, w.untracked(notes), taken, w.setsCallIDs) {
		taken[c.Root.PID] = true
		w.tracked[c.Note.ToolUseID] = c
		w.usage[c.Note.ToolUseID] = &usage{Since: time.Now()}
		w.records.tracked(c.Note.ToolUseID, c.Root)
		seen := w.seen[c.Note.ToolUseID]
		seen.matched = true
		w.seen[c.Note.ToolUseID] = seen
		w.f.event(slog.LevelInfo, EventTracking, "tracking", callAttrs(c)...)
	}
	// One parent index for the tick's trees. Units are found and judged
	// again after matching, so a command's tree is recorded before its work
	// detaches, and an agent a stamp names is protected, in protected too,
	// before anything is measured or stopped.
	if w.tickBelow == nil {
		w.tickBelow = parents{}
	}
	below := s.belowInto(w.tickBelow)
	w.recordLineage(s, below, protected)
	w.findUnits(s, below, protected)
	trees := w.measure(s, below, protected)
	w.markChildSeen(s, notes, taken)
	if len(notes) > 0 || len(w.tracked) > 0 {
		w.idleSince = time.Now()
	}
	w.expireKills()
	m, err := readMemory()
	if err != nil {
		w.f.event(slog.LevelError, EventError, "cannot read memory", "error", err)
		return time.Second
	}
	w.view = w.buildView(s, m, trees)
	act := w.demand(m, time.Now())
	if act == nothing {
		w.underFloor = false
	}
	w.calm = act == nothing && w.softSince.IsZero() && w.criticalSince.IsZero()
	if !w.coolingDown(s) && act != nothing && len(w.tracked) > 0 {
		w.stopLargest(s, below, protected, act, m)
	}
	return delay(m)
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

// snapshot is every process by PID, logging once each process left out for
// a reason other than its exit.
func (w *watcher) snapshot() (snapshot, error) {
	all, err := platform.Processes(w.unreadable)
	if err != nil {
		return nil, err
	}
	return fill(make(snapshot, len(all)), all), nil
}

// tickSnapshot is snapshot for tick alone, built in the map of the tick
// before and from the process list kept on the watcher (see tickSnap), so a
// busy machine's list is not made anew at every tick. Every other snapshot,
// such as the ones a stop takes while the tick's own is still in use, is a
// new one.
func (w *watcher) tickSnapshot() (snapshot, error) {
	all, err := w.list.Read(w.unreadable)
	if err != nil {
		return nil, err
	}
	if w.tickSnap == nil {
		w.tickSnap = make(snapshot, len(all))
	}
	return fill(w.tickSnap, all), nil
}

// unreadable logs once each process left out of a list for a reason other
// than its exit.
func (w *watcher) unreadable(pid int, err error) {
	if !w.unreadPIDs[pid] {
		w.unreadPIDs[pid] = true
		w.f.event(slog.LevelError, EventError, "cannot read a process",
			"process", pid, "error", err)
	}
}

// fill makes s hold exactly the processes of all, by PID, each with nothing
// read beyond the list: whatever s held before is cleared first, so a process
// that left, or the arguments, call ID or folder read for one, never carries
// into the new snapshot.
func fill(s snapshot, all []platform.Process) snapshot {
	clear(s)
	for _, p := range all {
		s[p.PID] = proc{Process: p}
	}
	return s
}

// forget stops tracking commands that ended or whose agent is gone (see
// gone), and units as forgetUnit says. A call whose command ended is
// remembered until its note goes, so the note cannot claim a later command
// with the same text.
func (w *watcher) forget(s snapshot) {
	for key := range w.unsized {
		if !s.alive(key.pid, key.start) {
			delete(w.unsized, key)
		}
	}
	for id, c := range w.tracked {
		if c.Detached != nil {
			w.forgetUnit(s, id, c)
			continue
		}
		rootGone := w.gone(s, c.Root.PID, c.Root.Start)
		agentGone := w.gone(s, c.Note.AgentPID, c.Note.AgentStart)
		if rootGone || agentGone {
			w.f.event(slog.LevelInfo, EventEnded, "ended", w.endAttrs(c)...)
			state := stateDone
			if w.stoppedBy[id] != "" {
				state = stateStopped
			}
			w.finish(w.commandView(id, c, state), w.usage[id])
			w.untrack(id)
			// Only a call whose note is still on disk is remembered: once notes
			// has deleted it, ended, nothing can match it again and notes would
			// never clear the entry.
			if _, noted := w.seen[id]; noted && !agentGone {
				w.ended[id] = true
			}
		}
	}
}
