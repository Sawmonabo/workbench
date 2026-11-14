package guard

// This file is the glimpse: what the watcher does between ticks when the
// kernel tells it that an agent, or the shell of one of its calls, forked or
// exec'd (see platform.Waiter.Follow). A call that ends within a tick is
// never seen by one, so without the glimpse its shell is never known and the
// work it leaves detached cannot be tied to it. The glimpse records only what
// the next tick would record if it came sooner: the shell of a call matched
// by the tick's own rules (see match) and that shell's direct children, with
// the call as owner, in the ancestry store and the call's record. It tracks,
// measures, logs and stops nothing; the tick stays the one place that does.

import (
	"cmp"
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// shellWakesCap is the most wakes, by its forks and its execs, a call's shell
// may cause in a second before it loses its fork watch for the rest of its
// life, keeping only its exit watch, and is left to the ticks. A shell that
// forks or execs this often runs a loop of programs. A shell forking 500
// times a second cost a follower about 4.3 % of a core (design 2.1 fact 25),
// so 250 wakes a second cost about 2 %; calls that leave work behind in
// their first moments wake it a few times, far below this.
const shellWakesCap = 250

// agentWakesCap is the most fork wakes an agent may cause in a second before
// it is not followed for its forks for agentPause, after which a tick follows
// it again; its exit stays watched as before, and its calls meanwhile are
// found at the ticks, as without the glimpse. A real Claude Code session
// woke a follower 85 times in all, so a hundred in one second is far beyond
// what an agent does when it runs commands, and bounds what an agent that
// forks in a loop can cost: following costs the watcher about 4 ms of CPU a
// call, for the three children a call makes (design 2.1 fact 34), so this is
// at most about a tenth of a core for one second in every agentPause.
const (
	agentWakesCap = 100
	agentPause    = 5 * time.Second
)

// wakes counts wakes in the present second.
type wakes struct {
	second time.Time
	n      int
}

// add counts one wake at now and gives the wakes of its second so far.
func (c *wakes) add(now time.Time) int {
	if now.Sub(c.second) >= time.Second {
		c.second, c.n = now, 0
	}
	c.n++
	return c.n
}

// glimpse is what the watcher follows between ticks: the known agents
// (agents, by PID), of which watched are those the waiter also watches for
// their exit, the agents' children it has looked at (seen), those it still
// follows because they had not exec'd yet when it looked (waiting, to their
// agent), the matched shells (shells, by PID) and the notes it gave a shell
// (given, by call ID, while the note is on disk). Each is pruned against the
// tick's snapshot in followAgents, so none holds more than the processes
// that ran at the last tick and what arrived since. Nothing of it is carried
// at a handover: the next watcher follows the known agents itself.
type glimpse struct {
	agents  map[int]procKey
	watched map[int]bool
	seen    map[procKey]bool
	waiting map[int]waitingChild
	shells  map[int]*shellWatch
	given   map[string]givenNote
	// refusedBy are the agents the kernel would not let it follow while they
	// ran, never asked again, and refused is set at the first, which is
	// logged once: their calls are then found at the ticks only.
	refusedBy map[procKey]bool
	refused   bool
	// agentWakes are each followed agent's wakes this second, and paused the
	// agents not followed for their forks until the time given (see
	// agentWakesCap).
	agentWakes map[int]*wakes
	paused     map[procKey]time.Time
}

// givenNote is a note the glimpse gave a shell: its agent and that shell. No
// other child takes it, as the tick gives each note one command (see match).
type givenNote struct {
	agent, shell procKey
}

// waitingChild is an agent's child that was still its agent's image (forked,
// not yet exec'd) when the glimpse looked: it is looked at again at its exec.
type waitingChild struct {
	key, agent procKey
}

// shellWatch is a matched shell: its process, its call as owner, and its
// wakes in the present second (see shellWakesCap).
type shellWatch struct {
	key    procKey
	owner  owner
	wakes  wakes
	capped bool
}

func newGlimpse() *glimpse {
	return &glimpse{
		agents:  map[int]procKey{},
		watched: map[int]bool{},
		seen:    map[procKey]bool{},
		waiting: map[int]waitingChild{},
		shells:  map[int]*shellWatch{},
		given:   map[string]givenNote{},

		refusedBy:  map[procKey]bool{},
		agentWakes: map[int]*wakes{},
		paused:     map[procKey]time.Time{},
	}
}

// followAgents follows each known agent that runs, between its calls too
// (agents, the ones with a note or a tracked command, are only those the
// waiter also watches for their exit, as before), and stops following one
// that is no longer known or runs no more; it forgets what it looked at of
// processes that are gone. Where the system cannot follow forks it does
// nothing. An agent paused for waking it too often (see agentWakesCap) is
// followed again by the first tick after its pause. An agent the kernel
// refuses while it runs is not asked again and
// keeps the exit watch it had; the first such refusal on a system that says
// it can follow is logged, once. One for an agent that exited meanwhile is
// not a refusal.
func (w *watcher) followAgents(s snapshot, agents []int) {
	if !platform.FollowsForks {
		return
	}
	if w.glimpse == nil {
		w.glimpse = newGlimpse()
	}
	g := w.glimpse
	// The waiter keeps an exit watch until the exit, so an agent watched once
	// stays watched.
	for _, pid := range agents {
		g.watched[pid] = true
	}
	now, at := map[int]bool{}, time.Now()
	for key := range w.known {
		if !s.alive(key.pid, key.start) {
			continue
		}
		now[key.pid] = true
		if until, ok := g.paused[key]; ok && at.Before(until) || g.agents[key.pid] == key ||
			g.refusedBy[key] {
			continue
		}
		delete(g.paused, key)
		if w.waiter.Follow(key.pid) {
			g.agents[key.pid] = key
			g.agentWakes[key.pid] = &wakes{}
			continue
		}
		info, err := platform.ProcessInfo(key.pid)
		if err != nil || info.Start != key.start || info.Zombie {
			continue // it exited meanwhile
		}
		g.refusedBy[key] = true
		if g.watched[key.pid] {
			// Follow gave up the exit watch it replaces; take it again.
			w.waiter.Watch(key.pid)
		}
		if !g.refused {
			g.refused = true
			w.f.event(slog.LevelWarn, EventError,
				"cannot follow an agent's forks; its calls are found at the ticks only",
				"process", key.pid)
		}
	}
	for key := range g.refusedBy {
		if !s.present(key.pid, key.start) {
			delete(g.refusedBy, key)
		}
	}
	for key := range g.paused {
		if !s.present(key.pid, key.start) {
			delete(g.paused, key)
		}
	}
	for pid := range g.agents {
		if !now[pid] {
			w.waiter.Unfollow(pid)
			delete(g.agents, pid)
			delete(g.agentWakes, pid)
		}
	}
	g.prune(s)
}

// prune forgets what the glimpse keeps of processes that are gone from s:
// the children looked at, the waiting children and the shells by their own
// process; the notes given by their agent (a note is also forgotten at the
// agent's next glimpse once it is gone from disk, see glimpseNotes); the
// exit watches by PID; and the wake counts of agents no longer followed.
func (g *glimpse) prune(s snapshot) {
	for key := range g.seen {
		if !s.present(key.pid, key.start) {
			delete(g.seen, key)
		}
	}
	for pid, c := range g.waiting {
		if !s.present(c.key.pid, c.key.start) {
			delete(g.waiting, pid)
		}
	}
	for pid, sh := range g.shells {
		if !s.present(sh.key.pid, sh.key.start) {
			delete(g.shells, pid)
		}
	}
	for id, given := range g.given {
		if !s.present(given.agent.pid, given.agent.start) {
			delete(g.given, id)
		}
	}
	for pid := range g.watched {
		if _, ok := s[pid]; !ok {
			delete(g.watched, pid)
		}
	}
	for pid := range g.agentWakes {
		if _, ok := g.agents[pid]; !ok {
			delete(g.agentWakes, pid)
		}
	}
}

// wait waits for d, or until a watched agent exits, as the waiter did alone
// before; meanwhile it handles each fork and exec of what it follows (see
// glimpseEvent). A wake neither hurries nor delays the next tick: the wait
// keeps its deadline, and a storm of wakes is bounded per shell and per
// agent (see shellWakesCap, agentWakesCap).
func (w *watcher) wait(stop func() bool, d time.Duration) {
	deadline := time.Now().Add(d)
	for !stop() {
		left := time.Until(deadline)
		if left <= 0 {
			return
		}
		agentExited := false
		for _, e := range w.waiter.Wait(left) {
			agentExited = w.glimpseEvent(e) || agentExited
		}
		if agentExited {
			return
		}
	}
}

// glimpseEvent handles one event and reports whether it was a watched
// agent's exit, which ends the wait as it did before.
func (w *watcher) glimpseEvent(e platform.Event) bool {
	g := w.glimpse
	if g == nil {
		// Nothing is followed: every event is a watched agent's exit.
		return e.Exit
	}
	if agent, ok := g.agents[e.PID]; ok {
		if e.Exit {
			// The waiter woke for this exit before only for an agent it
			// watched; a known agent between its calls it did not.
			delete(g.agents, e.PID)
			delete(g.agentWakes, e.PID)
			watched := g.watched[e.PID]
			delete(g.watched, e.PID)
			return watched
		}
		if e.Fork {
			w.agentForked(agent)
		}
		return false
	}
	if c, ok := g.waiting[e.PID]; ok {
		switch {
		case e.Exit:
			delete(g.waiting, e.PID)
		case e.Exec:
			delete(g.waiting, e.PID)
			w.glimpseChild(c.agent, c.key)
		}
		return false
	}
	if sh, ok := g.shells[e.PID]; ok {
		// The kernel folds what happened since the last wait into one event,
		// so a shell that forked and exited at once reports both: its
		// children are read before it is forgotten.
		if (e.Exec || e.Fork) && !sh.capped {
			w.shellChanged(sh, e.Exec && !e.Exit, e.Fork)
		}
		if e.Exit {
			delete(g.shells, e.PID)
		}
		return false
	}
	// Else an agent the waiter only watches (one it could not follow): its
	// exit ends the wait. A capped shell is still among the shells above. An
	// exit the waiter reports for a process that still runs is the one it
	// gives when the kernel refuses a follow (see platform.Waiter.Follow),
	// not an exit.
	if !e.Exit {
		return false
	}
	p, err := platform.ProcessInfo(e.PID)
	return err != nil || p.Zombie
}

// agentForked handles a fork of a followed agent: its new children are looked
// at, unless it has woken the watcher more than agentWakesCap times this
// second, which pauses following its forks for agentPause (its exit stays
// watched when it was).
func (w *watcher) agentForked(agent procKey) {
	g := w.glimpse
	now := time.Now()
	count := g.agentWakes[agent.pid]
	if count == nil {
		count = &wakes{}
		g.agentWakes[agent.pid] = count
	}
	if count.add(now) <= agentWakesCap {
		w.glimpseAgent(agent)
		return
	}
	if g.watched[agent.pid] {
		w.waiter.Watch(agent.pid)
	}
	w.waiter.Unfollow(agent.pid)
	delete(g.agents, agent.pid)
	delete(g.agentWakes, agent.pid)
	g.paused[agent] = now.Add(agentPause)
}

// glimpseAgent looks at each child of agent it has not looked at: it follows
// it first, so an exec right after the listing is never missed, then reads
// and matches it (see glimpseChild).
func (w *watcher) glimpseAgent(agent procKey) {
	g := w.glimpse
	// The kernel lists children newest first; the tick gives the oldest
	// matching child the oldest note (see match), so the glimpse looks at
	// them oldest first too.
	var kids []platform.Process
	for _, pid := range platform.Children(agent.pid) {
		p, err := platform.ProcessInfo(pid)
		if err == nil && !p.Zombie && p.PPID == agent.pid {
			kids = append(kids, p)
		}
	}
	slices.SortFunc(kids, func(a, b platform.Process) int {
		return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(a.PID, b.PID))
	})
	for _, p := range kids {
		key := procKey{p.PID, p.Start}
		if g.seen[key] || w.protectedNow(key) {
			continue
		}
		g.seen[key] = true
		if !w.waiter.Follow(p.PID) {
			continue // it exited meanwhile
		}
		w.glimpseChild(agent, key)
	}
}

// glimpseChild reads child, a followed child of agent, and matches it against
// the agent's notes that no command was tracked for and that have not ended,
// read from disk now, since the note of a call whose shell just started was
// usually written after the last tick. A match is recorded as the call's
// shell (see glimpseShell); a child still in its agent's image waits for its
// exec; any other is no longer followed.
func (w *watcher) glimpseChild(agent, child procKey) {
	unfollow := func() { w.waiter.Unfollow(child.pid) }
	a, errA := platform.ProcessInfo(agent.pid)
	p, errP := platform.ProcessInfo(child.pid)
	if errA != nil || errP != nil || a.Start != agent.start || p.Start != child.start || p.Zombie {
		unfollow()
		return
	}
	notes := w.glimpseNotes(agent)
	if len(notes) == 0 {
		unfollow()
		return
	}
	callIDs, folders := false, false
	for _, n := range notes {
		callIDs = callIDs || n.Kind == codex
		folders = folders || len(noteFolders(n)) > 0
	}
	s := snapshot{agent.pid: {Process: a}, child.pid: {Process: p}}
	loadArgs(s, agent.pid, callIDs, folders)
	taken := map[int]bool{}
	for _, c := range w.tracked {
		taken[c.Root.PID] = true
	}
	matched := match(s, notes, taken, w.setsCallIDs)
	switch {
	case len(matched) == 1 && ticksAnswer(s, matched[0], notes, w.glimpse.olderWaiting(agent, child)):
		w.glimpseShell(matched[0], child)
	case len(matched) == 0 && p.NotExeced:
		// Still its agent's program image: looked at again at its exec.
		w.glimpse.waiting[child.pid] = waitingChild{key: child, agent: agent}
	default:
		unfollow()
	}
}

// ticksAnswer reports whether the tick, seeing every child of the agent,
// would give c's note to c's root as the glimpse, seeing that child alone,
// does. It would when no other note the child may match has the same text.
// Else the tick gives each note the oldest child that runs in the best
// folder its note's folders allow (see match, folderRank), so it would when
// the root runs in that folder and no older child of the agent may still
// match: none is waiting for its exec (olderWaiting). Else the glimpse leaves
// the note to the tick, which finds the command as it did before the glimpse
// existed.
func ticksAnswer(s snapshot, c command, notes []note, olderWaiting bool) bool {
	if !slices.ContainsFunc(notes, func(n note) bool {
		return n.ToolUseID != c.Note.ToolUseID && n.Command == c.Note.Command
	}) {
		return true
	}
	dirs := noteFolders(c.Note)
	return !olderWaiting && s.folderRank(c.Root, dirs) == len(dirs)-1
}

// olderWaiting reports whether a child of agent older than child is still
// waiting for its exec, so it may yet match one of the agent's notes.
func (g *glimpse) olderWaiting(agent, child procKey) bool {
	for _, c := range g.waiting {
		if c.agent == agent && c.key != child &&
			(c.key.start < child.start || c.key.start == child.start && c.key.pid < child.pid) {
			return true
		}
	}
	return false
}

// glimpseShell records shell, the matched command c's root, as its call's
// shell: in the call's record and, with no parent, in the ancestry store,
// owned by the call, as the tick records a tracked command's shell (see
// recordLineage); then its children now. It stays followed, for its exec
// and its forks.
func (w *watcher) glimpseShell(c command, shell procKey) {
	if w.protectedNow(shell) {
		w.waiter.Unfollow(shell.pid)
		return
	}
	o := owner{
		ID:    c.Note.ToolUseID,
		Agent: procKey{c.Note.AgentPID, c.Note.AgentStart},
		Kind:  c.Note.Kind,
	}
	w.glimpse.given[c.Note.ToolUseID] = givenNote{agent: o.Agent, shell: shell}
	w.records.add(c.Note)
	w.records.tracked(c.Note.ToolUseID, c.Root)
	w.recordGlimpsed(shell, o, procKey{})
	sh := &shellWatch{key: shell, owner: o}
	w.glimpse.shells[shell.pid] = sh
	w.recordChildren(sh)
}

// shellChanged handles an exec or a fork of a followed shell, one wake: at
// an exec its version is recorded again, at a fork its children. A shell
// that has woken the watcher more than shellWakesCap times this second loses
// its fork watch for the rest of its life (its exit stays watched), and one
// that is no longer its call's (see stillTheCalls) is no longer followed;
// nothing is recorded for either.
func (w *watcher) shellChanged(sh *shellWatch, execed, forked bool) {
	if sh.wakes.add(time.Now()) > shellWakesCap {
		sh.capped = true
		w.waiter.Watch(sh.key.pid)
		w.waiter.Unfollow(sh.key.pid)
		return
	}
	if !w.stillTheCalls(sh) {
		return
	}
	if execed {
		w.recordGlimpsed(sh.key, sh.owner, procKey{})
	}
	if forked {
		w.recordChildren(sh)
	}
}

// stillTheCalls reports whether sh is still its call's shell, as the tick
// would record it: not protected now (a stamp may name it as an agent, or it
// may have exec'd into one), and recorded in the store as the call's. When it
// is not, the tick has left it out of the call's tree, with everything under
// it, and dropped its entry (see recordLineage), so the glimpse stops
// following it and forgets it, and records nothing more under it.
func (w *watcher) stillTheCalls(sh *shellWatch) bool {
	if o, ok := w.ancestry.ownerOf(sh.key); ok && o.ID == sh.owner.ID && !w.protectedNow(sh.key) {
		return true
	}
	w.waiter.Unfollow(sh.key.pid)
	delete(w.glimpse.shells, sh.key.pid)
	return false
}

// recordChildren records each direct child of sh's shell, with the shell as
// its parent and the shell's call as owner, leaving out a protected child as
// the tick does. Nothing below is recorded or followed. The caller has made
// sure the shell is still its call's.
func (w *watcher) recordChildren(sh *shellWatch) {
	for _, pid := range platform.Children(sh.key.pid) {
		p, err := platform.ProcessInfo(pid)
		if err != nil || p.Zombie || p.PPID != sh.key.pid {
			continue
		}
		w.recordGlimpsed(procKey{pid, p.Start}, sh.owner, sh.key)
	}
}

// recordGlimpsed records key in the ancestry store under o with parent, and
// its version now, through the store's one entry point, as the tick records
// a tree's members; a protected process is left out, as the tick leaves it
// out (see protectedNow). Stamps are not read here: the tick reads them.
func (w *watcher) recordGlimpsed(key procKey, o owner, parent procKey) {
	if w.protectedNow(key) {
		return
	}
	var version uint32
	if l, ok := platform.ProcessLineage(key.pid); ok {
		version = l.Version
	}
	w.ancestry.record(key, o, parent, version, time.Now())
}

// protectedNow reports whether key is a process the tick would leave out of
// a recorded tree as protected: PID 0 or 1, the watcher, a known agent or a
// process a stamp names as its agent.
func (w *watcher) protectedNow(key procKey) bool {
	if key.pid <= 1 || key.pid == os.Getpid() || w.stampAgents[key] {
		return true
	}
	_, known := w.known[key]
	return known
}

// glimpseNotes are agent's notes a child may match: on disk, with no tracked
// command, not ended (see untracked), and not given a shell by the glimpse
// already. A given note is forgotten once it is gone from disk.
func (w *watcher) glimpseNotes(agent procKey) []note {
	onDisk := w.peekNotes(agent)
	present := map[string]bool{}
	for _, n := range onDisk {
		present[n.ToolUseID] = true
	}
	g := w.glimpse
	for id, given := range g.given {
		if given.agent == agent && !present[id] {
			delete(g.given, id)
		}
	}
	return slices.DeleteFunc(w.untracked(onDisk), func(n note) bool {
		_, given := g.given[n.ToolUseID]
		return given
	})
}

// peekNotes are agent's notes on disk, read without changing anything: a
// note that cannot be read or does not parse is skipped, and the tick deals
// with it (see notes); one whose post hook ran is left out.
func (w *watcher) peekNotes(agent procKey) []note {
	entries, err := os.ReadDir(w.f.notes)
	if err != nil {
		return nil
	}
	var out []note
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !idPattern.MatchString(id) {
			continue
		}
		data, err := os.ReadFile(w.f.note(id))
		if err != nil {
			continue
		}
		var n note
		if json.Unmarshal(data, &n) != nil || n.ToolUseID != id || n.Ended ||
			n.AgentPID != agent.pid || n.AgentStart != agent.start {
			continue
		}
		out = append(out, n)
	}
	return out
}
