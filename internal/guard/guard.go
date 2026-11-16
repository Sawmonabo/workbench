// Package guard stops the largest shell command an AI coding agent started
// when the machine is about to run out of memory, and tells the agent why. On
// its own it stops only a command an agent started, or work such a command
// provably forked and left behind; never an agent it knows (one whose session
// hook ran or that left a note, or a process a stamp names as its agent), an
// agent's helpers (language and MCP servers), hooks, a command it could not
// match or any other program. A person may stop a command or detached work
// from the view.
package guard

import (
	"cmp"
	"slices"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
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
// wall clock. Posted marks a background or Monitor call's note after its post
// hook, which runs while the command goes on; Ended marks any other call's note
// after its post hook, which runs once the command has returned, and the
// watcher deletes an ended note once it has kept the call's record (see
// watcher.notes). Cwd is the agent's working folder for
// the call and Folder the command's leading cd target, both real paths, so
// they compare with the folder the kernel reports for a process; either is
// empty when unknown. Subagent is the Claude Code subagent type that made the
// call, and Project labels the folder (see projectName). SessionID, NotedAt,
// Subagent and Project are only for a person reading the note.
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
	Ended      bool      `json:"ended,omitempty"`
	Cwd        string    `json:"cwd,omitempty"`
	Folder     string    `json:"folder,omitempty"`
	Subagent   string    `json:"subagent,omitempty"`
	Project    string    `json:"project,omitempty"`
}

// proc is one process in a snapshot. Args stays nil until read. CallID is
// CODEX_TOOL_CALL_ID from the environment it started with, read only for a
// Codex agent and its direct children: Codex 0.163.0 and later set it on each
// command to the hook's tool_use_id, and it survives the exec of a shell or a
// #! script. Folder is the working folder the kernel reports, a real path,
// read like Args only when matching may use it, and empty when unread or
// unreadable.
type proc struct {
	platform.Process
	Args   []string
	CallID string
	Folder string
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
	return s.below().tree(s, []int{root}, skip)
}

// parents are a snapshot's processes by parent PID, each parent's children
// oldest first: one scan of the snapshot that every tree of it can share.
type parents map[int][]proc

// below indexes s by parent.
func (s snapshot) below() parents {
	return s.belowInto(parents{})
}

// belowInto is below built in below, an index of an earlier snapshot of the
// tick's own (see watcher.tickBelow), so that its map and its slices are
// used again rather than made anew at every tick: each slice is cleared and
// emptied first, and a parent left with no child is deleted, so the index
// holds exactly what below gives for s. Nothing may keep below or one of its
// slices past the tick it was built for.
func (s snapshot) belowInto(below parents) parents {
	for ppid, children := range below {
		clear(children)
		below[ppid] = children[:0]
	}
	for _, p := range s {
		if p.PPID != p.PID {
			below[p.PPID] = append(below[p.PPID], p)
		}
	}
	for ppid, children := range below {
		if len(children) == 0 {
			delete(below, ppid)
			continue
		}
		slices.SortFunc(children, olderFirst)
	}
	return below
}

// tree is the trees of roots in s, as snapshot.tree gives one, each process
// listed once even where the trees overlap.
func (below parents) tree(s snapshot, roots []int, skip func(proc) bool) []proc {
	var out []proc
	seen := map[int]bool{}
	var walk func(p proc)
	walk = func(p proc) {
		if seen[p.PID] || skip != nil && skip(p) {
			return
		}
		seen[p.PID] = true
		out = append(out, p)
		for _, child := range below[p.PID] {
			walk(child)
		}
	}
	for _, root := range roots {
		if p, ok := s[root]; ok {
			walk(p)
		}
	}
	return out
}

// outside is the skip for a tree that may be signalled: protected processes
// (the agents the guard knows, the watcher), each named by PID and start time so
// a later process reusing a protected PID is not left out, and PID 0 and 1.
func outside(protected map[int]uint64) func(proc) bool {
	return func(p proc) bool {
		start, ok := protected[p.PID]
		return ok && start == p.Start || p.PID <= 1
	}
}

// command is an agent command the watcher tracks: the agent's direct child
// whose tree holds the shell running one note's text. Or, with Detached, a
// detached unit: work an agent's command forked that the init process
// adopted (see unitRootWhy). A unit's Note is made up by the watcher and never
// written to disk: its ToolUseID is the unit's ID and its Command the root
// program's name only, because a running program's arguments may hold a
// token the shell expanded and the note's Command reaches the log and the
// reason. Root is then its first root that runs, and Shown its command line,
// which only the view shows.
type command struct {
	Note     note
	Root     proc
	Detached *detached
	Shown    string
}

// detached is what makes a command a unit: its roots, each a child of the
// init process, and Call, the ID of the call that started it, "" when it did
// not resolve (see watcher.resolveCall), in which case the guard never
// chooses it. ByStamp marks a unit whose first root was tied by its stamps
// alone, whose call it loses once that call is found to have started an
// agent session (see records.stampProves).
type detached struct {
	Roots   []detachedRoot `json:"roots"`
	Call    string         `json:"call"`
	ByStamp bool           `json:"by_stamp,omitempty"`
}

// detachedRoot is one root of a unit: the process, when the guard first
// tracked it or when it last started a new program (Since, which does not
// cross a handover: the new watcher starts it again), the program it was
// judged in (Version where the kernel keeps versions, else its Name and
// Args, see program), so that the guard judges it again once it runs another
// (see watcher.rejudgeRoots), and its process name and command line as they
// are now. Pending marks a root that is not settled, which the guard's own
// stop never takes (see rootHeld): seen in another program and not yet
// judged in it, or with nothing readable for its setting. Args is its
// command line as the kernel gives it, kept only where it decides (where the
// kernel keeps no versions, see program) and never shown: a program that
// rewrote its title can make it end in environment strings, so where the
// version decides it is not read at all, and nothing unfiltered is kept or
// carried at a handover. Shown is what the view shows of it (see
// shownCommand); neither is logged.
type detachedRoot struct {
	procRef
	Since   time.Time `json:"-"`
	Version uint32    `json:"version,omitempty"`
	Name    string    `json:"name"`
	Args    string    `json:"args,omitempty"`
	Shown   string    `json:"shown,omitempty"`
	Pending bool      `json:"pending,omitempty"`
}

// target is one signal: to the process PID, or with Group to the process
// group it leads. Start is the process's start time in the snapshot the
// target came from.
type target struct {
	PID   int
	Start uint64
	Group bool
}
