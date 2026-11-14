package guard

import (
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// owner is the call or unit in whose tree a process was seen.
//
//nolint:unused // first used by the watcher (unit tracking)
type owner struct {
	ID    string // a call's ID or a unit's
	Agent procKey
	Kind  kind
}

// facts is what the unit rule reads beyond the snapshot. The watcher fills it
// from the kernel; the test fills it by hand.
//
//nolint:unused // first used by the watcher (unit tracking)
type facts struct {
	self      int  // the watcher's user
	lineage   bool // platform.KnowsOriginalParent
	read      func(pid int) (platform.Lineage, bool)
	stamp     func(pid int) stamp
	args      func(pid int) []string // nil when unreadable
	known     map[procKey]kind       // agents the guard knows
	protected map[int]uint64
	versions  map[uint32]owner  // a recorded version's owner
	seenUnder map[procKey]owner // a process seen in a tracked tree
	records   *records
}

// tie is how a unit's root is tied to an agent.
//
//nolint:unused // first used by the watcher (unit tracking)
type tie struct {
	Agent  procKey
	Kind   kind
	Owner  string // the call or unit whose tree forked it, "" for a tie by stamp
	Shell  int    // the call stamp's shell, 0 without one
	Second int64  // the call stamp's second
	Codex  string // Codex's call ID, "" without one
	Mode   string // WORKBENCH_GUARD as read, "" when unread
}

//nolint:unused // first used by the watcher (unit tracking)
type verdict int

//nolint:unused // first used by the watcher (unit tracking)
const (
	notUnit verdict = iota // never a unit: do not ask again
	isUnit
	unread // no unit yet, but nothing readable was found: ask again while it lives
)

// unitRoot decides whether r, a child of the init process, is the root of a
// detached unit: work an agent's command forked and left behind. A wrong yes
// lets the guard stop a program no agent command started, so every doubt is
// a no. r is a unit only when it is the watcher's user's, not a zombie, not
// protected or under a protected process, not an agent the guard knows and
// not Workbench; when, where the kernel keeps the original parent, it was
// forked rather than started by init; and when it is tied to a known agent
// that runs and started before it, by the process that forked it or, where
// the kernel keeps the original parent, by its stamps. A stamp naming another
// agent, or WORKBENCH_GUARD=off, makes it none.
//
//nolint:unused // first used by the watcher (unit tracking)
func unitRoot(s snapshot, r proc, f facts) (tie, verdict) {
	rKey := procKey{r.PID, r.Start}
	if r.UID != f.self || r.Zombie || !childOfInit(s, r) || underProtected(s, r, f.protected) {
		return tie{}, notUnit
	}
	if _, ok := f.known[rKey]; ok {
		return tie{}, notUnit
	}
	if args := f.args(r.PID); len(args) > 0 && filepath.Base(args[0]) == "workbench" {
		return tie{}, notUnit
	}

	// Where the kernel keeps the original parent, a process init itself
	// started (an app opened with `open`) has init's own version there.
	var mine platform.Lineage
	if f.lineage {
		var parent platform.Lineage
		var ok bool
		if mine, ok = f.read(r.PID); !ok {
			return tie{}, notUnit
		}
		if parent, ok = f.read(r.PPID); !ok || mine.ParentVersion == parent.Version {
			return tie{}, notUnit
		}
	}

	stamps := stampsFor(s, r, f.stamp)
	var t tie
	if o, ok := f.seenUnder[rKey]; ok {
		t = tie{Agent: o.Agent, Kind: o.Kind, Owner: o.ID}
	} else if o, ok := f.versions[mine.ParentVersion]; f.lineage && ok {
		t = tie{Agent: o.Agent, Kind: o.Kind, Owner: o.ID}
	} else {
		// On Linux a service the init process starts can carry a command's
		// variables, and nothing there tells it from a process the command
		// forked, so a stamp ties nothing without the original parent.
		if !f.lineage {
			return tie{}, notUnit
		}
		if len(stamps) == 0 {
			return tie{}, unread
		}
		var ok bool
		if t, ok = stampTie(s, stamps, f); !ok {
			return tie{}, notUnit
		}
	}

	// A stamp read for r that names another agent makes r that agent's, to
	// be judged by that agent's stamps.
	for _, st := range stamps {
		if t.Kind == claude && st.Agent != 0 && st.Agent != t.Agent.pid {
			return tie{}, notUnit
		}
		if rec, ok := resolve(f.records, st.Codex); ok && rec.Agent != t.Agent {
			return tie{}, notUnit
		}
	}

	if _, ok := f.known[t.Agent]; !ok || !s.alive(t.Agent.pid, t.Agent.start) ||
		t.Agent.start >= r.Start {
		return tie{}, notUnit
	}
	for _, st := range stamps {
		if st.Mode != "" {
			t.Mode = st.Mode
			break
		}
	}
	if t.Mode == "off" {
		return tie{}, notUnit
	}
	return t, isUnit
}

// childOfInit reports whether r's parent is the init process: PID 1, or a
// process named as an init (initNames), such as a container's.
//
//nolint:unused // first used through unitRoot
func childOfInit(s snapshot, r proc) bool {
	if r.PPID == 1 {
		return true
	}
	parent, ok := s[r.PPID]
	return ok && initNames[parent.Name]
}

// underProtected reports whether r, or any process above it below PID 1, is
// protected, each named by PID and start time. The walk ends at a parent loop
// from a non-atomic scan.
//
//nolint:unused // first used through unitRoot
func underProtected(s snapshot, r proc, protected map[int]uint64) bool {
	seen := map[int]bool{}
	for p, ok := r, true; ok && p.PID > 1 && !seen[p.PID]; p, ok = s[p.PPID] {
		seen[p.PID] = true
		if start, found := protected[p.PID]; found && start == p.Start {
			return true
		}
	}
	return false
}

// stampsFor are the stamps read for r: its own when readable, else those of
// the first readable processes below it, reached through unreadable ones
// only. None means nothing readable was found.
//
//nolint:unused // first used through unitRoot
func stampsFor(s snapshot, r proc, read func(pid int) stamp) []stamp {
	var out []stamp
	seen := map[int]bool{}
	var walk func(p proc)
	walk = func(p proc) {
		if seen[p.PID] {
			return
		}
		seen[p.PID] = true
		if st := read(p.PID); st.Readable {
			out = append(out, st)
			return
		}
		for _, child := range s.children(p.PID) {
			walk(child)
		}
	}
	walk(r)
	return out
}

// stampTie ties stamps to an agent: to a Codex call's agent by a Codex ID
// that resolves, else to the one Claude Code agent that the call stamps
// written by their own agent name, when the guard knows it.
//
//nolint:unused // first used through unitRoot
func stampTie(s snapshot, stamps []stamp, f facts) (tie, bool) {
	for _, st := range stamps {
		if rec, ok := resolve(f.records, st.Codex); ok {
			return tie{Agent: rec.Agent, Kind: rec.Kind, Codex: st.Codex}, true
		}
	}
	var by *stamp
	for i, st := range stamps {
		if !st.call() {
			continue
		}
		if by != nil && by.Agent != st.Agent {
			return tie{}, false
		}
		if by == nil {
			by = &stamps[i]
		}
	}
	if by == nil {
		return tie{}, false
	}
	p, ok := s[by.Agent]
	if !ok {
		return tie{}, false
	}
	agent := procKey{by.Agent, p.Start}
	if f.known[agent] != claude {
		return tie{}, false
	}
	return tie{Agent: agent, Kind: claude, Shell: by.CallShell, Second: by.CallSecond}, true
}

// resolve is the record of the Codex call id, when one is kept.
//
//nolint:unused // first used through unitRoot
func resolve(recs *records, id string) (callRecord, bool) {
	if recs == nil || id == "" {
		return callRecord{}, false
	}
	return recs.byID(id)
}
