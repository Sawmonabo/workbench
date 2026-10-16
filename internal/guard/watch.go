package guard

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/operation"
)

const (
	idleExit  = 10 * time.Minute
	cooldown  = 10 * time.Second
	killAfter = 5 * time.Second
	giveUp    = 10 * time.Second
	killKeep  = 24 * time.Hour
	// childLinger is how long a child of an agent must be seen alive before it
	// may stand for a waiting note's command: one tick at the normal pace.
	childLinger = time.Second

	refusalLimit = 3
)

// seenNote is a note the watcher has read: matched once it was tracked,
// childSeen as markChildSeen sets it, and posted from the first tick it was
// read marked posted. One that goes away unmatched with childSeen is logged.
type seenNote struct {
	command   string
	matched   bool
	childSeen bool
	posted    time.Time
}

type watcher struct {
	f         folders
	release   func()
	lockInfo  os.FileInfo
	tracked   map[string]command
	seen      map[string]seenNote
	stopped   []proc    // the last stopped command's processes
	quietFrom time.Time // when they were all gone
	// passed are commands never chosen again: a dry-run one already logged,
	// one still running after a stop gave up on it, or one whose stops kept
	// signalling nothing.
	passed map[string]bool
	// refusals counts a call's consecutive stops that signalled nothing while
	// its command still ran.
	refusals map[string]int
	// ended are calls whose tracked command has ended. A call runs once, so
	// its note, which stays until the post hook or the agent's exit (forever
	// for a denied call), is never matched again.
	ended     map[string]bool
	idleSince time.Time
	expired   time.Time
	waiter    *waiter
	// unsized are processes whose footprint could not be read, so each is
	// logged once, not every tick.
	unsized map[procKey]bool
	// agentChildren are the agents' live children at the last tick, each with
	// the first tick it was seen at since.
	agentChildren map[procKey]time.Time
	// underFloor is set once a pressure episode's largest command was logged
	// as too small to stop, and cleared when memory recovers.
	underFloor bool
}

// procKey names one process: its PID and start time.
type procKey struct {
	pid   int
	start uint64
}

// Watch is the watcher, at most one per user, started detached by the pre
// hook. It finds each noted command, watches machine memory, and near
// out-of-memory stops the largest agent command and records why. It returns
// after ten minutes with no note and no tracked command, when its lock file
// is no longer the one it locked, or when ctx ends. It has no terminal and
// logs only to the guard log. It works from /, so it never holds the folder
// of the session that started it.
func Watch(ctx context.Context) {
	debug.SetMemoryLimit(32 << 20)
	f, err := openFolders()
	if err != nil {
		return
	}
	w := &watcher{
		f:         f,
		release:   func() {},
		tracked:   map[string]command{},
		seen:      map[string]seenNote{},
		passed:    map[string]bool{},
		ended:     map[string]bool{},
		refusals:  map[string]int{},
		unsized:   map[procKey]bool{},
		idleSince: time.Now(),
		waiter:    newWaiter(),
	}
	defer w.waiter.close()
	if !w.lock() {
		return
	}
	defer func() { w.release() }()
	f.logf("watch: started")
	for ctx.Err() == nil {
		if !w.ownsLock() {
			f.logf("watch: the lock file changed; exiting")
			return
		}
		next := w.tick()
		if time.Since(w.idleSince) >= idleExit && !w.carryOn() {
			return
		}
		w.waiter.wait(next)
	}
}

func (w *watcher) lock() bool {
	release, held, err := operation.TryLock(w.f.lock())
	if err != nil || held {
		return false
	}
	info, err := os.Stat(w.f.lock())
	if err != nil {
		release()
		return false
	}
	w.release, w.lockInfo = release, info
	return true
}

// ownsLock reports whether the lock file is still the one this watcher
// locked (same device and inode).
func (w *watcher) ownsLock() bool {
	info, err := os.Stat(w.f.lock())
	return err == nil && os.SameFile(info, w.lockInfo)
}

// carryOn releases the lock at idle exit, then reads the notes once more. A
// live note written meanwhile, or a process list it cannot read, makes it take
// the lock again and carry on, unless another watcher took it first; so a
// note written as a watcher stops is never left without one. Only notes that
// parse and whose agent runs count, so a broken note cannot keep the watcher
// alive. It logs why it does not carry on.
func (w *watcher) carryOn() bool {
	w.release()
	w.release = func() {}
	if s, err := snapshotNow(); err == nil && len(w.notes(s)) == 0 {
		w.f.logf("watch: idle for %s; exiting", idleExit)
		return false
	}
	if !w.lock() {
		w.f.logf("watch: idle, and the lock is held by another watcher or cannot be taken; exiting")
		return false
	}
	w.idleSince = time.Now()
	return true
}

// tick runs one round and returns the time to the next.
func (w *watcher) tick() time.Duration {
	s, err := snapshotNow()
	if err != nil {
		w.f.logf("watch: cannot list processes: %v", err)
		return time.Second
	}
	notes := w.notes(s)
	w.forget(s)
	agents, protected := w.agents(s, notes)
	for _, agent := range agents {
		w.waiter.watch(agent)
		loadArgs(s, agent)
	}
	taken := map[int]bool{}
	for _, c := range w.tracked {
		taken[c.Root.PID] = true
	}
	for _, c := range match(s, w.untracked(notes), taken) {
		taken[c.Root.PID] = true
		w.tracked[c.Note.ToolUseID] = c
		seen := w.seen[c.Note.ToolUseID]
		seen.matched = true
		w.seen[c.Note.ToolUseID] = seen
		w.f.logf("watch: tracking %s (pid %d): %s",
			c.Note.ToolUseID, c.Root.PID, clip(c.Note.Command))
	}
	w.markChildSeen(s, agents, notes, taken)
	if len(notes) > 0 || len(w.tracked) > 0 {
		w.idleSince = time.Now()
	}
	w.expireKills()
	m, err := readMemory()
	if err != nil {
		w.f.logf("watch: cannot read memory: %v", err)
		return time.Second
	}
	act := decide(m)
	if act == nothing {
		w.underFloor = false
	}
	if !w.coolingDown(s) && act != nothing && len(w.tracked) > 0 {
		w.stopLargest(s, protected, act, m)
	}
	return delay(m)
}

// notes reads the notes on disk. A note that does not parse is deleted: the
// pre hook renames a complete file into place, so a broken one never heals.
// So is a note whose agent (PID and start time) is gone. A posted note, a
// background call's that its post hook kept, is deleted once its command has
// ended or, when no command matched it, childLinger after it was first read
// posted: its command started before the post hook, so a later process is
// never its command. A note that goes away without ever being matched is
// logged.
func (w *watcher) notes(s snapshot) []note {
	present := map[string]bool{}
	var notes []note
	now := time.Now()
	for _, id := range w.f.noteIDs() {
		var n note
		path := w.f.note(id)
		if readJSON(path, &n) != nil || n.ToolUseID != id || !s.alive(n.AgentPID, n.AgentStart) {
			_ = os.Remove(path)
			continue
		}
		seen, ok := w.seen[id]
		if !ok {
			seen = seenNote{command: n.Command}
		}
		if n.Posted && seen.posted.IsZero() {
			seen.posted = now
		}
		w.seen[id] = seen
		if n.Posted && (w.ended[id] || !seen.matched && now.Sub(seen.posted) >= childLinger) {
			_ = os.Remove(path)
			continue
		}
		present[id] = true
		notes = append(notes, n)
	}
	for id, seen := range w.seen {
		if !present[id] {
			if !seen.matched && seen.childSeen {
				w.f.logf("watch: note %s ended unmatched: %s", id, clip(seen.command))
			}
			delete(w.seen, id)
			delete(w.ended, id)
		}
	}
	return notes
}

// markChildSeen sets childSeen on each waiting note whose agent has a child
// that started after the note, is not in taken (a tracked command's root) and
// has been seen alive, with the same start time, at every tick for at least
// childLinger: its command may have run in a shape match does not know. The
// span is in time, not ticks, because ticks quicken to 100 ms under memory
// pressure. The pre hook's own shell started before NotedStart and never
// counts.
func (w *watcher) markChildSeen(s snapshot, agents []int, notes []note, taken map[int]bool) {
	before, now := w.agentChildren, time.Now()
	w.agentChildren = map[procKey]time.Time{}
	for _, agent := range agents {
		for _, p := range s.children(agent) {
			if key := (procKey{p.PID, p.Start}); !p.Zombie {
				w.agentChildren[key] = cmp.Or(before[key], now)
			}
		}
	}
	for _, n := range w.untracked(notes) {
		seen := w.seen[n.ToolUseID]
		if !seen.childSeen && slices.ContainsFunc(s.children(n.AgentPID), func(p proc) bool {
			first, ok := w.agentChildren[procKey{p.PID, p.Start}]
			return ok && p.Start > n.NotedStart && now.Sub(first) >= childLinger && !taken[p.PID]
		}) {
			seen.childSeen = true
			w.seen[n.ToolUseID] = seen
		}
	}
}

// forget stops tracking commands that ended or whose agent is gone. A call
// whose command ended is remembered until its note goes, so the note cannot
// claim a later command with the same text.
func (w *watcher) forget(s snapshot) {
	for key := range w.unsized {
		if !s.alive(key.pid, key.start) {
			delete(w.unsized, key)
		}
	}
	for id, c := range w.tracked {
		agentAlive := s.alive(c.Note.AgentPID, c.Note.AgentStart)
		if !s.alive(c.Root.PID, c.Root.Start) || !agentAlive {
			delete(w.tracked, id)
			delete(w.passed, id)
			delete(w.refusals, id)
			// Only a call whose note is still on disk is remembered: when the
			// post hook already removed it, nothing can match it again and
			// notes would never clear the entry.
			if _, noted := w.seen[id]; noted && agentAlive {
				w.ended[id] = true
			}
		}
	}
}

// agents are the live agents of the notes and tracked commands. protected is
// them and the watcher: never signalled, nor anything under them.
func (w *watcher) agents(s snapshot, notes []note) ([]int, map[int]bool) {
	protected := map[int]bool{os.Getpid(): true}
	var agents []int
	add := func(pid int, start uint64) {
		if !protected[pid] && s.alive(pid, start) {
			protected[pid] = true
			agents = append(agents, pid)
		}
	}
	for _, n := range notes {
		add(n.AgentPID, n.AgentStart)
	}
	for _, c := range w.tracked {
		add(c.Note.AgentPID, c.Note.AgentStart)
	}
	return agents, protected
}

// untracked are the notes still waiting for their command: not tracked, and
// not a call whose command already ran.
func (w *watcher) untracked(notes []note) []note {
	var out []note
	for _, n := range notes {
		if _, ok := w.tracked[n.ToolUseID]; !ok && !w.ended[n.ToolUseID] {
			out = append(out, n)
		}
	}
	return out
}

// expireKills deletes kill records over a day old, at most once a minute.
func (w *watcher) expireKills() {
	if time.Since(w.expired) < time.Minute {
		return
	}
	w.expired = time.Now()
	entries, _ := os.ReadDir(w.f.killed)
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > killKeep {
			_ = os.Remove(filepath.Join(w.f.killed, entry.Name()))
		}
	}
}

// coolingDown is true from a stop until the stopped command's processes are
// gone and ten more seconds have passed, so the pressure figures catch up.
func (w *watcher) coolingDown(s snapshot) bool {
	if w.stopped == nil {
		return false
	}
	for _, p := range w.stopped {
		if s.alive(p.PID, p.Start) {
			w.quietFrom = time.Time{}
			return true
		}
	}
	if w.quietFrom.IsZero() {
		w.quietFrom = time.Now()
	}
	if time.Since(w.quietFrom) < cooldown {
		return true
	}
	w.stopped = nil
	return false
}

// stopLargest stops the tracked command with the largest footprint over its
// tree (without protected processes); a tie goes to the older command. A
// passed call is left out: a dry-run one already logged, one a stop gave up
// on, or one whose stops signalled nothing three times in a row. A polite stop
// leaves a command under the memory floor running, and logs that once a
// pressure episode.
func (w *watcher) stopLargest(s snapshot, protected map[int]bool, act action, m memory) {
	skip := outside(protected)
	var chosen command
	var chosenSize uint64
	var members []proc
	for id, c := range w.tracked {
		if w.passed[id] {
			continue
		}
		tree := s.tree(c.Root.PID, skip)
		var size uint64
		for _, p := range tree {
			if p.Zombie {
				continue
			}
			bytes, err := footprint(p.PID)
			if key := (procKey{p.PID, p.Start}); err != nil && !w.unsized[key] {
				w.unsized[key] = true
				w.f.logf("watch: size of pid %d: %v; counted as 0", p.PID, err)
			}
			size += bytes
		}
		if members == nil || size > chosenSize ||
			size == chosenSize && c.Root.Start < chosen.Root.Start {
			chosen, chosenSize, members = c, size, tree
		}
	}
	switch {
	case len(members) == 0:
	case act == polite && chosenSize < m.floor():
		if !w.underFloor {
			w.underFloor = true
			w.f.logf("watch: memory is low, but the largest agent command, %s, holds %s, "+
				"under the %s floor; not stopping it", chosen.Note.ToolUseID,
				gigabytes(chosenSize), gigabytes(m.floor()))
		}
	default:
		w.stop(chosen, chosenSize, len(members)-1)
	}
}

// stop re-checks memory, snapshots the command again, records the reason and
// signals it. It reads the notes again first, so an agent that left its first
// note since the tick is protected. The record is written before any signal,
// because the agent's post hook may run the moment the command dies; it is
// deleted when nothing was signalled. With WORKBENCH_GUARD=dry-run on the
// note it only logs.
func (w *watcher) stop(c command, size uint64, children int) {
	m, err := readMemory()
	if err != nil {
		w.f.logf("watch: cannot read memory; not stopping %s: %v", c.Note.ToolUseID, err)
		return
	}
	if decide(m) == nothing {
		w.f.logf("watch: memory recovered before stopping %s", c.Note.ToolUseID)
		return
	}
	s, err := snapshotNow()
	if err != nil {
		return
	}
	_, protected := w.agents(s, w.notes(s))
	chosen := targets(s, c.Root, protected)
	if len(chosen) == 0 {
		// Nothing to signal: the root is gone or changed (the next tick stops
		// tracking it) or is protected. Counted like a refused stop, so a
		// command that can never be stopped is not chosen every tick.
		w.refused(c)
		return
	}
	text := reason(c.Note.Command, children, size, m)
	if c.Note.DryRun {
		w.passed[c.Note.ToolUseID] = true
		w.f.logf("watch: dry run, would stop %s: %s", c.Note.ToolUseID, text)
		return
	}
	w.stopped = s.tree(c.Root.PID, outside(protected))
	w.quietFrom = time.Time{}
	record := w.f.record(c.Note.ToolUseID)
	if err = writeJSON(
		record,
		kill{ToolUseID: c.Note.ToolUseID, Reason: text, At: time.Now()},
	); err != nil {
		// The machine comes first: the stop goes ahead and the agent gets no reason.
		w.f.logf("watch: cannot record the stop of %s: %v; stopping anyway, "+
			"the agent will be told nothing", c.Note.ToolUseID, err)
	}
	w.f.logf("watch: stopping %s: %s", c.Note.ToolUseID, text)
	if w.signal(chosen, c.Note.ToolUseID) {
		delete(w.refusals, c.Note.ToolUseID)
		return
	}
	_ = os.Remove(record)
	// Nothing was signalled, so there is nothing to wait for: no cooldown.
	w.stopped = nil
	w.refused(c)
}

// refused handles a stop that signalled nothing. A command that is gone, or
// that cannot be checked, is no refusal: a transient failure (out of memory
// or file descriptors) must not pass over the largest command for good. One
// still running with its start time refused the signal (a root-owned process,
// say); after three such stops in a row the call is passed over, so it is not
// retried every tick.
func (w *watcher) refused(c command) {
	id := c.Note.ToolUseID
	s, err := snapshotNow()
	if err != nil || !s.alive(c.Root.PID, c.Root.Start) {
		w.f.logf("watch: %s ended or could not be checked", id)
		return
	}
	w.refusals[id]++
	if w.refusals[id] < refusalLimit {
		w.f.logf("watch: %s is running but nothing could be signalled (%d of %d)",
			id, w.refusals[id], refusalLimit)
		return
	}
	w.passed[id] = true
	w.f.logf("watch: %s refused %d stops in a row; not chosen again", id, refusalLimit)
}

// signal sends SIGTERM, then SIGKILL once memory reaches the forced-stop line
// or after five seconds, polling every 100 ms until the command's processes
// are gone, and gives up after ten seconds. It reports whether any live
// target was signalled.
func (w *watcher) signal(chosen []target, id string) bool {
	sent := false
	for _, t := range chosen {
		sent = send(t, unix.SIGTERM) || sent
	}
	if !sent {
		return false
	}
	killed := false
	for start := time.Now(); time.Since(start) < giveUp; time.Sleep(100 * time.Millisecond) {
		if s, err := snapshotNow(); err == nil && !w.anyAlive(s) {
			return true
		}
		if !killed && escalateNow(start) {
			w.escalate()
			killed = true
		}
	}
	w.abandon(id)
	return true
}

// escalateNow is true after five seconds or once memory reaches the
// forced-stop line; memory is read only until then.
func escalateNow(start time.Time) bool {
	if time.Since(start) >= killAfter {
		return true
	}
	m, err := readMemory()
	return err == nil && decide(m) == forced
}

// escalate sends SIGKILL, by PID, once to every stopped process still alive
// and to anything started since under one still running, leaving out the
// agents of the notes on disk now and everything under them. It does not
// signal the group: a shell that died of SIGTERM was the group leader, and
// once it is reaped the group is no longer known to be the command's, while a
// child that ignored SIGTERM lives on under PID 1. A process started under one
// that has since exited is out of reach (spec 12).
func (w *watcher) escalate() {
	s, err := snapshotNow()
	if err != nil {
		return
	}
	_, protected := w.agents(s, w.notes(s))
	w.dropProtected(protected)
	known := map[int]bool{}
	for _, p := range w.stopped {
		known[p.PID] = true
	}
	// The stopped processes' trees overlap, so a process already collected is
	// left out with everything under it.
	collected := map[int]bool{}
	excluded := outside(protected)
	skip := func(q proc) bool { return excluded(q) || collected[q.PID] }
	var live []proc
	for _, p := range w.stopped {
		if !s.alive(p.PID, p.Start) {
			continue
		}
		for _, q := range s.tree(p.PID, skip) {
			collected[q.PID] = true
			live = append(live, q)
		}
	}
	for _, p := range live {
		if !p.Zombie {
			send(target{PID: p.PID, Start: p.Start}, unix.SIGKILL)
		}
		if !known[p.PID] {
			known[p.PID] = true
			w.stopped = append(w.stopped, p)
		}
	}
}

// abandon gives up on a stopped command that is still running after ten
// seconds, such as one in uninterruptible I/O: it logs what survived, ends
// the cooldown and passes the call over from now on, so one process that
// cannot be stopped never keeps the watcher from acting again.
func (w *watcher) abandon(id string) {
	var left []int
	if s, err := snapshotNow(); err == nil {
		_, protected := w.agents(s, w.notes(s))
		w.dropProtected(protected)
		for _, p := range w.stopped {
			if s.alive(p.PID, p.Start) {
				left = append(left, p.PID)
			}
		}
	}
	w.f.logf("watch: gave up on %s after %s; still running: %v; not chosen again", id, giveUp, left)
	w.stopped = nil
	w.passed[id] = true
}

// loadArgs reads the command lines matching looks at: the agent's, its direct
// children's and, below a sandbox launcher, what the launcher started. Sizes
// need no command lines, so the rest of the tree is not read.
func loadArgs(s snapshot, agent int) {
	seen := map[int]bool{}
	var read func(pid int, expand bool)
	read = func(pid int, expand bool) {
		p, ok := s[pid]
		if !ok || seen[pid] {
			return
		}
		seen[pid] = true
		p.Args = readArgs(pid)
		s[pid] = p
		if expand || len(p.Args) > 0 && launchers[filepath.Base(p.Args[0])] {
			for _, child := range s.children(pid) {
				read(child.PID, false)
			}
		}
	}
	read(agent, true)
}

// alive reports whether the process pid with this start time still runs; a
// zombie holds no memory and counts as gone.
func (s snapshot) alive(pid int, start uint64) bool {
	p, ok := s[pid]
	return ok && p.Start == start && !p.Zombie
}

// anyAlive reports whether a stopped process still runs, leaving out the
// agents of the notes on disk now and everything under them.
func (w *watcher) anyAlive(s snapshot) bool {
	_, protected := w.agents(s, w.notes(s))
	w.dropProtected(protected)
	for _, p := range w.stopped {
		if s.alive(p.PID, p.Start) {
			return true
		}
	}
	return false
}

// dropProtected removes from the stopped processes each one that is protected
// now and each one whose parents within them lead to one that is: an agent
// may leave its first note after its command's processes were taken. The
// cooldown then no longer waits on that agent either.
func (w *watcher) dropProtected(protected map[int]bool) {
	byPID := make(map[int]proc, len(w.stopped))
	for _, p := range w.stopped {
		byPID[p.PID] = p
	}
	underProtected := func(p proc) bool {
		for seen := map[int]bool{}; !seen[p.PID]; {
			if protected[p.PID] {
				return true
			}
			seen[p.PID] = true
			parent, ok := byPID[p.PPID]
			if !ok {
				return false
			}
			p = parent
		}
		return false
	}
	w.stopped = slices.DeleteFunc(w.stopped, underProtected)
}
