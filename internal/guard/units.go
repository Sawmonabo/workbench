package guard

import (
	"cmp"
	"path/filepath"
	"slices"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// owner is the call or unit in whose tree a process was seen.
type owner struct {
	ID    string  `json:"id"` // a call's ID or a unit's
	Agent procKey `json:"agent"`
	Kind  kind    `json:"kind"`
}

// facts is what the unit rule reads beyond the snapshot. The watcher fills it
// from the kernel; the test fills it by hand.
type facts struct {
	self      int  // the watcher's user
	lineage   bool // platform.KnowsOriginalParent
	read      func(pid int) (platform.Lineage, bool)
	stamp     func(pid int) stamp
	args      func(pid int) []string // nil when unreadable
	known     map[procKey]kind       // agents the guard knows
	protected map[int]uint64
	origins   *ancestry // where the processes of tracked trees were seen
	records   *records
	below     parents // the snapshot's parent index
}

// tie is how a unit's root is tied to an agent.
type tie struct {
	Agent   procKey
	Kind    kind
	Owner   string  // the call or unit whose tree forked it, "" for a tie by stamp
	Parent  procKey // the recorded process that forked it, for a tie by its version
	Version uint32  // the root's own version as read, 0 where none is kept
	Shell   int     // the call stamp's shell, 0 without one
	Second  int64   // the call stamp's second
	Codex   string  // Codex's call ID, "" without one
	Mode    string  // WORKBENCH_GUARD as read, "" when unread
	// Hidden marks a tie by parent for which nothing readable was found: the
	// root's environment is hidden, and so is that of every process below it
	// reached through hidden ones, so its setting is not known.
	Hidden bool
}

type verdict int

const (
	// notUnit is no unit: asked again only when its reason is not final (see
	// finalWhy) and the process runs another program.
	notUnit verdict = iota
	isUnit
	unread // no unit yet, but nothing readable was found: ask again while it lives
)

// The reasons unitRootWhy gives for a root that is no unit, as an ended
// record of a unit whose last root is dropped names them. finalWhy says which
// are final.
const (
	whyOtherUser     = "its root is another user's"
	whyStartedByInit = "its root was started by the init process"
	whyNotDetached   = "its root is no longer a child of the init process"
	whyProtected     = "its root is protected or under a protected process"
	whyAgent         = "its root is an agent"
	whyWorkbench     = "its root is Workbench"
	whyNoLineage     = "where its root comes from cannot be read"
	whyUntied        = "its root is tied to no agent's command"
	whyOtherAgent    = "its root belongs to another agent"
	whyTwoAgents     = "its root's stamps name two agents"
	whyAgentUnknown  = "its root's agent is not one the guard knows that started before it"
	whyOff           = "its root turned the guard off"
	whyTiedDropped   = "what started its root is not this call's work"
)

// finalWhy reports whether a "no unit" verdict for why is final for the life
// of the process (its PID and start time): it rests on evidence that the
// process is not this agent's command's work, which no later program it runs
// can take back. A verdict that rests only on the absence of evidence may be
// judged again once the process runs another program. Every caller that
// keeps a verdict asks this, so the first judgment and a later one agree.
func finalWhy(why string) bool {
	switch why {
	case whyOtherUser, // a process's owner never changes
		whyStartedByInit, // the kernel's record of who forked it never changes
		whyProtected,     // an agent, the watcher, or work under them
		whyAgent,         // an agent the guard knows is never a unit
		whyWorkbench,     // Workbench's own process, whatever it runs next
		whyOtherAgent,    // its stamps name another agent: that agent's work
		whyTwoAgents,     // its stamps say two agents' commands started it
		whyTiedDropped,   // what tied it was found not to be this call's work
		// The process said so itself. A program it starts may drop the
		// variable (the caller of execve chooses the environment), and it
		// stays out all the same: once a process opted out, it is out for
		// its life, whatever it runs later.
		whyOff:
		return true
	case whyNotDetached, // not (yet) a child of the init process, or a zombie
		whyNoLineage,    // a read that failed, not an answer
		whyUntied,       // nothing ties it to an agent's command yet
		whyAgentUnknown: // the agent it is tied to is not known yet, or not alive
		return false
	}
	return false // unread ("") and anything else: nothing readable was found
}

// unitRootWhy decides whether r, a child of the init process, is the root of
// a detached unit: work an agent's command forked and left behind, and, when
// it is none, why. A wrong yes lets the guard stop a program no agent command
// started, so every doubt is a no. r is a unit only when it is the watcher's
// user's, not a zombie, not protected or under a protected process, not an
// agent the guard knows and not Workbench; when, where the kernel keeps the
// original parent, it was forked rather than started by init; and when it is
// tied to a known agent that runs and started before it, by the process that
// forked it or, where the kernel keeps the original parent, by its stamps. A
// stamp naming another agent, or WORKBENCH_GUARD=off, makes it none. No
// environment is read for a root these rules reject before its stamps.
func unitRootWhy(s snapshot, r proc, f facts) (tie, verdict, string) {
	rKey := procKey{r.PID, r.Start}
	switch {
	case r.UID != f.self:
		return tie{}, notUnit, whyOtherUser
	case r.Zombie || !childOfInit(s, r, f.protected):
		return tie{}, notUnit, whyNotDetached
	case underProtected(s, r, f.protected):
		return tie{}, notUnit, whyProtected
	}
	if _, ok := f.known[rKey]; ok {
		return tie{}, notUnit, whyAgent
	}
	if args := f.args(r.PID); len(args) > 0 && filepath.Base(args[0]) == "workbench" {
		return tie{}, notUnit, whyWorkbench
	}

	// Where the kernel keeps the original parent, a process init itself
	// started (an app opened with `open`) has init's own version there.
	var mine platform.Lineage
	if f.lineage {
		var parent platform.Lineage
		var ok bool
		if mine, ok = f.read(r.PID); !ok {
			return tie{}, notUnit, whyNoLineage
		}
		if parent, ok = f.read(r.PPID); !ok {
			return tie{}, notUnit, whyNoLineage
		}
		if mine.ParentVersion == parent.Version {
			return tie{}, notUnit, whyStartedByInit
		}
	}

	stamps := stampsFor(f.below, r, f.stamp)
	var t tie
	if o, ok := f.origins.ownerOf(rKey); ok {
		t = tie{Agent: o.Agent, Kind: o.Kind, Owner: o.ID, Hidden: len(stamps) == 0}
	} else if forker, o, ok := f.origins.forker(mine.ParentVersion); f.lineage && ok {
		t = tie{Agent: o.Agent, Kind: o.Kind, Owner: o.ID, Parent: forker, Hidden: len(stamps) == 0}
	} else {
		// On Linux a service the init process starts can carry a command's
		// variables, and nothing there tells it from a process the command
		// forked, so a stamp ties nothing without the original parent.
		if !f.lineage {
			return tie{}, notUnit, whyUntied
		}
		if len(stamps) == 0 {
			return tie{}, unread, ""
		}
		var why string
		if t, why = stampTie(s, stamps, f); why != "" {
			return tie{}, notUnit, why
		}
	}

	// A stamp read for r that names another agent makes r that agent's, to
	// be judged by that agent's stamps.
	for _, st := range stamps {
		if t.Kind == claude && st.Agent != 0 && st.Agent != t.Agent.pid {
			return tie{}, notUnit, whyOtherAgent
		}
		if rec, ok := resolve(f.records, st.Codex); ok && rec.Agent != t.Agent {
			return tie{}, notUnit, whyOtherAgent
		}
	}

	if _, ok := f.known[t.Agent]; !ok || !s.alive(t.Agent.pid, t.Agent.start) ||
		t.Agent.start >= r.Start {
		return tie{}, notUnit, whyAgentUnknown
	}
	for _, st := range stamps {
		if st.Mode != "" {
			t.Mode = st.Mode
			break
		}
	}
	if t.Mode == "off" {
		return tie{}, notUnit, whyOff
	}
	t.Version = mine.Version
	return t, isUnit, ""
}

// childOfInit reports whether r's parent is the init process: PID 1, or a
// process named as an init (initNames), such as a container's. A PID 1 that
// is protected is an agent run as a container's init, whose children are its
// own commands' shells, not work left behind, so it is no init.
func childOfInit(s snapshot, r proc, protected map[int]uint64) bool {
	if r.PPID == 1 {
		first, ok := s[1]
		start, isProtected := protected[1]
		return !ok || !isProtected || start != first.Start
	}
	parent, ok := s[r.PPID]
	return ok && initNames[parent.Name]
}

// underProtected reports whether r, or any process above it below PID 1, is
// protected, each named by PID and start time. The walk ends at a parent loop
// from a non-atomic scan.
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
// the first readable processes below it in below, the parent index, reached
// through unreadable ones only. None means nothing readable was found.
func stampsFor(below parents, r proc, read func(pid int) stamp) []stamp {
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
		for _, child := range below[p.PID] {
			walk(child)
		}
	}
	walk(r)
	return out
}

// stampTie ties stamps to an agent: to a Codex call's agent by a Codex ID
// that resolves to a Codex call, else to the one Claude Code agent that the
// call stamps written by their own agent name, when the guard knows it, and
// gives why not otherwise: call stamps written by two agents are evidence
// that the root is not one agent command's work (whyTwoAgents), anything
// else is the absence of a tie (whyUntied). A Claude Code call's ID put in
// CODEX_TOOL_CALL_ID ties nothing, so it cannot stand in for the agreeing
// call stamp.
func stampTie(s snapshot, stamps []stamp, f facts) (tie, string) {
	for _, st := range stamps {
		if rec, ok := resolve(f.records, st.Codex); ok && rec.Kind == codex {
			return tie{Agent: rec.Agent, Kind: codex, Codex: st.Codex}, ""
		}
	}
	var by *stamp
	for i, st := range stamps {
		if !st.call() {
			continue
		}
		if by != nil && by.Agent != st.Agent {
			return tie{}, whyTwoAgents
		}
		if by == nil {
			by = &stamps[i]
		}
	}
	if by == nil {
		return tie{}, whyUntied
	}
	p, ok := s[by.Agent]
	if !ok {
		return tie{}, whyUntied
	}
	agent := procKey{by.Agent, p.Start}
	if f.known[agent] != claude {
		return tie{}, whyUntied
	}
	return tie{Agent: agent, Kind: claude, Shell: by.CallShell, Second: by.CallSecond}, ""
}

// resolve is the record of the Codex call id, when one is kept.
func resolve(recs *records, id string) (callRecord, bool) {
	if recs == nil || id == "" {
		return callRecord{}, false
	}
	return recs.byID(id)
}

// unitGrace is how long a unit is tracked before the guard may choose it: a
// session a command started and left detached needs that long to reach its
// SessionStart hook and be known as an agent, which is never stopped.
const unitGrace = 10 * time.Second

// candidate is a tracked command as the guard's own choice sees it: its size
// now and how long it has been tracked.
type candidate struct {
	c       command
	size    uint64
	tracked time.Duration
}

// mayChoose reports whether the guard may choose the candidate on its own: a
// noted command always, a unit only once a call is proved to have started it
// and it has been tracked for unitGrace. A stamp that resolves to no call
// does not prove an agent's command forked the process (a command a person
// typed with ! carries the same stamp), so such a unit is left to a person.
func (cand candidate) mayChoose() bool {
	return cand.c.Detached == nil || cand.c.Detached.Call != "" && cand.tracked >= unitGrace
}

// ownStop is c as the guard's own stop may take it at now, and how long the
// youngest root it keeps has been tracked: a noted command whole (0); a unit
// with only its settled roots (see rootHeld), since a session a unit's
// process starts and leaves detached joins that unit and needs unitGrace to
// be known as an agent, and a root not settled may be one that opted out. A
// unit with no such root keeps none and gives 0, which choose never takes. A
// person's stop takes every live root instead.
func ownStop(c command, now time.Time) (command, time.Duration) {
	if c.Detached == nil {
		return c, 0
	}
	own := *c.Detached
	own.Roots = nil
	var youngest time.Duration
	for _, r := range c.Detached.Roots {
		if rootHeld(r, now) != "" {
			continue
		}
		if age := now.Sub(r.Since); own.Roots == nil || age < youngest {
			youngest = age
		}
		own.Roots = append(own.Roots, r)
	}
	c.Detached = &own
	if len(own.Roots) > 0 && !slices.ContainsFunc(own.Roots, func(r detachedRoot) bool {
		return r.PID == c.Root.PID && r.Start == c.Root.Start
	}) {
		// The reason, the log and the kill record name the program of a root
		// this stop signals, not the one it left out.
		first := own.Roots[0]
		c.Root = proc{Process: platform.Process{PID: first.PID, PPID: 1, Start: first.Start}}
		c.Note.Command, c.Shown = cmp.Or(first.Name, c.Note.Command), first.Args
	}
	return c, youngest
}

// Why the guard itself will not choose a unit now (see held), as a snapshot
// shows it: no call is proved to have started it, no root has been tracked
// unitGrace, or no root tracked that long is settled. The last two are also
// why its own stop leaves one root out (see rootHeld).
const (
	heldNoCall = "no call"
	heldYoung  = "young"
	heldUnread = "unread"
)

// held is why the guard itself will not choose the unit d at now, "" when it
// may: heldNoCall without a proved call, else heldYoung when no root that
// runs (runs reports it) has been tracked unitGrace, else heldUnread when
// none of those is settled. It is the view's word for what mayChoose and
// ownStop decide, by the same rootHeld.
func held(d *detached, now time.Time, runs func(detachedRoot) bool) string {
	if d.Call == "" {
		return heldNoCall
	}
	why := heldYoung
	for _, r := range d.Roots {
		if !runs(r) {
			continue
		}
		switch rootHeld(r, now) {
		case "":
			return ""
		case heldUnread:
			why = heldUnread
		}
	}
	return why
}

// rootHeld is why the guard's own stop leaves the root r out at now, "" when
// it may take it: heldYoung while it has been tracked less than unitGrace
// since it was found or started its present program, else heldUnread while
// it is Pending (its setting cannot be read, or it is being judged again).
// It is the one predicate that decides this.
func rootHeld(r detachedRoot, now time.Time) string {
	switch {
	case now.Sub(r.Since) < unitGrace:
		return heldYoung
	case r.Pending:
		return heldUnread
	}
	return ""
}

// choose is the guard's choice of what to stop: the largest candidate it may
// stop, the earlier root first among equals, and the sum of those it may.
func choose(cands []candidate) (chosen candidate, total uint64, ok bool) {
	for _, cand := range cands {
		if !cand.mayChoose() {
			continue
		}
		total += cand.size
		if !ok || cand.size > chosen.size ||
			cand.size == chosen.size && cand.c.Root.Start < chosen.c.Root.Start {
			chosen, ok = cand, true
		}
	}
	return chosen, total, ok
}
