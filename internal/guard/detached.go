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
// longer the outer call's. Where the kernel keeps the original parent, each
// member's stamp is read in the walk, through the cache (see stampOf), from
// the second tick the member is recorded at (its versions are recorded from
// the first), so a live process a member names as its agent, older than the
// member, is protected from this tick and is dropped with the rest: a
// session started without the guard's hooks is protected from the first
// readable program that carries its CLAUDE_PID. Elsewhere no member's stamp
// is read. A call whose tree holds a protected process other than the
// watcher, found in the walk or in that drop, started an agent (see
// markStartedAgents).
func (w *watcher) recordLineage(s snapshot, below parents, protected map[int]uint64) {
	now := time.Now()
	self := os.Getpid()
	w.ancestry.prune(func(k procKey) bool { return s.alive(k.pid, k.start) }, now)
	for id, c := range w.tracked {
		o := owner{ID: id, Agent: procKey{c.Note.AgentPID, c.Note.AgentStart}, Kind: c.Note.Kind}
		roots := rootPIDs(s, c)
		skip := func(p proc) bool {
			if start, ok := protected[p.PID]; ok && start == p.Start && p.PID > 1 {
				if p.PID != self {
					w.markStartedAgent(id)
				}
				return true
			}
			return p.PID <= 1
		}
		for _, p := range below.tree(s, roots, skip) {
			if p.Zombie {
				continue
			}
			var parent procKey
			if q, ok := s[p.PPID]; ok && !slices.Contains(roots, p.PID) {
				parent = procKey{q.PID, q.Start}
			}
			var prog program
			if platform.KnowsOriginalParent {
				if l, ok := platform.ProcessLineage(p.PID); ok {
					prog.version = l.Version
				}
			}
			// A member's stamp is read from the second tick it is seen on:
			// most processes of a busy build live less than a tick, and work a
			// session leaves in its first tick is tied to it only through the
			// store, which drops it once the session is known. Where the
			// kernel keeps no original parent, members' stamps are not read:
			// protecting by them there is not verified.
			before := w.ancestry.record(procKey{p.PID, p.Start}, o, parent, prog.version, now)
			if before && platform.KnowsOriginalParent {
				w.stampFor(s, p, prog, protected)
			}
		}
	}
	for pid, start := range protected {
		key := procKey{pid, start}
		o, recorded := w.ancestry.ownerOf(key)
		if !recorded {
			continue
		}
		if pid != self {
			w.markStartedAgent(o.ID)
		}
		why := whyProtected
		if _, known := w.known[key]; known {
			why = whyAgent
		}
		w.dropAndLeave(s, key, why)
	}
	w.markStartedAgents(s)
}

// markStartedAgents marks each call whose call stamp or Codex call ID a
// known agent's own environment holds: that call's command started the
// agent, which inherited the stamp (see callRecord.StartedAgent).
func (w *watcher) markStartedAgents(s snapshot) {
	for agent := range w.known {
		if !s.alive(agent.pid, agent.start) {
			continue
		}
		if rec, ok := w.stampCall(s, w.stampOf(s, agent.pid, nil)); ok {
			w.records.startedAgent(rec.ID)
		}
	}
}

// markStartedAgent marks the call of the owner id, a call or a unit whose
// call resolved, as one that started an agent.
func (w *watcher) markStartedAgent(id string) {
	if strings.HasPrefix(id, unitPrefix) {
		u, ok := w.tracked[id]
		if !ok || u.Detached == nil || u.Detached.Call == "" {
			return
		}
		id = u.Detached.Call
	}
	w.records.startedAgent(id)
}

// stampCall is the record of the call a stamp names: by its Codex call ID,
// else by its call stamp written by its own agent (see records.byShell).
func (w *watcher) stampCall(s snapshot, st stamp) (callRecord, bool) {
	if rec, ok := resolve(w.records, st.Codex); ok {
		return rec, true
	}
	a, ok := s[st.Agent]
	if !st.call() || !ok {
		return callRecord{}, false
	}
	return w.records.byShell(procKey{a.PID, a.Start}, st.CallShell, st.CallSecond)
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
			w.classified[rk] = finalNo()
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

// program names the program a process runs, as the guard tells one from the
// next: where the kernel keeps versions (platform.KnowsOriginalParent), its
// version, which the kernel raises at every exec and at nothing else, so a
// program that rewrites its title keeps it and one that execs the same path
// and arguments does not; elsewhere its process name and command line. The
// zero value is a program the guard could not read.
type program struct {
	version uint32
	name    string
	args    string
}

// programOf is the program p runs.
func programOf(p proc) program {
	if platform.KnowsOriginalParent {
		if l, ok := platform.ProcessLineage(p.PID); ok {
			return program{version: l.Version}
		}
		return program{}
	}
	return program{name: p.Name, args: strings.Join(platform.Args(p.PID), " ")}
}

// programSeen is the program p runs as far as one cheap look tells: its
// version where the kernel keeps versions, as programOf; elsewhere its
// process name alone, which the snapshot holds, so no command line is read.
// It tells a child of the init process that is no tracked root, and a
// cached stamp, from the next program; a tracked root, which may be stopped
// on its verdict, is followed by programOf.
func programSeen(p proc) program {
	if platform.KnowsOriginalParent {
		return programOf(p)
	}
	return program{name: p.Name}
}

// known reports whether the program was read.
func (prog program) known() bool { return prog != program{} }

// replaced reports whether now, read later of the same process, is another
// program than prog. A failed or empty read never counts as one, and nor
// does a process name or command line that changed where the kernel keeps
// versions.
func (prog program) replaced(now program) bool {
	if platform.KnowsOriginalParent {
		return now.version != 0 && now.version != prog.version
	}
	return now.name != "" && now.name != prog.name || now.args != "" && now.args != prog.args
}

// judged is unitRootWhy's verdict on a child of the init process, with the
// program it was judged in. fixed marks a verdict that is never judged again
// (see finalWhy), and every root that left its unit: it was that unit's, was
// judged again and failed, so it never comes back.
type judged struct {
	v       verdict
	program program
	fixed   bool
}

// judgedAs is the judgement of a process running prog by unitRootWhy's
// verdict v and its reason why.
func judgedAs(prog program, v verdict, why string) judged {
	return judged{v: v, program: prog, fixed: v == notUnit && finalWhy(why)}
}

// finalNo is a final "no unit": for a root that left its unit, or one a
// watcher handing over judged final.
func finalNo() judged {
	return judged{v: notUnit, fixed: true}
}

// findUnits finds and judges again the units of this tick, after matching
// and recordLineage; below is the tick's parent index of s. The roots of the
// tracked units are judged again first (see rejudgeRoots). Then each child
// of an init process that is the watcher's user's and not a zombie goes
// through unitRootWhy: once, each tick while its verdict is unread, and again
// once a no that rests only on the absence of evidence (see finalWhy) is
// followed by another program (see programSeen); a final no is never judged
// again, and drops what was recorded of the process and below it (see
// dropAndLeave). A root tied by parent to a tracked unit joins it; any other
// becomes a tracked unit, logged tracking. Where the kernel keeps the
// original parent but does not answer for the init process, it judges only
// whether a root has become a known agent or come under a protected process,
// which needs no lineage, finds no new unit, and makes every root of every
// unit Pending, so the guard's own stop takes no detached work meanwhile (see
// rejudgeRoots); it logs that once, and once more when it answers again.
func (w *watcher) findUnits(s snapshot, below parents, protected map[int]uint64) {
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
	w.judgeUnits(s, facts{
		self:      os.Getuid(),
		lineage:   platform.KnowsOriginalParent,
		read:      platform.ProcessLineage,
		stamp:     func(pid int) stamp { return w.stampOf(s, pid, protected) },
		args:      platform.Args,
		known:     w.known,
		protected: protected,
		origins:   w.ancestry,
		records:   w.records,
		below:     below,
	})
}

// judgeUnits is findUnits once lineage answers, with what the rules read in
// f: it judges the tracked units' roots again (see rejudgeRoots), then each
// child of an init process findUnits says, and tracks, joins or drops it.
func (w *watcher) judgeUnits(s snapshot, f facts) {
	w.rejudgeRoots(s, f, true)
	type orphan struct {
		p    proc
		prog program
	}
	var roots []orphan
	for _, p := range s {
		if p.UID != f.self || p.Zombie || !childOfInit(s, p, f.protected) {
			continue
		}
		j, judgedBefore := w.classified[procKey{p.PID, p.Start}]
		if judgedBefore && (j.v == isUnit || j.fixed) {
			continue
		}
		prog := programSeen(p)
		if judgedBefore && j.v == notUnit && !j.program.replaced(prog) {
			continue
		}
		roots = append(roots, orphan{p, prog})
	}
	// The earlier root first, so a unit is named by its first root.
	slices.SortFunc(roots, func(a, b orphan) int { return olderFirst(a.p, b.p) })
	for _, r := range roots {
		key := procKey{r.p.PID, r.p.Start}
		if j, ok := w.classified[key]; ok && j.fixed {
			continue // a final no given this tick, by a drop
		}
		t, v, why := unitRootWhy(s, r.p, f)
		w.classified[key] = judgedAs(r.prog, v, why)
		switch {
		case v == isUnit:
			w.addRoot(s, r.p, t)
		case v == notUnit && finalWhy(why):
			w.dropAndLeave(s, key, why)
		}
	}
}

// rejudgeRoots judges each live root of every tracked unit again, after a
// unit tied by stamp alone has lost a call that no longer proves it (see
// loseStampCall).
//
// A root that has become an agent the guard knows, or protected (a stamp
// names it as its agent), leaves its unit at once, with or without lineage.
//
// A root found running another program than the one it was judged in (see
// programOf), such as a fork caught before it started its own, starts its
// time before the guard's own stop may take it again (Since, see ownStop)
// and is Pending: not settled, so never chosen by the guard itself, until it
// is judged in its new program. So is one whose program cannot be read, as
// while the kernel does not answer, since it may run another. Its Name and
// Args follow the process whenever they differ, which alone resets nothing.
//
// A Pending root is judged through every rule of unitRootWhy at each tick
// the kernel answers. One that is a unit is settled, unless nothing readable
// was found for its setting (see tie.Hidden): then it stays Pending and is
// judged again next tick. One still a unit whose stamps say dry-run, or
// whose setting is unread while its agent's own stamp says dry-run or off,
// makes its unit a dry run. One that is now off, Workbench, another agent's
// or any other no leaves its unit for good. One whose lineage cannot be read,
// or that has nothing readable at all, stays Pending.
//
// Without lineage (whole false: the kernel does not answer where processes
// come from) nothing is judged beyond the first two paragraphs, and every
// root that stays is Pending, so the guard's own stop takes no detached work
// until lineage is read again and each root is judged again; a person's stop
// is unchanged.
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
		agent := procKey{c.Note.AgentPID, c.Note.AgentStart}
		w.loseStampCall(&c)
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
			followProgram(p, r)
			if !whole {
				// Where processes come from cannot be read: no root is
				// settled, whatever it ran, until it is judged again.
				r.Pending = true
				continue
			}
			if !r.Pending {
				continue
			}
			t, v, why := unitRootWhy(s, p, f)
			switch {
			case v == isUnit:
				r.Pending = t.Hidden
				if t.Mode == "dry-run" || t.Hidden && w.agentDry(s, agent) {
					c.Note.DryRun = true
				}
			case v == notUnit && why != whyNoLineage:
				leaves = append(leaves, leaving{key, why})
			}
		}
		followFirstRoot(s, &c)
		w.tracked[id] = c
	}
	for _, l := range leaves {
		w.dropAndLeave(s, l.key, l.why)
	}
}

// loseStampCall takes from c, a unit tied by stamp alone, a call that no
// longer proves it (see records.stampProves): its call and the subagent it
// was labelled with, so the guard never chooses it; a person still may.
func (w *watcher) loseStampCall(c *command) {
	if d := c.Detached; d != nil && d.ByStamp && d.Call != "" && !w.records.stampProves(d.Call) {
		d.Call, c.Note.Subagent = "", ""
	}
}

// followProgram brings r, a live root of a unit, up to date with p, its
// process: a root that runs another program than the one it was judged in,
// or whose program cannot be read, is Pending (see rejudgeRoots), and the
// former starts its time again; its Name, Args (where they decide) and Shown
// follow p whenever they differ. Where the kernel keeps versions the command
// line decides nothing, so only the shown form is read again, at each call:
// a program that rewrites its title keeps its version. Elsewhere the command
// line just read is what changes, so the shown form is read again only when
// it did, and no read is added to each tick there.
func followProgram(p proc, r *detachedRoot) {
	now := programOf(p)
	judgedIn := program{version: r.Version, name: r.Name, args: r.Args}
	switch {
	case !now.known():
		r.Pending = true
	case judgedIn.replaced(now) || !judgedIn.known():
		r.Version, r.Since, r.Pending = now.version, time.Now(), true
	}
	if p.Name != "" {
		r.Name = p.Name
	}
	changed := now.args != "" && now.args != r.Args
	if now.args != "" {
		r.Args = now.args
	}
	if !platform.KnowsOriginalParent && !changed && r.Shown != "" {
		return
	}
	if shown := shownCommand(p.PID, p.Name); shown != "" {
		r.Shown = shown
	}
}

// agentDry reports whether agent's own stamp says dry-run or off (see
// dryMode): a unit whose root's setting cannot be read takes it, as a
// person's stop of a row does.
func (w *watcher) agentDry(s snapshot, agent procKey) bool {
	return s.alive(agent.pid, agent.start) && dryMode(w.stampOf(s, agent.pid, nil))
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
			c.Shown = r.Shown
			return
		}
	}
}

// addRoot tracks r, a unit's root tied by t: in the unit that forked it when
// that unit is tracked, else as a new unit, whose call it resolves once. A
// root that joins a unit and whose stamps say dry-run makes that unit a dry
// run, as does one whose setting cannot be read (see tie.Hidden) while its
// agent's own stamp says dry-run or off; such a root is Pending until
// something readable is found for it (see rejudgeRoots). r is recorded in the
// ancestry store as its unit's, with the process that tied it as its parent
// (see tie.Parent) when it was not recorded before, so dropping what tied it
// takes it from its unit (see dropAndLeave).
func (w *watcher) addRoot(s snapshot, r proc, t tie) {
	now := time.Now()
	root := detachedRoot{
		procRef: procRef{PID: r.PID, Start: r.Start},
		Since:   now,
		Version: t.Version,
		Name:    r.Name,
		Args:    programOf(r).args,
		Shown:   shownCommand(r.PID, r.Name),
		Pending: t.Hidden,
	}
	key := procKey{r.PID, r.Start}
	dry := t.Mode == "dry-run" || t.Hidden && w.agentDry(s, t.Agent)
	if u, ok := w.tracked[t.Owner]; ok && u.Detached != nil {
		w.ancestry.record(
			key,
			owner{ID: t.Owner, Agent: t.Agent, Kind: t.Kind},
			t.Parent,
			t.Version,
			now,
		)
		u.Detached.Roots = append(u.Detached.Roots, root)
		u.Note.DryRun = u.Note.DryRun || dry
		w.tracked[t.Owner] = u
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
		DryRun:     dry || rec.DryRun,
	}
	if call != "" {
		n.Subagent = rec.Subagent
	}
	c := command{
		Note: n, Root: s[r.PID], Shown: root.Shown,
		Detached: &detached{Roots: []detachedRoot{root}, Call: call, ByStamp: t.Owner == ""},
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
// call did not start it. So does a tie by stamp alone to a call that started
// an agent session (see records.stampProves), whose record gives only its
// dry run.
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
	if t.Owner == "" && !w.records.stampProves(rec.ID) {
		// The call started an agent session, whose own processes carry its
		// stamps: the stamp alone no longer proves the call. Its dry run
		// still holds.
		return "", callRecord{DryRun: rec.DryRun}
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
