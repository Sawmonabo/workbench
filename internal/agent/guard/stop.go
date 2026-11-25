package guard

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
	"golang.org/x/sys/unix"
)

// The stops' timing and limits: the cooldown after a stop (see coolingDown),
// the SIGKILL that follows SIGTERM (see escalateNow), the end of the wait for
// a stopped command's processes to go (see signal), the age at which a kill
// record goes in any case (see expireKills), and the stops in a row that
// signalled nothing after which a call is passed over (see refused).
const (
	cooldown     = 10 * time.Second
	killAfter    = 5 * time.Second
	giveUp       = 10 * time.Second
	killKeep     = 24 * time.Hour
	refusalLimit = 3
)

// expireKills deletes, at most once a minute, each kill record whose call's
// note is no longer on disk, which no post hook will read: the post hook
// takes a record before it marks the note (see Post), and a note goes only
// once it is marked ended or posted, its agent is gone or it was abandoned,
// so a record left after it is one a post hook could not take, having
// looked for it at another path, or one written as its command ended. It
// also deletes records over a day old, and stale temporary files (see
// listFolder).
func (w *watcher) expireKills() {
	if time.Since(w.expired) < time.Minute {
		return
	}
	w.expired = time.Now()
	entries, _ := listFolder(w.f.killed)
	for _, entry := range entries {
		path := filepath.Join(w.f.killed, entry.Name())
		id, isRecord := strings.CutSuffix(entry.Name(), ".json")
		if isRecord && idPattern.MatchString(id) {
			if _, err := os.Lstat(w.f.note(id)); errors.Is(err, fs.ErrNotExist) {
				_ = os.Remove(path)
				continue
			}
		}
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > killKeep {
			_ = os.Remove(path)
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

// stopLargest stops the tracked command or unit the guard may choose (see
// choose) with the largest footprint over its tree (without protected
// processes); a tie goes to the older root. A passed call is left out: a
// dry-run one already logged, one a stop gave up on, or one whose stops
// signalled nothing three times in a row. So is one that no longer runs in
// this snapshot, one a person is stopping, a unit whose call did not resolve
// and a unit none of whose roots the guard's own stop may take (see
// rootHeld); a unit is sized and stopped over only those roots (see ownStop). A
// polite stop leaves the commands running when together they hold less than
// the memory floor, and logs that once a pressure episode.
func (w *watcher) stopLargest(
	s snapshot,
	below parents,
	protected map[int]uint64,
	act action,
	m memory,
) {
	skip := outside(protected)
	var cands []candidate
	trees := map[string][]proc{}
	for id, c := range w.tracked {
		if w.passed[id] || w.manual[id] != nil || !live(s, c) {
			continue
		}
		// A unit only with the roots its own stop may take (see ownStop).
		own, tracked := ownStop(
			c,
			time.Now(),
			func(r detachedRoot) bool { return s.alive(r.PID, r.Start) },
		)
		cand := candidate{c: own, tracked: tracked}
		// One the guard may not choose is not sized: choose leaves it out.
		if !cand.mayChoose() {
			continue
		}
		trees[id] = s.commandTreeIn(below, own, skip)
		cand.size = w.size(trees[id], platform.Footprint)
		cands = append(cands, cand)
	}
	chosen, total, ok := choose(cands)
	members := trees[chosen.c.Note.ToolUseID]
	switch {
	case !ok || len(members) == 0:
	case act == polite && total < m.floor():
		if !w.underFloor {
			w.underFloor = true
			w.f.event(slog.LevelWarn, EventMemory,
				"memory is low, but the agent commands are too small to stop",
				"size", total, "floor", m.floor(), "memory", m)
		}
	default:
		w.stop(chosen.c, chosen.size, len(members)-1)
	}
}

// stop re-checks memory, snapshots the command again, records the reason and
// signals it. It reads the notes and the agents folder again first (see
// agents), so an agent that left its first note or registered its session
// since the tick is protected. The record is written before any signal,
// because the agent's post hook may run the moment the command dies; a
// unit's is written for the call that started it, when that call is told
// (see killRecordFor). A record it wrote is deleted when nothing was
// signalled. With WORKBENCH_GUARD=dry-run on the note it only logs.
func (w *watcher) stop(c command, size uint64, children int) {
	m, err := readMemory()
	if err != nil {
		w.f.event(slog.LevelError, EventError, "cannot read memory; not stopping",
			append(callAttrs(c), "error", err)...)
		return
	}
	if act, _ := decide(m); act == nothing {
		w.f.event(slog.LevelInfo, EventMemory, "memory recovered before the stop",
			append(callAttrs(c), "memory", m)...)
		return
	}
	s, err := w.snapshot()
	if err != nil {
		return
	}
	notes, err := w.notes(s)
	if err != nil {
		w.f.event(slog.LevelError, EventError, "cannot read the notes; not stopping",
			append(callAttrs(c), "error", err)...)
		return
	}
	_, protected := w.agents(s, notes)
	chosen := targets(s, c, protected)
	if len(chosen) == 0 {
		// Nothing to signal: the root is gone or changed (the next tick stops
		// tracking it) or is protected. Counted like a refused stop, so a
		// command that can never be stopped is not chosen every tick.
		w.refused(c)
		return
	}
	text := reason(c.Note.Command, c.Detached != nil, children, size, m)
	stopping := append(w.endAttrs(c), "by", "guard",
		"size", size, "children", children, "reason", text, "memory", m)
	if c.Note.DryRun {
		w.passed[c.Note.ToolUseID] = true
		w.f.event(slog.LevelWarn, EventStopping, "would stop (dry run)",
			append(stopping, "dry_run", true)...)
		return
	}
	w.stopped = s.commandTree(c, outside(protected))
	w.quietFrom = time.Time{}
	// A unit's reason goes to the call that started it, only while that
	// call's command runs in the foreground (see killRecordFor).
	record := w.writeKill(c, notes, text)
	w.f.event(slog.LevelWarn, EventStopping, "stopping", stopping...)
	if w.signal(chosen, c) {
		delete(w.refusals, c.Note.ToolUseID)
		return
	}
	if record != "" {
		_ = os.Remove(record)
	}
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
	s, err := w.snapshot()
	if err != nil || !live(s, c) {
		w.f.event(slog.LevelInfo, EventRefused,
			"nothing signalled: the command ended or could not be checked", callAttrs(c)...)
		return
	}
	w.refusals[id]++
	if w.refusals[id] < refusalLimit {
		text := fmt.Sprintf("running, but nothing could be signalled (%d of %d)",
			w.refusals[id], refusalLimit)
		w.f.event(slog.LevelWarn, EventRefused, text, callAttrs(c)...)
		return
	}
	w.passed[id] = true
	w.f.event(slog.LevelWarn, EventRefused,
		fmt.Sprintf("refused %d stops in a row; not chosen again", refusalLimit), callAttrs(c)...)
}

// signal sends SIGTERM, then SIGKILL once memory calls for a forced stop or
// after five seconds, polling every 100 ms until the command's processes are
// gone, and gives up after ten seconds; meanwhile it shows the command
// stopping to the clients each second. It reports whether any live target
// was signalled.
func (w *watcher) signal(chosen []target, c command) bool {
	sent := false
	for _, t := range chosen {
		p := platform.Process{PID: t.PID, Start: t.Start}
		sent = platform.Signal(p, t.Group, unix.SIGTERM) || sent
	}
	if !sent {
		return false
	}
	w.stoppedBy[c.Note.ToolUseID] = "guard"
	w.showStopping(c.Note.ToolUseID)
	killed := false
	shown := time.Now()
	for start := time.Now(); time.Since(start) < giveUp; time.Sleep(100 * time.Millisecond) {
		if time.Since(shown) >= time.Second {
			shown = time.Now()
			w.showStopping(c.Note.ToolUseID)
		}
		if s, err := w.snapshot(); err == nil && !w.anyAlive(s) {
			return true
		}
		if !killed && w.escalateNow(start) {
			if killed = w.escalate(); killed {
				w.f.event(slog.LevelWarn, EventForced, "sent SIGKILL", callAttrs(c)...)
			}
		}
	}
	w.abandon(c)
	return true
}

// showStopping sends the clients the last view with the command id shown as
// stopping by the guard, since no tick runs while signal polls. The view's
// commands are copied, never changed in place: a client may still be
// writing the last one.
func (w *watcher) showStopping(id string) {
	if w.server == nil || w.view.At.IsZero() {
		return
	}
	view := w.view
	view.At = time.Now()
	view.Commands = slices.Clone(view.Commands)
	for i := range view.Commands {
		if view.Commands[i].ID == id && view.Commands[i].Ended.IsZero() {
			view.Commands[i].State, view.Commands[i].By = stateStopping, "guard"
		}
	}
	w.view = view
	w.server.broadcast(view)
}

// escalateNow is true after five seconds or once memory calls for a forced
// stop; memory is read only until then.
func (w *watcher) escalateNow(start time.Time) bool {
	if time.Since(start) >= killAfter {
		return true
	}
	m, err := readMemory()
	return err == nil && w.demand(m, time.Now()) == forced
}

// escalate sends SIGKILL, by PID, once to each process escalation chooses. It
// does not signal the group: a shell that died of SIGTERM was the group
// leader, and once it is reaped the group is no longer known to be the
// command's, while a child that ignored SIGTERM lives on under PID 1. It
// reports false when the processes or notes could not be read, so the next
// poll tries again.
func (w *watcher) escalate() bool {
	s, err := w.snapshot()
	if err != nil {
		return false
	}
	notes, err := w.notes(s)
	if err != nil {
		return false
	}
	_, protected := w.agents(s, notes)
	kill, wait := escalation(s, w.stopped, protected)
	w.stopped = wait
	for _, p := range kill {
		if !p.Zombie {
			platform.Signal(p.Process, false, unix.SIGKILL)
		}
	}
	return true
}

// escalation chooses, from one snapshot, what escalate kills: every stopped
// process still alive and anything started since under one, leaving out
// protected processes (the agents of the notes on disk now and the other
// agents the guard knows, the watcher) with
// everything under them, and each stopped process that is protected now or
// sits under one that is (see dropProtected). wait is the stopped processes to
// wait for: those kept, and the new ones. A process started under one that has
// since exited is out of reach: nothing links it to the command any more.
func escalation(s snapshot, stopped []proc, protected map[int]uint64) (kill, wait []proc) {
	wait = dropProtected(slices.Clone(stopped), protected)
	known := map[int]bool{}
	for _, p := range wait {
		known[p.PID] = true
	}
	// The stopped processes' trees overlap, so a process already collected is
	// left out with everything under it.
	collected := map[int]bool{}
	excluded := outside(protected)
	skip := func(q proc) bool { return excluded(q) || collected[q.PID] }
	for _, p := range wait {
		if !s.alive(p.PID, p.Start) {
			continue
		}
		for _, q := range s.tree(p.PID, skip) {
			collected[q.PID] = true
			kill = append(kill, q)
		}
	}
	for _, p := range kill {
		if !known[p.PID] {
			known[p.PID] = true
			wait = append(wait, p)
		}
	}
	return kill, wait
}

// abandon gives up on a stopped command that is still running after ten
// seconds, such as one in uninterruptible I/O: it logs what survived, ends
// the cooldown and passes the call over from now on, so one process that
// cannot be stopped never keeps the watcher from acting again.
func (w *watcher) abandon(c command) {
	var left []int
	if s, err := w.snapshot(); err == nil {
		if notes, err := w.notes(s); err == nil {
			_, protected := w.agents(s, notes)
			for _, p := range dropProtected(w.stopped, protected) {
				if s.alive(p.PID, p.Start) {
					left = append(left, p.PID)
				}
			}
		}
	}
	text := fmt.Sprintf("still running %.0f seconds after the stop; not chosen again",
		giveUp.Seconds())
	w.f.event(slog.LevelWarn, EventGaveUp, text, append(callAttrs(c), "still_running", left)...)
	w.stopped = nil
	w.passed[c.Note.ToolUseID] = true
}

// anyAlive reports whether a stopped process still runs, leaving out the
// agents of the notes on disk now, the other agents the guard knows and
// everything under them. Notes it cannot
// read count as a process still running.
func (w *watcher) anyAlive(s snapshot) bool {
	notes, err := w.notes(s)
	if err != nil {
		return true
	}
	_, protected := w.agents(s, notes)
	w.stopped = dropProtected(w.stopped, protected)
	for _, p := range w.stopped {
		if s.alive(p.PID, p.Start) {
			return true
		}
	}
	return false
}

// dropProtected removes, in place, from the stopped processes each one that
// is protected now and each one whose parents within them lead to one that
// is: an agent may leave its first note after its command's processes were
// taken. The cooldown then no longer waits on that agent either.
func dropProtected(stopped []proc, protected map[int]uint64) []proc {
	excluded := outside(protected)
	byPID := make(map[int]proc, len(stopped))
	for _, p := range stopped {
		byPID[p.PID] = p
	}
	underProtected := func(p proc) bool {
		for seen := map[int]bool{}; !seen[p.PID]; {
			if excluded(p) {
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
	return slices.DeleteFunc(stopped, underProtected)
}
