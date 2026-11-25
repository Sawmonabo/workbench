package guard

import (
	"cmp"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"time"
)

// seenNote is a note the watcher has read: its command, agent with its kind
// and NotedStart, read when this watcher first read it, matched once it was
// tracked, childSeen as markChildSeen sets it, posted and postedRound from
// the first tick it was read marked posted, and ended, when its post hook
// marked it ended (its EndedAt). One that goes away unmatched with childSeen,
// and whose shell was not learned either (see found), is logged, with the
// note's cwd, project, subagent and at, its NotedAt; one that goes away
// ended without being tracked goes into the history as it is (see wentAway).
type seenNote struct {
	command     string
	agent       procKey
	kind        kind
	noted       uint64
	cwd         string
	project     string
	subagent    string
	dryRun      bool
	at          time.Time
	read        time.Time
	matched     bool
	childSeen   bool
	posted      time.Time
	postedRound int
	ended       time.Time
}

// notes reads the notes on disk. A note that cannot be read (for want of
// memory or file descriptors, say) is left for a later tick, its state kept. A
// note that does not parse is deleted: the pre hook renames a complete file
// into place, so a broken one never heals. So is a note whose agent (PID and
// start time) is gone (see gone). Every other note read makes its agent known
// and keeps its call's record. An ended note, one whose post hook ran, is then
// deleted and taken as a note that went away: it is not given, so it is never
// matched, nor abandoned. A posted note, a background or Monitor call's that
// its post hook kept, is deleted once its command has ended or, when no command
// matched it, childLinger after it was first read posted and once match has
// looked for it in two rounds: its command started before the post hook, so a
// later process is never its command. A note that
// was never posted and whose command never matched or has ended is
// abandoned, and deleted, once a note
// its agent wrote later has gone and abandonAfter has passed since it was
// first read: a call that was denied or cut off gets no post hook, and its
// note would otherwise keep the watcher running for as long as its agent
// does. A note that goes away without ever being matched is logged.
func (w *watcher) notes(s snapshot) ([]note, error) {
	ids, err := w.f.noteIDs()
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	var notes []note
	now := time.Now()
	for _, id := range ids {
		path := w.f.note(id)
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			present[id] = true
			if !w.unreadNotes[id] {
				w.unreadNotes[id] = true
				w.f.event(slog.LevelError, EventError, "cannot read a note",
					"call", id, "error", err)
			}
			continue
		}
		delete(w.unreadNotes, id)
		var n note
		if json.Unmarshal(data, &n) != nil || n.ToolUseID != id ||
			w.gone(s, n.AgentPID, n.AgentStart) {
			_ = os.Remove(path)
			continue
		}
		w.known[procKey{n.AgentPID, n.AgentStart}] = n.Kind
		w.records.add(n)
		seen, ok := w.seen[id]
		if !ok {
			seen = seenNote{
				command:  n.Command,
				agent:    procKey{n.AgentPID, n.AgentStart},
				kind:     n.Kind,
				noted:    n.NotedStart,
				cwd:      n.Cwd,
				project:  n.Project,
				subagent: n.Subagent,
				dryRun:   n.DryRun,
				at:       n.NotedAt,
				read:     now,
			}
		}
		if n.Posted && seen.posted.IsZero() {
			seen.posted, seen.postedRound = now, w.round
		}
		if n.Ended && w.letGo {
			// The lock is let go (see carryOn): the note stays for the
			// watcher that takes it, which puts the call into the history,
			// and counts as one to carry on for.
			present[id] = true
			notes = append(notes, n)
			continue
		}
		if n.Ended {
			// Seen first, so a call that ended before any tick tracked it
			// goes into the history as it goes away (see wentAway).
			seen.ended = cmp.Or(n.EndedAt, now)
			w.seen[id] = seen
			_ = os.Remove(path)
			continue
		}
		w.seen[id] = seen
		if n.Posted && (w.ended[id] || !seen.matched && now.Sub(seen.posted) >= childLinger &&
			w.round > seen.postedRound+1) {
			_ = os.Remove(path)
			continue
		}
		if !n.Posted && (!seen.matched || w.ended[id]) && n.NotedStart < w.lastEnded[seen.agent] &&
			now.Sub(seen.read) >= abandonAfter {
			w.f.event(slog.LevelWarn, EventAbandoned,
				"abandoned without a post hook after a later call of its agent ended",
				"call", id, "agent", n.Kind, "command", clip(n.Command))
			_ = os.Remove(path)
			delete(w.seen, id)
			delete(w.ended, id)
			continue
		}
		present[id] = true
		notes = append(notes, n)
	}
	w.wentAway(s, present)
	return notes, nil
}

// wentAway forgets the notes seen that are not present now, deleted by notes
// or gone from disk, and what notes keeps of notes that cannot be read or
// agents that are gone. A call no tick of this watcher tracked (see
// untrackedEnd) goes into the history as it goes away: unmatched when its
// command was never found while its agent had run a command (see found),
// which is logged; else done, when its post hook marked it ended, with no
// memory, since none was measured. A tracked call goes into the history when
// its command ends (see forget).
func (w *watcher) wentAway(s snapshot, present map[string]bool) {
	for id, seen := range w.seen {
		if !present[id] {
			w.lastEnded[seen.agent] = max(w.lastEnded[seen.agent], seen.noted)
			if state := w.untrackedEnd(id, seen); state != "" {
				if state == stateUnmatched {
					attrs := append([]any{"call", id, "agent", seen.kind},
						placeAttrs(seen.cwd, seen.project, seen.subagent)...)
					// A note that never found its command left that command
					// unguarded, which a person should see.
					w.f.event(slog.LevelWarn, EventUnmatched, "ended without matching a command",
						append(attrs, "took", time.Since(seen.at).Seconds(),
							"command", clip(seen.command))...)
				}
				w.finishAt(CommandView{
					ID: id, Agent: string(seen.kind), Subagent: seen.subagent,
					Project: seen.project, Cwd: seen.cwd, Command: seen.command,
					State: state, DryRun: seen.dryRun, Started: seen.at,
				}, nil, cmp.Or(seen.ended, time.Now()))
			}
			delete(w.seen, id)
			delete(w.ended, id)
		}
	}
	for id := range w.unreadNotes {
		if !present[id] {
			delete(w.unreadNotes, id)
		}
	}
	for key := range w.lastEnded {
		if !s.alive(key.pid, key.start) {
			delete(w.lastEnded, key)
		}
	}
}

// untrackedEnd is the state in which the call id, whose note went away, goes
// into the history when no tick of this watcher tracked its command, "" when
// it does not go there now. A tracked one, and one whose tracked command
// ended under the watcher before (ended, carried at a handover), are there
// already or go when forget ends them. Of the rest, one whose command was
// never found while its agent ran a command it could have been is unmatched;
// one its post hook marked ended is done: it ran and returned between two
// ticks, or before any watcher ran. Any other (posted and never matched,
// abandoned, or of an agent that is gone) is not known to have finished, and
// goes nowhere.
func (w *watcher) untrackedEnd(id string, seen seenNote) string {
	if _, tracked := w.tracked[id]; tracked || seen.matched || w.ended[id] || w.carries(id) {
		return ""
	}
	switch {
	case !w.found(id, seen) && seen.childSeen:
		return stateUnmatched
	case !seen.ended.IsZero():
		return stateDone
	}
	return ""
}

// carries reports whether what the watcher before this one handed over,
// before this watcher's first tick takes it on (see adopt), names the call
// id: as a command it tracked, one whose command ended under it, or one the
// history does not hold yet. Its note can go away at that first tick before
// adopt runs, and the call goes into the history as adopt and forget say,
// with its memory, never twice.
func (w *watcher) carries(id string) bool {
	c := w.inherited
	if c == nil {
		return false
	}
	_, tracked := c.Tracked[id]
	return tracked || slices.Contains(c.Ended, id) ||
		slices.ContainsFunc(c.Pending, func(f Finished) bool { return f.ID == id })
}

// found reports whether the command of the call id was found: tracked by a
// tick (matched), or its shell learned when it started, by a fork wake-up
// (see glimpseShell), which keeps the shell in the call's record; a record
// crosses a handover, so a new watcher knows it too. A call whose shell was
// learned but that ended before any tick tracked it was never measured, as
// any call that ends within a tick is not, but its command was found and its
// work tied to it, so it did not run in a shape the guard does not know: it
// is no unmatched call, and nothing is logged or shown for it but that work.
func (w *watcher) found(id string, seen seenNote) bool {
	if seen.matched {
		return true
	}
	rec, ok := w.records.byID(id)
	return ok && rec.Shell.Start != 0
}

// markChildSeen sets childSeen on each waiting note whose agent has a child
// that started after the note, is not in taken (a tracked command's root) and
// has been seen alive, with the same start time, at every tick for at least
// childLinger: its command may have run in a shape match does not know. The
// span is in time, not ticks, because ticks quicken to 100 ms under memory
// pressure. The pre hook's own shell started before NotedStart and never
// counts. agentChildren covers every known agent that runs, with a note or
// without.
func (w *watcher) markChildSeen(s snapshot, notes []note, taken map[int]bool) {
	before, now := w.agentChildren, time.Now()
	w.agentChildren = map[procKey]time.Time{}
	for agent := range w.known {
		if !s.alive(agent.pid, agent.start) {
			continue
		}
		for _, p := range s.children(agent.pid) {
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
