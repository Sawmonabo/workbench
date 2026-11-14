package guard

// This file tracks detached units: work an agent's command forked and left
// behind as a child of the init process (see unitRoot).

import (
	"cmp"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

const (
	// versionsKept is how long a process's version is kept after it is last
	// seen: a child the init process adopts is found within a tick or two, so
	// a minute covers a loaded machine without keeping versions without end.
	versionsKept = time.Minute
	// versionsLimit is the most versions kept, the oldest dropped first.
	versionsLimit = 1 << 16
	// lineageEvery is how often the trees' lineage is recorded: reading each
	// process's version is a call per process, too many for every 100 ms tick
	// under pressure.
	lineageEvery = time.Second
)

// unitPrefix starts a unit's ID, so it is never taken for a call's.
const unitPrefix = "detached-"

// unitID is the ID of the unit whose first root is r.
func unitID(r procRef) string { return fmt.Sprintf("%s%d-%d", unitPrefix, r.PID, r.Start) }

// rootPIDs are the PIDs whose trees make up c in s: for a unit each root
// that runs with its start time; for a noted command its root while its PID
// still names it, a zombie included, whose children still run in its tree.
func rootPIDs(s snapshot, c command) []int {
	if c.Detached == nil {
		if p, ok := s[c.Root.PID]; ok && p.Start == c.Root.Start {
			return []int{c.Root.PID}
		}
		return nil
	}
	var pids []int
	for _, r := range c.Detached.Roots {
		if s.alive(r.PID, r.Start) {
			pids = append(pids, r.PID)
		}
	}
	return pids
}

// commandTree is the tree of c.Root, or for a unit the trees of its live
// roots, each process once. A process for which skip reports true is left
// out with everything under it.
func (s snapshot) commandTree(c command, skip func(proc) bool) []proc {
	return s.commandTreeIn(s.below(), c, skip)
}

// commandTreeIn is commandTree with s's parent index already built, which a
// tick builds once for all its trees.
func (s snapshot) commandTreeIn(below parents, c command, skip func(proc) bool) []proc {
	return below.tree(s, rootPIDs(s, c), skip)
}

// live reports whether c still runs: c.Root runs with its start time, or for
// a unit any of its roots does.
func live(s snapshot, c command) bool {
	if c.Detached == nil {
		return s.alive(c.Root.PID, c.Root.Start)
	}
	return slices.ContainsFunc(c.Detached.Roots, func(r procRef) bool {
		return s.alive(r.PID, r.Start)
	})
}

// firstRoot is the first of a unit's roots that runs in s, else its first
// root, else c.Root as it is.
func firstRoot(s snapshot, c command) proc {
	if c.Detached == nil || len(c.Detached.Roots) == 0 {
		return c.Root
	}
	for _, r := range c.Detached.Roots {
		if s.alive(r.PID, r.Start) {
			return s[r.PID]
		}
	}
	r := c.Detached.Roots[0]
	return proc{Process: platform.Process{PID: r.PID, PPID: 1, Start: r.Start}}
}

// recordLineage records, at most once a second, where each process of every
// tracked command's and unit's tree was seen: seenUnder names its owner and,
// where the kernel keeps the original parent, versions names the owner of
// its version, so a child the init process adopts later is tied to the call
// or unit that forked it. Only the tree a stop would signal is recorded,
// without protected processes, so work under a nested agent is never tied to
// the outer call. A process that is gone is dropped from seenUnder; a version
// is kept versionsKept after it was last seen, at most versionsLimit of them.
func (w *watcher) recordLineage(s snapshot, below parents, protected map[int]uint64) {
	now := time.Now()
	if now.Sub(w.lineageAt) < lineageEvery {
		return
	}
	w.lineageAt = now
	for key := range w.seenUnder {
		if !s.alive(key.pid, key.start) {
			delete(w.seenUnder, key)
		}
	}
	skip := outside(protected)
	for id, c := range w.tracked {
		o := owner{ID: id, Agent: procKey{c.Note.AgentPID, c.Note.AgentStart}, Kind: c.Note.Kind}
		for _, p := range s.commandTreeIn(below, c, skip) {
			if p.Zombie {
				continue
			}
			w.seenUnder[procKey{p.PID, p.Start}] = o
			if !platform.KnowsOriginalParent {
				continue
			}
			if l, ok := platform.ProcessLineage(p.PID); ok {
				w.versions[l.Version] = o
				w.versionSeen[l.Version] = now
			}
		}
	}
	for v, at := range w.versionSeen {
		if now.Sub(at) > versionsKept {
			delete(w.versionSeen, v)
			delete(w.versions, v)
		}
	}
	if extra := len(w.versionSeen) - versionsLimit; extra > 0 {
		type seen struct {
			v  uint32
			at time.Time
		}
		all := make([]seen, 0, len(w.versionSeen))
		for v, at := range w.versionSeen {
			all = append(all, seen{v, at})
		}
		slices.SortFunc(
			all,
			func(a, b seen) int { return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.v, b.v)) },
		)
		for _, old := range all[:extra] {
			delete(w.versionSeen, old.v)
			delete(w.versions, old.v)
		}
	}
}

// stampOf is pid's stamp in s, read once per process (see readStamp) and
// kept in stamps while the process runs; one the kernel hides is read again
// next time. A stamp whose agent stamp names a
// live process that started before pid makes that process protected while it
// lives (see agents), and at once in protected when it is not nil.
func (w *watcher) stampOf(s snapshot, pid int, protected map[int]uint64) stamp {
	p, ok := s[pid]
	if !ok {
		return stamp{}
	}
	key := procKey{pid, p.Start}
	st, ok := w.stamps[key]
	if !ok {
		// Only a readable stamp is kept: a process forked but not yet exec'd
		// shows its parent's environment, which a restricted shell hides, and
		// shows its own once it execs.
		if st = readStamp(pid); st.Readable {
			w.stamps[key] = st
		}
	}
	if a, ok := s[st.Agent]; ok && st.Agent > 1 && !a.Zombie && a.Start < p.Start {
		w.stampAgents[procKey{a.PID, a.Start}] = true
		if protected != nil {
			protected[a.PID] = a.Start
		}
	}
	return st
}

// findUnits finds new units, after matching and recordLineage: each child of
// an init process that is the watcher's user's, not a zombie and not yet
// classified, by PID and start time, goes through unitRoot once, or each tick
// while its verdict is unread. A root tied by parent to a tracked unit joins
// it; any other becomes a tracked unit, logged tracking. Where the kernel
// keeps the original parent but does not answer for the init process, it
// finds none and logs that once.
func (w *watcher) findUnits(s snapshot, protected map[int]uint64) {
	for key := range w.classified {
		if !s.alive(key.pid, key.start) {
			delete(w.classified, key)
		}
	}
	for key := range w.stamps {
		if !s.alive(key.pid, key.start) {
			delete(w.stamps, key)
		}
	}
	if platform.KnowsOriginalParent {
		if _, ok := platform.ProcessLineage(1); !ok {
			if !w.lineageLost {
				w.lineageLost = true
				w.f.event(slog.LevelError, EventError,
					"cannot read where processes come from; detached work is not tracked")
			}
			return
		}
	}
	self := os.Getuid()
	f := facts{
		self:      self,
		lineage:   platform.KnowsOriginalParent,
		read:      platform.ProcessLineage,
		stamp:     func(pid int) stamp { return w.stampOf(s, pid, protected) },
		args:      platform.Args,
		known:     w.known,
		protected: protected,
		versions:  w.versions,
		seenUnder: w.seenUnder,
		records:   w.records,
	}
	var roots []proc
	for _, p := range s {
		if p.UID != self || p.Zombie || !childOfInit(s, p) {
			continue
		}
		if v, ok := w.classified[procKey{p.PID, p.Start}]; ok && v != unread {
			continue
		}
		roots = append(roots, p)
	}
	// The earlier root first, so a unit is named by its first root.
	slices.SortFunc(roots, olderFirst)
	for _, r := range roots {
		// Its stamps first, so an agent they name is protected before the rule
		// asks whether r is under a protected process.
		stampsFor(s, r, f.stamp)
		t, v := unitRoot(s, r, f)
		w.classified[procKey{r.PID, r.Start}] = v
		if v == isUnit {
			w.addRoot(s, r, t)
		}
	}
}

// addRoot tracks r, a unit's root tied by t: in the unit that forked it when
// that unit is tracked, else as a new unit, whose call it resolves once.
func (w *watcher) addRoot(s snapshot, r proc, t tie) {
	ref := procRef{PID: r.PID, Start: r.Start}
	if u, ok := w.tracked[t.Owner]; ok && u.Detached != nil {
		u.Detached.Roots = append(u.Detached.Roots, ref)
		return
	}
	call, rec := w.resolveCall(r, t)
	args := platform.Args(r.PID)
	name := r.Name
	if len(args) > 0 {
		name = filepath.Base(args[0])
	}
	cwd, _ := platform.Folder(r.PID)
	id := unitID(ref)
	n := note{
		ToolUseID:  id,
		AgentPID:   t.Agent.pid,
		AgentStart: t.Agent.start,
		Kind:       t.Kind,
		Command:    name,
		Cwd:        cwd,
		Project:    projectName("", cwd),
		NotedAt:    platform.StartTime(r.Start),
		DryRun:     t.Mode == "dry-run" || call != "" && rec.DryRun,
	}
	if call != "" {
		n.Subagent = rec.Subagent
	}
	c := command{
		Note: n, Root: s[r.PID], Shown: strings.Join(args, " "),
		Detached: &detached{Roots: []procRef{ref}, Call: call},
	}
	w.tracked[id] = c
	w.usage[id] = &usage{Since: time.Now()}
	w.f.event(slog.LevelInfo, EventTracking, "tracking", callAttrs(c)...)
}

// resolveCall is the ID and record of the call that started the unit whose
// root r is tied by t, or "" when it does not resolve: the call whose tree
// forked it, for a tie by parent; else the Codex call its stamp names; else
// the call of its agent whose shell its call stamp names (see byShell). A
// tie by parent to a unit no longer tracked resolves to nothing, as does a
// record of another agent, or of a call whose shell started after r: such a
// call did not start it.
func (w *watcher) resolveCall(r proc, t tie) (string, callRecord) {
	var rec callRecord
	var ok bool
	switch {
	case strings.HasPrefix(t.Owner, unitPrefix):
		return "", callRecord{}
	case t.Owner != "":
		// The call's tree forked r: that proves the call, with or without
		// its record, which the newest few thousand bound.
		if rec, ok = w.records.byID(t.Owner); !ok {
			return t.Owner, callRecord{}
		}
	case t.Codex != "":
		rec, ok = w.records.byID(t.Codex)
	default:
		rec, ok = w.records.byShell(t.Agent, t.Shell, t.Second)
	}
	if !ok || rec.Agent != t.Agent || rec.Shell.Start != 0 && r.Start < rec.Shell.Start {
		return "", callRecord{}
	}
	return rec.ID, rec
}

// dropAgentRoots drops from each unit the roots that are agents the guard
// knows: a session a command started and left detached is an agent, never a
// unit. A unit left with no root is forgotten with one ended record.
func (w *watcher) dropAgentRoots(s snapshot) {
	for id, c := range w.tracked {
		if c.Detached == nil {
			continue
		}
		before := len(c.Detached.Roots)
		c.Detached.Roots = slices.DeleteFunc(c.Detached.Roots, func(r procRef) bool {
			_, known := w.known[procKey{r.PID, r.Start}]
			return known
		})
		if len(c.Detached.Roots) == before {
			continue
		}
		if len(c.Detached.Roots) > 0 {
			c.Root = firstRoot(s, c)
			w.tracked[id] = c
			continue
		}
		w.f.event(slog.LevelInfo, EventEnded, "ended",
			append(w.endAttrs(c), "reason", "its root is an agent")...)
		w.untrack(id)
	}
}

// forgetUnit is forget for a unit: a root gone for two rounds (see gone)
// leaves it, and the unit ends once no root is left or its agent is gone.
// Members the init process adopts after a root exits join the unit as roots
// (see findUnits) before that root is dropped. A unit is never put in ended:
// it has no note to be matched again.
func (w *watcher) forgetUnit(s snapshot, id string, c command) {
	agentGone := w.gone(s, c.Note.AgentPID, c.Note.AgentStart)
	c.Detached.Roots = slices.DeleteFunc(c.Detached.Roots, func(r procRef) bool {
		return w.gone(s, r.PID, r.Start)
	})
	if !agentGone && len(c.Detached.Roots) > 0 {
		c.Root = firstRoot(s, c)
		w.tracked[id] = c
		return
	}
	w.f.event(slog.LevelInfo, EventEnded, "ended", w.endAttrs(c)...)
	state := stateDone
	if w.stoppedBy[id] != "" {
		state = stateStopped
	}
	w.finish(w.commandView(id, c, state))
	w.untrack(id)
}

// untrack forgets what is kept of the tracked command id.
func (w *watcher) untrack(id string) {
	delete(w.stoppedBy, id)
	delete(w.tracked, id)
	delete(w.usage, id)
	delete(w.passed, id)
	delete(w.refusals, id)
}

// killRecordFor is the call whose kill record tells c's stop, and whether
// one is written: a noted command's own call; for a unit the call that
// started it, only while that call's command runs in the foreground (its note
// is among notes and not posted) and no record for it is on disk, so a
// unit's stop never takes the place of a stop of the call itself.
func (w *watcher) killRecordFor(c command, notes []note) (string, bool) {
	if c.Detached == nil {
		return c.Note.ToolUseID, true
	}
	call := c.Detached.Call
	if call == "" || !slices.ContainsFunc(notes, func(n note) bool {
		return n.ToolUseID == call && !n.Posted
	}) {
		return "", false
	}
	if _, err := os.Lstat(w.f.record(call)); !os.IsNotExist(err) {
		return "", false
	}
	return call, true
}

// writeKill writes the kill record that tells c's stop with text, when one
// is written (see killRecordFor), and gives its path, "" when it wrote none.
func (w *watcher) writeKill(c command, notes []note, text string) string {
	call, tell := w.killRecordFor(c, notes)
	if !tell {
		return ""
	}
	record := w.f.record(call)
	if err := writeJSON(record, kill{ToolUseID: call, Reason: text, At: time.Now()}); err != nil {
		// The machine comes first: the stop goes ahead and the agent gets no reason.
		w.f.event(slog.LevelError, EventError,
			"cannot record the stop; stopping anyway, the agent will be told nothing",
			append(callAttrs(c), "error", err)...)
		return ""
	}
	return record
}
