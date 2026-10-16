// Package guard stops the largest shell command an AI coding agent started
// when the machine is about to run out of memory, and tells the agent why. It
// never stops the agent, its harness, MCP servers, hooks or any other
// program. See docs/superpowers/specs/2026-10-14-agent-command-guard-design.md.
package guard

import (
	"cmp"
	"slices"
	"time"
)

// kind is the agent a note came from; it decides the command shape matched.
type kind string

const (
	claude kind = "claude"
	codex  kind = "codex"
)

// note is what the pre hook leaves for one shell call. NotedStart is the pre
// hook process's own kernel start time, in the units of proc.Start, so the
// matcher compares kernel start times with each other and never with the
// wall clock. Posted marks a background call's note after its post hook,
// which runs while the command goes on. SessionID and NotedAt are only for a
// person reading the note.
type note struct {
	AgentPID   int       `json:"agent_pid"`
	AgentStart uint64    `json:"agent_start"`
	Kind       kind      `json:"kind"`
	SessionID  string    `json:"session_id"`
	ToolUseID  string    `json:"tool_use_id"`
	Command    string    `json:"command"`
	NotedStart uint64    `json:"noted_start"`
	NotedAt    time.Time `json:"noted_at"`
	DryRun     bool      `json:"dry_run,omitempty"`
	Posted     bool      `json:"posted,omitempty"`
}

// proc is one process in a snapshot. Start is the kernel's start time in the
// platform's own units (startSlack is one second of them) and, with the PID,
// names the process: a later process reusing the PID has another Start. Args
// stays nil until read.
type proc struct {
	PID, PPID, PGID int
	Start           uint64
	Name            string
	Args            []string
	Zombie          bool
}

// snapshot is every process at one moment, by PID.
type snapshot map[int]proc

// children are pid's direct children, oldest first.
func (s snapshot) children(pid int) []proc {
	var out []proc
	for _, p := range s {
		if p.PPID == pid && p.PID != pid {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, olderFirst)
	return out
}

func olderFirst(a, b proc) int {
	return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(a.PID, b.PID))
}

// tree is root and its descendants, each parent before its children. A
// process for which skip reports true is left out with everything under it.
// Each process is listed once, so a parent loop from a non-atomic scan ends
// the walk. One pass indexes the snapshot by parent, so a large tree costs
// one scan of the machine's processes, not one per member.
func (s snapshot) tree(root int, skip func(proc) bool) []proc {
	below := map[int][]proc{}
	for _, p := range s {
		if p.PPID != p.PID {
			below[p.PPID] = append(below[p.PPID], p)
		}
	}
	var out []proc
	seen := map[int]bool{}
	var walk func(p proc)
	walk = func(p proc) {
		if seen[p.PID] || skip != nil && skip(p) {
			return
		}
		seen[p.PID] = true
		out = append(out, p)
		children := below[p.PID]
		slices.SortFunc(children, olderFirst)
		for _, child := range children {
			walk(child)
		}
	}
	if p, ok := s[root]; ok {
		walk(p)
	}
	return out
}

// outside is the skip for a tree that may be signalled: protected processes
// (agents that left a note, the watcher) and PID 0 and 1.
func outside(protected map[int]bool) func(proc) bool {
	return func(p proc) bool { return protected[p.PID] || p.PID <= 1 }
}

// command is an agent command the watcher tracks: the agent's direct child
// whose tree holds the shell running one note's text.
type command struct {
	Note note
	Root proc
}

// target is one signal: to the process PID, or with Group to the process
// group it leads. Start is the process's start time in the snapshot the
// target came from.
type target struct {
	PID   int
	Start uint64
	Group bool
}
