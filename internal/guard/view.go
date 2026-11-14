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
// shownCommand), which the view alone shows. A row shows its root and
// command line in that form too, and its kind unless it is an unmatched
// command, which shows as a command does.
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
	if c.Row != "" {
		v.PID = c.Root.PID
		if c.Row != rowUnmatched {
			v.Kind = c.Row
		}
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
// their trees as measure read them, a unit that runs with why the guard
// itself will not choose it now (see held), then the rows of this tick (see
// buildRows), an unmatched command's with the state unmatched, then the
// commands that finished within finishedShown, whose older ones it forgets,
// and the memory in use split by the sizes the running rows show.
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
	newestFirst := func(a, b CommandView) int {
		return cmp.Or(b.Started.Compare(a.Started), cmp.Compare(a.ID, b.ID))
	}
	slices.SortFunc(running, newestFirst)
	var rows []CommandView
	for id, c := range w.rows {
		state := stateRunning
		switch {
		case w.manual[id] != nil:
			state = stateStopping
		case c.Row == rowUnmatched:
			state = stateUnmatched
		}
		v := w.commandView(id, c, state)
		v.Tree = w.rowTrees[id]
		rows = append(rows, v)
	}
	slices.SortFunc(rows, newestFirst)
	finished := slices.Clone(w.finished)
	slices.SortFunc(finished, func(a, b CommandView) int { return b.Ended.Compare(a.Ended) })
	view := Snapshot{
		At: now, Memory: memoryView(m), LargestPeak: w.largestPeak,
		Commands: slices.Concat(running, rows, finished),
	}
	view.Sums = memorySums(view.Memory.Used, slices.Concat(running, rows))
	return view
}

// memorySums splits used, the memory in use, by the memory the views show,
// each by its kind; a command's ended this tick shows none. Other is what is
// left, never below zero: resident sizes count shared pages in each process.
func memorySums(used uint64, views []CommandView) MemorySums {
	var sums MemorySums
	for _, v := range views {
		switch v.Kind {
		case "":
			sums.Commands += v.Memory
		case "detached":
			sums.Detached += v.Memory
		case rowAgent:
			sums.Agents += v.Memory
		case rowHelper:
			sums.Helpers += v.Memory
		}
	}
	if parts := sums.Commands + sums.Detached + sums.Agents + sums.Helpers; parts < used {
		sums.Other = used - parts
	}
	return sums
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
