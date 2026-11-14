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
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// shellWakesCap is the most wakes a call's shell may cause in a second
// before it loses its fork watch for the rest of its life, keeping only its
// exit watch, and is left to the ticks. A shell that forks this often runs a
// loop of programs, and each wake costs a listing of its children and a
// version read: measured, about 86 µs of CPU per wake (25.9 ms for 303 wakes
// at 500 forks a second), so 250 a second costs about 2 % of a core, under
// research R1's uncapped worst case of 6.8 %. Calls that leave work behind in
// their first moments wake it a few times, far below this.
const shellWakesCap = 250

// agentWakesCap is the most fork wakes an agent may cause in a second before
// it is not followed for its forks for agentPause, after which a tick follows
// it again; its exit stays watched as before, and its calls meanwhile are
// found at the ticks, as without the glimpse. Research R1 saw 85 events in a
// whole real Claude Code session, so a hundred in one second is far beyond
// what an agent does when it runs commands, and bounds what an agent that
// forks in a loop can cost: a wake on an agent lists its children and reads
// each new one, which a fake agent measured at well under a millisecond (see
// the wiring report), so at most about a tenth of a core for one second in
// every agentPause.
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
// (given, by call ID, while the note is on disk). Nothing of it is carried
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
	for key := range g.seen {
		if !s.present(key.pid, key.start) {
			delete(g.seen, key)
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
		if e.Exec && !e.Exit {
			w.recordGlimpsed(sh.key, sh.owner, procKey{})
		}
		if e.Fork && !sh.capped {
			w.shellForked(sh)
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
	for _, pid := range platform.Children(agent.pid) {
		p, err := platform.ProcessInfo(pid)
		if err != nil || p.Zombie || p.PPID != agent.pid {
			continue
		}
		key := procKey{pid, p.Start}
		if g.seen[key] || w.protectedNow(key) {
			continue
		}
		g.seen[key] = true
		if !w.waiter.Follow(pid) {
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
	case len(matched) == 1:
		w.glimpseShell(matched[0], child)
	case slices.Equal(s[child.pid].Args, s[agent.pid].Args):
		w.glimpse.waiting[child.pid] = waitingChild{key: child, agent: agent}
	default:
		unfollow()
	}
}

// glimpseShell records shell, the matched command c's root, as its call's
// shell: in the call's record and, with no parent, in the ancestry store,
// owned by the call, as the tick records a tracked command's shell (see
// recordLineage); then its children now. It stays followed, for its exec
// and its forks.
func (w *watcher) glimpseShell(c command, shell procKey) {
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

// shellForked handles a fork of a followed shell: its children are recorded,
// unless it has woken the watcher more than shellWakesCap times this second,
// which takes its fork watch for the rest of its life (its exit stays
// watched).
func (w *watcher) shellForked(sh *shellWatch) {
	if sh.wakes.add(time.Now()) > shellWakesCap {
		sh.capped = true
		w.waiter.Watch(sh.key.pid)
		w.waiter.Unfollow(sh.key.pid)
		return
	}
	w.recordChildren(sh)
}

// recordChildren records each direct child of sh's shell, with the shell as
// its parent and the shell's call as owner. Nothing below is followed.
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
