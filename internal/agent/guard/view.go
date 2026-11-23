package guard

// This file builds the snapshot a tick sends the clients of the watcher's socket.

import (
	"cmp"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// shownCommand is pid's command line as the view may show it: the form that
// never holds what might be an environment string (see platform.ShownArgs),
// ending in " …" when that form left something out, so a shortened line is
// not read as the whole command; name, the process name, stands for the
// line when nothing of it is left. Never logged.
func shownCommand(pid int, name string) string {
	args, cut := platform.ShownArgs(pid)
	line := strings.Join(args, " ")
	if !cut {
		return line
	}
	return cmp.Or(line, name) + " …"
}

// commandView is a tracked command as a snapshot shows it, in state. A unit
// shows its kind, its root, the call that started it when that resolved, and
// its command line in the form that never holds an environment string (see
// shownCommand), which the view alone shows.
func (w *watcher) commandView(id string, c command, state string) CommandView {
	v := CommandView{
		ID: id, Agent: string(c.Note.Kind), Subagent: c.Note.Subagent, Project: c.Note.Project,
		Cwd: c.Note.Cwd, Command: c.Note.Command, State: state, DryRun: c.Note.DryRun,
		Started: c.Note.NotedAt,
	}
	if c.Shown != "" {
		v.Command = c.Shown
	}
	if c.Detached != nil {
		v.Kind, v.PID, v.LaunchedBy = "detached", c.Root.PID, c.Detached.Call
	}
	if state == stateStopped || state == stateStopping {
		v.By = w.stoppedBy[id]
	}
	if u := w.usage[id]; u != nil {
		// A copy: the buckets change in place at the next reading, while a
		// client may still be writing this snapshot.
		v.Peak, v.History = u.Peak, slices.Clone(u.Buckets)
		if state != stateDone && state != stateStopped {
			v.Memory = u.Last
		}
	}
	return v
}

// finish is finishAt for a command that ended now.
func (w *watcher) finish(v CommandView, u *usage) { w.finishAt(v, u, time.Now()) }

// finishAt puts a command that ended at ended, v with its usage u (nil for
// one never measured, which keeps no graph and no processes), on the list of
// those the history does not hold yet and hands it to the history writer,
// without waiting on it: its graph, its processes, the largest peak first, at
// most processesKept of them, and its command cut at commandKept bytes, as the
// history keeps them. A Finished is never changed once made: the writer and
// the snapshots share it.
func (w *watcher) finishAt(v CommandView, u *usage, ended time.Time) {
	f := Finished{CommandView: v}
	f.Ended = ended
	f.PID, f.Held, f.Memory, f.History, f.Tree = 0, "", 0, nil, nil
	f.Command, f.Cut = keptCommand(f.Command)
	if u != nil {
		f.Graph = slices.Clone(u.Buckets)
		f.Processes, f.Unlisted = u.peaks()
	}
	w.addFinished(f)
}

// addFinished puts f on the list of finished commands the history does not
// hold yet and hands it to the writer. Past pendingLimit the oldest are
// dropped: the first drop since the list was last empty is logged, and the
// count once the list is empty again (see takeWritten).
func (w *watcher) addFinished(f Finished) {
	w.history.submit(f)
	var dropped int
	w.unwritten, dropped = keepWithin(append(w.unwritten, f), pendingLimit)
	if dropped == 0 {
		return
	}
	if w.dropped == 0 {
		w.f.event(slog.LevelWarn, EventError, fmt.Sprintf(
			"the finished commands the guard history has not taken are over %d MiB; "+
				"dropping the oldest, which are not kept", pendingLimit>>20),
			"dropped", dropped, "error", w.history.failure())
	}
	w.dropped += dropped
}

// takeWritten takes the commands the writer has written off the list of
// those the history does not hold yet, and logs how many were dropped once
// the list is empty again.
func (w *watcher) takeWritten() {
	written := w.history.done()
	if len(written) == 0 {
		return
	}
	ids := make(map[string]bool, len(written))
	for _, id := range written {
		ids[id] = true
	}
	w.unwritten = slices.DeleteFunc(w.unwritten, func(f Finished) bool { return ids[f.ID] })
	if len(w.unwritten) == 0 && w.dropped > 0 {
		w.f.event(slog.LevelWarn, EventError, fmt.Sprintf(
			"finished commands dropped before the guard history could take them: %d",
			w.dropped), "dropped", w.dropped)
		w.dropped = 0
	}
}

// historyError is what the snapshot says of the history at now (see
// Snapshot.HistoryError).
func (w *watcher) historyError(now time.Time) string {
	if failure := w.history.failure(); failure != "" {
		return "Guard history cannot be written: " + failure
	}
	at, copied := w.history.damaged()
	if at.IsZero() {
		return ""
	}
	clock := at.Format("3:04 PM")
	if y, m, d := at.Date(); y != now.Year() || m != now.Month() || d != now.Day() {
		clock = at.Format("Jan 2 3:04 PM")
	}
	return "Guard history was damaged and started again at " + clock + "; the old file is " + copied
}

// buildView is the snapshot of this tick: s and m, the tracked commands with
// their trees as measure read them, a unit that runs with why the guard
// itself will not choose it now (see held), and the finished commands the
// history does not hold yet, after taking off those the writer has written
// since the last tick, with what there is to say of the history.
func (w *watcher) buildView(s snapshot, m memory, trees map[string][]ProcessView) Snapshot {
	now := time.Now()
	w.takeWritten()
	var running []CommandView
	for id, c := range w.tracked {
		state := stateRunning
		switch {
		case w.manual[id] != nil:
			state = stateStopping
		case !live(s, c) && w.stoppedBy[id] != "":
			state = stateStopped
		case !live(s, c):
			state = stateDone
		}
		v := w.commandView(id, c, state)
		if c.Detached != nil {
			v.PID = firstRoot(s, c).PID
			if live(s, c) {
				v.Held = held(
					c.Detached,
					now,
					func(r detachedRoot) bool { return s.alive(r.PID, r.Start) },
				)
			}
		}
		v.Tree = trees[id]
		running = append(running, v)
	}
	slices.SortFunc(running, func(a, b CommandView) int {
		return cmp.Or(b.Started.Compare(a.Started), cmp.Compare(a.ID, b.ID))
	})
	return Snapshot{
		At: now, Memory: memoryView(m), LargestPeak: w.largestPeak, Commands: running,
		// A copy: the list changes in place, while a client may still be
		// writing this snapshot.
		Pending:      slices.Clone(w.unwritten),
		HistoryError: w.historyError(now),
	}
}

// treeView is a command's processes with the resident sizes read for them; a
// zombie or a process not read shows 0.
func treeView(procs []proc, sizes map[int]uint64) []ProcessView {
	out := make([]ProcessView, 0, len(procs))
	for _, p := range procs {
		out = append(out, ProcessView{PID: p.PID, Name: p.Name, Memory: sizes[p.PID]})
	}
	return out
}

// residentInto reads a process's resident size as platform.Resident does and
// keeps it in sizes by PID.
func residentInto(sizes map[int]uint64) func(pid int) (uint64, error) {
	return func(pid int) (uint64, error) {
		bytes, err := platform.Resident(pid)
		sizes[pid] = bytes
		return bytes, err
	}
}
