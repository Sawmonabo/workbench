package guard

// This file builds the snapshot a tick sends the clients of the watcher's socket.

import (
	"cmp"
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
		v.Peak, v.History = u.Peak, slices.Clone(u.Samples)
		if n := len(u.Samples); n > 0 && state != stateDone && state != stateStopped {
			v.Memory = u.Samples[n-1]
		}
	}
	return v
}

// finish keeps a command that ended for the snapshots that follow (see
// buildView).
func (w *watcher) finish(v CommandView) {
	v.Ended, v.Memory, v.Tree = time.Now(), 0, nil
	w.finished = append(w.finished, v)
}

// buildView is the snapshot of this tick: s and m, the tracked commands with
// their trees as measure read them, a unit that runs with why the guard
// itself will not choose it now (see held), then the commands that finished
// within finishedShown, whose older ones it forgets. A finished command is
// kept past that for as long as a unit its call started shows, tracked or
// finished within finishedShown, so the view always has the command to draw
// such a unit under; it is judged again each tick, so a unit that loses its
// call lets the command go.
func (w *watcher) buildView(s snapshot, m memory, trees map[string][]ProcessView) Snapshot {
	now := time.Now()
	started := map[string]bool{}
	for _, c := range w.tracked {
		if c.Detached != nil && c.Detached.Call != "" {
			started[c.Detached.Call] = true
		}
	}
	for _, v := range w.finished {
		if v.LaunchedBy != "" && now.Sub(v.Ended) < finishedShown {
			started[v.LaunchedBy] = true
		}
	}
	w.finished = slices.DeleteFunc(w.finished, func(v CommandView) bool {
		return now.Sub(v.Ended) >= finishedShown && !started[v.ID]
	})
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
	finished := slices.Clone(w.finished)
	slices.SortFunc(finished, func(a, b CommandView) int { return b.Ended.Compare(a.Ended) })
	return Snapshot{
		At: now, Memory: memoryView(m), LargestPeak: w.largestPeak,
		Commands: slices.Concat(running, finished),
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
