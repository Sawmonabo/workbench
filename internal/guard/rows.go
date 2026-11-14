package guard

// This file builds the rows the view shows beside the tracked commands: each
// agent the guard knows, each long-lived helper an agent started itself (a
// language or MCP server) and each running command no note matched. Rows are
// display only: never tracked, never carried at a handover, never chosen by
// the guard, and never logged while they run. A person may stop a helper or
// an unmatched command; a stop of an agent row is refused.

import (
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// helperAfter is how long a direct child of an agent must have been seen
// alive before it is a row: a hook or a command's shell lives a moment, and
// a forked child shows its parent's environment until it execs.
const helperAfter = 5 * time.Second

// The kinds of row, each its rows' ID prefix.
const (
	rowAgent     = "agent"
	rowHelper    = "helper"
	rowUnmatched = "unmatched"
)

// rowID is the ID of the row of kind row whose root is p.
func rowID(row string, p proc) string { return fmt.Sprintf("%s-%d-%d", row, p.PID, p.Start) }

// buildRows sets w.rows for this tick, after markChildSeen: a row for each
// known agent that runs, sized by the agent process alone, and, for each of
// its direct children seen alive for helperAfter that is not in taken, not
// protected and claimed by no waiting note, the row rowKind gives it, sized
// over its tree without protected processes, as a command's is. below is the
// tick's parent index of s. A row keeps its made-up note and command line
// from the tick it was first seen while its process name stays the same, and
// its usage by row ID; the usage and stoppedBy of a row that is gone are
// dropped. A child kept as a row the tick before, with the same PID, start
// time and process name, is not read again for its command line.
func (w *watcher) buildRows(
	s snapshot,
	below parents,
	notes []note,
	taken map[int]bool,
	protected map[int]uint64,
) {
	before := w.rows
	w.rows, w.rowTrees = map[string]command{}, map[string][]ProcessView{}
	kept := map[procKey]proc{}
	for _, c := range before {
		kept[procKey{c.Root.PID, c.Root.Start}] = c.Root
	}
	skip, now := outside(protected), time.Now()
	waiting := w.untracked(notes)
	for agent, k := range w.known {
		if !s.alive(agent.pid, agent.start) {
			continue
		}
		a := s[agent.pid]
		w.addRow(before, rowAgent, a, agent, k, []proc{a}, false)
		dry := dryMode(w.stampOf(s, a.PID, protected))
		for _, child := range below[a.PID] {
			key := procKey{child.PID, child.Start}
			first, seen := w.agentChildren[key]
			if !seen || now.Sub(first) < helperAfter || taken[child.PID] || child.Zombie ||
				skip(child) {
				continue
			}
			child.Args = argsOf(child, kept[key])
			if slices.ContainsFunc(waiting, func(n note) bool {
				return n.AgentPID == a.PID && n.AgentStart == a.Start && claims(s, n, a, child,
					n.NotedStart-min(n.NotedStart, platform.StartSecond),
					w.setsCallIDs[agent])
			}) {
				continue
			}
			tree, stamps := w.stampedTree(s, below, child.PID, protected)
			row := rowKind(k, a.PID, child, stamps)
			if row == "" {
				continue
			}
			w.addRow(
				before,
				row,
				child,
				agent,
				k,
				tree,
				dry || slices.ContainsFunc(stamps, dryMode),
			)
		}
	}
	for id := range before {
		if _, ok := w.rows[id]; !ok {
			delete(w.usage, id)
			delete(w.stoppedBy, id)
		}
	}
}

// argsOf is child's command line: as already read this tick, else as read
// for last, the same process kept as a row the tick before, while its
// process name stays the same, else read now.
func argsOf(child, last proc) []string {
	switch {
	case child.Args != nil:
		return child.Args
	case last.PID == child.PID && last.Start == child.Start && last.Name == child.Name &&
		last.Args != nil:
		return last.Args
	}
	return platform.Args(child.PID)
}

// stampedTree is root's tree without protected processes and the readable
// stamps of its processes, zombies left out. The stamps are read first: one
// may name a process of the tree as its agent, which stampOf then protects,
// so the tree is walked again after, without it, and a row's size never
// counts it beside its own agent's row.
func (w *watcher) stampedTree(
	s snapshot,
	below parents,
	root int,
	protected map[int]uint64,
) ([]proc, []stamp) {
	stamped := map[int]stamp{}
	for _, p := range below.tree(s, []int{root}, outside(protected)) {
		if !p.Zombie {
			stamped[p.PID] = w.stampOf(s, p.PID, protected)
		}
	}
	tree := below.tree(s, []int{root}, outside(protected))
	var stamps []stamp
	for _, p := range tree {
		if st, ok := stamped[p.PID]; ok && st.Readable {
			stamps = append(stamps, st)
		}
	}
	return tree, stamps
}

// rowKind is the row a direct child of an agent of kind k, whose PID is
// agentPID, gets from its command line and the readable stamps of its tree,
// or "" for none. A helper is not a shell (see shells) or a sandbox launcher
// (see launchers), which runs a command, and, under Claude Code, carries no
// CLAUDE_PID naming its agent in its tree (a hook and a status line command
// do), or, under Codex, no Codex call ID. An unmatched command, under Claude
// Code only, has in its tree a call stamp its own agent wrote that names the
// child as its shell, with a second that fits the child's start as
// records.byShell judges it (see fitsShell): a command whose shape no note
// matched, or one a person typed with !. A child whose command line cannot be
// read is no helper.
func rowKind(k kind, agentPID int, child proc, stamps []stamp) string {
	// Any shell, in a shape shellScript knows or not: a shell whose whole tree
	// hides its environment would otherwise pass for a helper. A launcher
	// runs a command too, which under a Codex that sets no call IDs carries
	// nothing that tells it from a helper. Both are judged by the program
	// the command line names and by the kernel's name.
	helper := len(child.Args) > 0
	if helper {
		for _, name := range []string{filepath.Base(child.Args[0]), child.Name} {
			helper = helper && !shells[name] && !launchers[name]
		}
	}
	switch k {
	case claude:
		if helper &&
			!slices.ContainsFunc(stamps, func(st stamp) bool { return st.Agent == agentPID }) {
			return rowHelper
		}
		if slices.ContainsFunc(stamps, func(st stamp) bool {
			return st.call() && st.Agent == agentPID && st.CallShell == child.PID &&
				fitsShell(child.Start, st.CallSecond)
		}) {
			return rowUnmatched
		}
	case codex:
		if helper && !slices.ContainsFunc(stamps, func(st stamp) bool { return st.Codex != "" }) {
			return rowHelper
		}
	}
	return ""
}

// dryMode reports whether a stamp's WORKBENCH_GUARD keeps a person's stop of
// a row to the log: dry-run, as for a call's note, or off.
func dryMode(st stamp) bool { return st.Mode == "dry-run" || st.Mode == "off" }

// addRow keeps the row of kind row rooted at p, of the agent of kind k, in
// w.rows, and adds its size over tree to its usage. A new row's note is made
// up as a unit's is (see addRoot): its ID the row's, its command the
// program's name only, its folder and project the process's own, no
// subagent, and its command line in Shown, which only the view shows; it is
// made again once the process name changes, since the process then runs
// another program. dry marks its stops as log only; once set it stays.
func (w *watcher) addRow(
	before map[string]command,
	row string,
	p proc,
	agent procKey,
	k kind,
	tree []proc,
	dry bool,
) {
	id := rowID(row, p)
	c, ok := before[id]
	if !ok || c.Root.Name != p.Name {
		cwd, _ := platform.Folder(p.PID)
		// The kernel's name only reaches the log; the command line, which
		// may hold a token, only the view, in the form that never holds an
		// environment string (see programName, shownCommand). p.Args, read
		// for matching, is never shown.
		c = command{
			Row: row, Shown: shownCommand(p.PID, p.Name),
			Note: note{
				ToolUseID: id, AgentPID: agent.pid, AgentStart: agent.start, Kind: k,
				Command: programName(p), Cwd: cwd, Project: projectName("", cwd),
				NotedAt: platform.StartTime(p.Start),
			},
		}
	}
	c.Root = p
	c.Note.DryRun = c.Note.DryRun || dry
	w.rows[id] = c
	u := w.usage[id]
	if u == nil {
		u = &usage{Since: time.Now()}
		w.usage[id] = u
	}
	sizes := map[int]uint64{}
	u.add(w.size(tree, residentInto(sizes)))
	w.rowTrees[id] = treeView(tree, sizes)
}
