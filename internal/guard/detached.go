package guard

// This file tracks detached units: work an agent's command forked and left
// behind as a child of the init process (see unitRootWhy).

import (
	"cmp"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
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
	return slices.ContainsFunc(c.Detached.Roots, func(r detachedRoot) bool {
		return s.alive(r.PID, r.Start)
	})
}

// programName is a program's name as the log, the reason and a kill record
// may name it: the kernel's process name, never anything from its arguments,
// which its launcher sets (exec -a) or the program itself rewrites, and which
// may hold users, hosts, paths or a token.
func programName(p proc) string { return p.Name }

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

// recordLineage records, at every tick, each process of every tracked
// command's and unit's tree in the ancestry store, with the version the
// kernel keeps for it, so a child the init process adopts later is tied to
// the call or unit that forked it. Only the tree a stop would signal is
// recorded: protected processes and what is under them are left out, and
// zombies are skipped. A noted command's shell is recorded without a parent,
// so no chain leads from a call into its agent, and a unit's root keeps the
// parent it was given when it was tied (see addRoot). Then every recorded
// process that is protected now (a known agent, the watcher, a process a
// stamp names as its agent) is dropped with what was recorded below it (see
// dropAndLeave), so what a nested session left before it was known is no
// longer the outer call's.
func (w *watcher) recordLineage(s snapshot, below parents, protected map[int]uint64) {
	now := time.Now()
	w.ancestry.prune(func(k procKey) bool { return s.alive(k.pid, k.start) }, now)
	skip := outside(protected)
	for id, c := range w.tracked {
		o := owner{ID: id, Agent: procKey{c.Note.AgentPID, c.Note.AgentStart}, Kind: c.Note.Kind}
		roots := rootPIDs(s, c)
		for _, p := range below.tree(s, roots, skip) {
			if p.Zombie {
				continue
			}
			var parent procKey
			if q, ok := s[p.PPID]; ok && !slices.Contains(roots, p.PID) {
				parent = procKey{q.PID, q.Start}
			}
			var version uint32
			if platform.KnowsOriginalParent {
				if l, ok := platform.ProcessLineage(p.PID); ok {
					version = l.Version
				}
			}
			w.ancestry.record(procKey{p.PID, p.Start}, o, parent, version, now)
		}
	}
	for pid, start := range protected {
		key := procKey{pid, start}
		if _, recorded := w.ancestry.ownerOf(key); !recorded {
			continue
		}
		why := whyProtected
		if _, known := w.known[key]; known {
			why = whyAgent
		}
		w.dropAndLeave(s, key, why)
	}
}

// dropAndLeave drops key, a process found not to be the work of what it was
// recorded under, from the ancestry store with everything recorded below it
// (see ancestry.drop), so nothing is tied by parent to it or to what it
// started. Every root of a tracked unit that was dropped, and key when it is
// one, leaves its unit for good (see finalNo): key with why, any other with
// whyTiedDropped. A unit left with no root is forgotten with one ended record
// naming why its last root left; the command of any other follows its first
// live root.
func (w *watcher) dropAndLeave(s snapshot, key procKey, why string) {
	dropped := map[procKey]bool{key: true}
	for _, k := range w.ancestry.drop(key) {
		dropped[k] = true
	}
	for id, c := range w.tracked {
		if c.Detached == nil {
			continue
		}
		left := ""
		roots := make([]detachedRoot, 0, len(c.Detached.Roots))
		for _, r := range c.Detached.Roots {
			rk := procKey{r.PID, r.Start}
			if !dropped[rk] {
				roots = append(roots, r)
				continue
			}
			w.classified[rk] = finalNo(s[r.PID])
			left = whyTiedDropped
			if rk == key {
				left = why
			}
		}
		if left == "" {
			continue
		}
		c.Detached.Roots = roots
		if len(roots) == 0 {
			w.f.event(slog.LevelInfo, EventEnded, "ended", append(w.endAttrs(c), "reason", left)...)
			w.untrack(id)
			continue
		}
		followFirstRoot(s, &c)
		w.tracked[id] = c
	}
}

// cachedStamp is a readable stamp kept for a process with its process name
// when it was read: once the name changes the process runs another program,
// with an environment of its own, so the stamp is read again.
type cachedStamp struct {
	stamp
	name string
}

// stampOf is pid's stamp in s, read once per program a process runs (see
// readStamp) and kept in stamps while it runs; one the kernel hides is read
// again next time. A stamp whose agent stamp names a live process that
// started before pid makes that process protected while it lives (see
// agents), and at once in protected when it is not nil.
func (w *watcher) stampOf(s snapshot, pid int, protected map[int]uint64) stamp {
	p, ok := s[pid]
	if !ok {
		return stamp{}
	}
	key := procKey{pid, p.Start}
	cached, ok := w.stamps[key]
	st := cached.stamp
	if !ok || cached.name != p.Name {
		delete(w.stamps, key)
		// Only a readable stamp is kept: a process forked but not yet exec'd
		// shows its parent's environment, which a restricted shell hides, and
		// shows its own once it execs.
		if st = readStamp(pid); st.Readable {
			w.stamps[key] = cachedStamp{st, p.Name}
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

// judged is unitRootWhy's verdict on a child of the init process, with the
// process name it was judged under. fixed marks a verdict that is never
// judged again (see finalWhy), and every root that left its unit: it was
// that unit's, was judged again and failed, so it never comes back.
type judged struct {
	v     verdict
	name  string
	fixed bool
}

// judgedAs is the judgement of p by unitRootWhy's verdict v and its reason why.
func judgedAs(p proc, v verdict, why string) judged {
	return judged{v: v, name: p.Name, fixed: v == notUnit && finalWhy(why)}
}

// finalNo is a final "no unit" for p: a root that left its unit, or one a
// watcher handing over judged final.
func finalNo(p proc) judged {
	return judged{v: notUnit, name: p.Name, fixed: true}
}

// findUnits finds and judges again the units of this tick, after matching
// and recordLineage. The roots of the tracked units are judged again first
// (see rejudgeRoots). Then each child of an init process that is the
// watcher's user's and not a zombie goes through unitRootWhy: once, each tick
// while its verdict is unread, and again once a no that rests only on the
// absence of evidence (see finalWhy) is followed by another program (its
// process name changed); a final no is never judged again, and drops what
// was recorded of the process and below it (see dropAndLeave). A root tied by
// parent to a tracked unit joins it; any other becomes a tracked unit, logged
// tracking. Where the kernel keeps the original parent but does not answer
// for the init process, it judges nothing but whether a root has become a
// known agent, which needs no lineage, and logs that once, and once more
// when it answers again.
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
			// A rule that needs lineage must not drop a unit on a failed read.
			w.rejudgeRoots(s, facts{known: w.known, protected: protected}, false)
			return
		}
		if w.lineageLost {
			w.lineageLost = false
			w.f.event(slog.LevelInfo, EventError,
				"can read where processes come from again; detached work is tracked")
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
		origins:   w.ancestry,
		records:   w.records,
	}
	w.rejudgeRoots(s, f, true)
	var roots []proc
	for _, p := range s {
		if p.UID != self || p.Zombie || !childOfInit(s, p) {
			continue
		}
		if j, ok := w.classified[procKey{p.PID, p.Start}]; ok &&
			(j.v == isUnit || j.fixed || j.v == notUnit && j.name == p.Name) {
			continue
		}
		roots = append(roots, p)
	}
	// The earlier root first, so a unit is named by its first root.
	slices.SortFunc(roots, olderFirst)
	for _, r := range roots {
		key := procKey{r.PID, r.Start}
		if j, ok := w.classified[key]; ok && j.fixed {
			continue // a final no given this tick, by a drop
		}
		t, v, why := unitRootWhy(s, r, f)
		w.classified[key] = judgedAs(r, v, why)
		switch {
		case v == isUnit:
			w.addRoot(s, r, t)
		case v == notUnit && finalWhy(why):
			w.dropAndLeave(s, key, why)
		}
	}
}

// rejudgeRoots judges each live root of every tracked unit again.
//
// A root that has become an agent the guard knows, or protected (a stamp
// names it as its agent), leaves its unit at once, with or without lineage.
//
// A root seen with a new process name or command line runs another program
// than before, such as a fork caught before it started its own: its Name,
// Args and Since are taken from it at once, whatever the verdict, so its
// time before the guard's own stop may take it starts again (see ownStop),
// and it is judged again (Pending) with fresh stamps through every rule of
// unitRootWhy. One that is now off, Workbench, another agent's or any other no
// leaves its unit for good. One still a unit whose stamps say dry-run makes
// its unit a dry run. One whose lineage cannot be read, or that has nothing
// readable, stays and is judged again next tick. Without lineage (whole
// false) nothing is judged beyond the first paragraph.
//
// Every root that leaves takes with it what was recorded of it and below it
// (see dropAndLeave), and a unit left with no root is forgotten with one
// ended record naming why. The unit's command follows its first live root.
func (w *watcher) rejudgeRoots(s snapshot, f facts, whole bool) {
	type leaving struct {
		key procKey
		why string
	}
	var leaves []leaving
	for id, c := range w.tracked {
		if c.Detached == nil {
			continue
		}
		for i := range c.Detached.Roots {
			r := &c.Detached.Roots[i]
			key := procKey{r.PID, r.Start}
			if !s.alive(r.PID, r.Start) {
				continue // forget drops it once gone
			}
			p := s[r.PID]
			if _, known := f.known[key]; known {
				leaves = append(leaves, leaving{key, whyAgent})
				continue
			}
			if underProtected(s, p, f.protected) {
				leaves = append(leaves, leaving{key, whyProtected})
				continue
			}
			if args := strings.Join(platform.Args(r.PID), " "); p.Name != r.Name || args != r.Args {
				r.Name, r.Args, r.Since, r.Pending = p.Name, args, time.Now(), true
			}
			if !whole || !r.Pending {
				continue
			}
			// Fresh stamps, for the root and what is below it.
			for _, q := range s.tree(r.PID, nil) {
				delete(w.stamps, procKey{q.PID, q.Start})
			}
			t, v, rootWhy := unitRootWhy(s, p, f)
			switch {
			case v == isUnit:
				if t.Mode == "dry-run" {
					c.Note.DryRun = true
				}
				r.Pending = false
			case v == notUnit && rootWhy != whyNoLineage:
				leaves = append(leaves, leaving{key, rootWhy})
			}
		}
		followFirstRoot(s, &c)
		w.tracked[id] = c
	}
	for _, l := range leaves {
		w.dropAndLeave(s, l.key, l.why)
	}
}

// followFirstRoot makes a unit's Root its first live root, and its command
// that root's program as it was last judged: its process name in the note,
// which may be logged, and its command line in Shown, which only the view
// shows.
func followFirstRoot(s snapshot, c *command) {
	c.Root = firstRoot(s, *c)
	for _, r := range c.Detached.Roots {
		if s.alive(r.PID, r.Start) {
			c.Note.Command = cmp.Or(r.Name, programName(s[r.PID]))
			c.Shown = r.Args
			return
		}
	}
}

// addRoot tracks r, a unit's root tied by t: in the unit that forked it when
// that unit is tracked, else as a new unit, whose call it resolves once. A
// root that joins a unit and whose stamps say dry-run makes that unit a dry
// run. r is recorded in the ancestry store as its unit's, with the process
// that tied it as its parent (see tie.Parent) when it was not recorded
// before, so dropping what tied it takes it from its unit (see dropAndLeave).
func (w *watcher) addRoot(s snapshot, r proc, t tie) {
	now := time.Now()
	root := detachedRoot{
		procRef: procRef{PID: r.PID, Start: r.Start},
		Since:   now,
		Name:    r.Name,
		Args:    strings.Join(platform.Args(r.PID), " "),
	}
	key := procKey{r.PID, r.Start}
	if u, ok := w.tracked[t.Owner]; ok && u.Detached != nil {
		w.ancestry.record(
			key,
			owner{ID: t.Owner, Agent: t.Agent, Kind: t.Kind},
			t.Parent,
			t.Version,
			now,
		)
		u.Detached.Roots = append(u.Detached.Roots, root)
		if t.Mode == "dry-run" {
			u.Note.DryRun = true
			w.tracked[t.Owner] = u
		}
		return
	}
	call, rec := w.resolveCall(r, t)
	cwd, _ := platform.Folder(r.PID)
	id := unitID(root.procRef)
	w.ancestry.record(key, owner{ID: id, Agent: t.Agent, Kind: t.Kind}, t.Parent, t.Version, now)
	n := note{
		ToolUseID:  id,
		AgentPID:   t.Agent.pid,
		AgentStart: t.Agent.start,
		Kind:       t.Kind,
		Command:    programName(r),
		Cwd:        cwd,
		Project:    projectName("", cwd),
		NotedAt:    platform.StartTime(r.Start),
		DryRun:     t.Mode == "dry-run" || call != "" && rec.DryRun,
	}
	if call != "" {
		n.Subagent = rec.Subagent
	}
	c := command{
		Note: n, Root: s[r.PID], Shown: root.Args,
		Detached: &detached{Roots: []detachedRoot{root}, Call: call},
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
		// its record, which the newest few thousand bound. Without one, the
		// call's tracked command still says its subagent and dry run.
		if rec, ok = w.records.byID(t.Owner); !ok {
			rec = callRecord{ID: t.Owner, Agent: t.Agent}
			if owner, tracked := w.tracked[t.Owner]; tracked {
				rec.Subagent, rec.DryRun = owner.Note.Subagent, owner.Note.DryRun
			}
			return t.Owner, rec
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

// forgetUnit is forget for a unit: a root gone for two rounds (see gone)
// leaves it, and the unit ends once no root is left or its agent is gone.
// Members the init process adopts after a root exits join the unit as roots
// (see findUnits) before that root is dropped. A unit is never put in ended:
// it has no note to be matched again.
func (w *watcher) forgetUnit(s snapshot, id string, c command) {
	agentGone := w.gone(s, c.Note.AgentPID, c.Note.AgentStart)
	c.Detached.Roots = slices.DeleteFunc(c.Detached.Roots, func(r detachedRoot) bool {
		return w.gone(s, r.PID, r.Start)
	})
	if !agentGone && len(c.Detached.Roots) > 0 {
		followFirstRoot(s, &c)
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
