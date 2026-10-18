package guard

// This file builds the snapshot a tick sends the clients of the watcher's socket.

import (
	"cmp"
	"slices"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// commandView is a tracked command as a snapshot shows it, in state.
func (w *watcher) commandView(id string, c command, state string) CommandView {
	v := CommandView{
		ID: id, Agent: string(c.Note.Kind), Subagent: c.Note.Subagent, Project: c.Note.Project,
		Cwd: c.Note.Cwd, Command: c.Note.Command, State: state, DryRun: c.Note.DryRun,
		Started: c.Note.NotedAt,
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

// finish keeps a command that ended for the snapshots of the next minute.
func (w *watcher) finish(v CommandView) {
	v.Ended, v.Memory, v.Tree = time.Now(), 0, nil
	w.finished = append(w.finished, v)
}

// buildView is the snapshot of this tick: s and m, the tracked commands with
// their trees as measure read them, and the commands that finished within
// finishedShown, whose older ones it forgets.
func (w *watcher) buildView(s snapshot, m memory, trees map[string][]ProcessView) Snapshot {
	now := time.Now()
	w.finished = slices.DeleteFunc(w.finished, func(v CommandView) bool {
		return now.Sub(v.Ended) >= finishedShown
	})
	var running []CommandView
	for id, c := range w.tracked {
		state := stateRunning
		switch {
		case w.manual[id] != nil:
			state = stateStopping
		case !s.alive(c.Root.PID, c.Root.Start) && w.stoppedBy[id] != "":
			state = stateStopped
		case !s.alive(c.Root.PID, c.Root.Start):
			state = stateDone
		}
		v := w.commandView(id, c, state)
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
		Commands: append(running, finished...),
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
