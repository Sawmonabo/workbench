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
	"strings"
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
// over its tree without protected processes, as a command's is. A row keeps
// its made-up note and command line from the tick it was first seen, and its
// usage by row ID; the usage and stoppedBy of a row that is gone are dropped.
func (w *watcher) buildRows(
	s snapshot,
	notes []note,
	taken map[int]bool,
	protected map[int]uint64,
) {
	before := w.rows
	w.rows, w.rowTrees = map[string]command{}, map[string][]ProcessView{}
	below, skip, now := s.below(), outside(protected), time.Now()
	waiting := w.untracked(notes)
	for agent, k := range w.known {
		if !s.alive(agent.pid, agent.start) {
			continue
		}
		a := s[agent.pid]
		w.addRow(before, rowAgent, a, agent, k, []proc{a}, false)
		dry := dryMode(w.stampOf(s, a.PID, protected))
		for _, child := range below[a.PID] {
			first, seen := w.agentChildren[procKey{child.PID, child.Start}]
			if !seen || now.Sub(first) < helperAfter || taken[child.PID] || child.Zombie ||
				skip(child) {
				continue
			}
			if child.Args == nil {
				child.Args = platform.Args(child.PID)
			}
			if slices.ContainsFunc(waiting, func(n note) bool {
				return n.AgentPID == a.PID && n.AgentStart == a.Start && claims(s, n, a, child,
					n.NotedStart-min(n.NotedStart, platform.StartSecond),
					w.setsCallIDs[agent])
			}) {
				continue
			}
			tree := below.tree(s, []int{child.PID}, skip)
			var stamps []stamp
			for _, p := range tree {
				if st := w.stampOf(s, p.PID, protected); st.Readable && !p.Zombie {
					stamps = append(stamps, st)
				}
			}
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

// rowKind is the row a direct child of an agent of kind k, whose PID is
// agentPID, gets from its command line and the readable stamps of its tree,
// or "" for none. A helper is not a shell (see shells) and, under
// Claude Code, carries no CLAUDE_PID naming its agent in its tree (a hook and
// a status line command do), or, under Codex, no Codex call ID. An unmatched
// command, under Claude Code only, has in its tree a call stamp its own agent
// wrote that names the child as its shell, with a second that fits the
// child's start as records.byShell judges it: a command whose shape no note
// matched, or one a person typed with !. A child whose command line cannot be
// read is no helper.
func rowKind(k kind, agentPID int, child proc, stamps []stamp) string {
	// Any shell, in a shape shellScript knows or not: a shell whose whole tree
	// hides its environment would otherwise pass for a helper.
	helper := len(child.Args) > 0 && !shells[filepath.Base(child.Args[0])]
	switch k {
	case claude:
		if helper &&
			!slices.ContainsFunc(stamps, func(st stamp) bool { return st.Agent == agentPID }) {
			return rowHelper
		}
		started := platform.StartTime(child.Start)
		if !started.IsZero() && slices.ContainsFunc(stamps, func(st stamp) bool {
			late := st.CallSecond - started.Unix()
			return st.call() && st.Agent == agentPID && st.CallShell == child.PID &&
				late >= 0 && late <= int64(stampSlack/time.Second)
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
// subagent, and its command line in Shown, which only the view shows. dry
// marks its stops as log only; once set it stays.
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
	if !ok {
		args := p.Args
		if args == nil {
			args = platform.Args(p.PID)
		}
		name := p.Name
		if len(args) > 0 {
			name = filepath.Base(args[0])
		}
		cwd, _ := platform.Folder(p.PID)
		c = command{
			Row: row, Shown: strings.Join(args, " "),
			Note: note{
				ToolUseID: id, AgentPID: agent.pid, AgentStart: agent.start, Kind: k,
				Command: name, Cwd: cwd, Project: projectName("", cwd),
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
