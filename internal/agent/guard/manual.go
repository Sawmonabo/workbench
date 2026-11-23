package guard

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// manualReason is what the agent is told of a stop a person asked for.
const manualReason = "Stopped by you (workbench agent guard)."

// manualStop is a stop a person asked for, followed across ticks: the
// processes to wait for, when it was sent, whether it was a forced one and
// whether SIGKILL has gone out, and the command, for the log.
type manualStop struct {
	procs         []proc
	since         time.Time
	force, forced bool
	command       command
}

// manualStop takes a person's stop at a tick, from the snapshot, with the
// protected processes, of that tick. A peer that is not allowed to stop
// (see stopAllowed) is refused for every ID. Each chosen command is stopped as
// the guard's own stop does it, without waiting: a dry-run note only logs; any
// other gets the kill record, then SIGTERM, or SIGKILL with force, and is
// followed at each tick by followManual. A stop that signals nothing removes
// its record and answers already finished. A person's stop was asked for,
// not chosen, so it sets no cooldown and counts toward no refusal or pass. A
// person may stop a unit whose call did not resolve; notes, read at the same
// tick, say whether a unit's call is told (see killRecordFor).
func (w *watcher) manualStop(
	req stopRequest,
	s snapshot,
	notes []note,
	protected map[int]uint64,
) []StopResult {
	if !stopAllowed(req.peer, s, protected) {
		results := make([]StopResult, 0, len(req.ids))
		for _, id := range req.ids {
			results = append(results, StopResult{ID: id, Error: "not allowed"})
		}
		return results
	}
	stop, results := chooseStops(req.ids, w.tracked, s)
	index := map[string]int{}
	for i, r := range results {
		index[r.ID] = i
	}
	for _, c := range stop {
		if err := w.stopFor(c, s, notes, protected, req.force); err != "" {
			results[index[c.Note.ToolUseID]] = StopResult{ID: c.Note.ToolUseID, Error: err}
		}
	}
	return results
}

// stopFor stops one command for a person (see manualStop) and gives why it
// could not, or "". A command already being stopped for a person is not
// signalled again, but a forced stop of one stopped politely sends SIGKILL
// now. A unit's kill record goes to the call that started it, as for the
// guard's own stop (see killRecordFor).
func (w *watcher) stopFor(
	c command,
	s snapshot,
	notes []note,
	protected map[int]uint64,
	force bool,
) string {
	id := c.Note.ToolUseID
	if m := w.manual[id]; m != nil {
		if force && !m.forced {
			kill, wait := escalation(s, m.procs, protected)
			m.procs = wait
			if len(kill) > 0 {
				w.killManual(m, kill)
			}
		}
		return ""
	}
	// size is what the guard's own stop records: the tree's footprint (Pss on
	// Linux), read now, or the last reading when none of it can be read.
	size := w.size(s.commandTree(c, outside(protected)), platform.Footprint)
	if u := w.usage[id]; size == 0 && u != nil {
		size = u.Last
	}
	stopping := append(w.endAttrs(c), "by", "you", "size", size)
	if c.Note.DryRun {
		w.f.event(slog.LevelWarn, EventStopping, "would stop (dry run)",
			append(stopping, "dry_run", true)...)
		return ""
	}
	chosen := targets(s, c, protected)
	if len(chosen) == 0 {
		return "already finished"
	}
	record := w.writeKill(c, notes, manualReason)
	w.f.event(slog.LevelWarn, EventStopping, "stopping", stopping...)
	sig := unix.SIGTERM
	if force {
		sig = unix.SIGKILL
	}
	sent := false
	for _, t := range chosen {
		sent = platform.Signal(platform.Process{PID: t.PID, Start: t.Start}, t.Group, sig) || sent
	}
	if !sent {
		if record != "" {
			_ = os.Remove(record)
		}
		return "already finished"
	}
	if force {
		w.f.event(slog.LevelWarn, EventForced, "sent SIGKILL", append(callAttrs(c), "by", "you")...)
	}
	w.stoppedBy[id] = "you"
	w.manual[id] = &manualStop{
		procs: s.commandTree(c, outside(protected)), since: time.Now(),
		force: force, forced: force, command: c,
	}
	return ""
}

// followManual follows each person's stop at a tick: once its processes are
// all gone it is done; after killAfter it sends SIGKILL to what is left,
// and after giveUp it logs what survived and lets it go.
func (w *watcher) followManual(s snapshot, protected map[int]uint64) {
	for id, m := range w.manual {
		kill, wait := escalation(s, m.procs, protected)
		m.procs = wait
		if len(kill) == 0 {
			delete(w.manual, id)
			continue
		}
		if time.Since(m.since) >= killAfter && !m.forced {
			w.killManual(m, kill)
		}
		if time.Since(m.since) >= giveUp {
			var left []int
			for _, p := range kill {
				left = append(left, p.PID)
			}
			text := fmt.Sprintf("still running %.0f seconds after the stop", giveUp.Seconds())
			w.f.event(slog.LevelWarn, EventGaveUp, text,
				append(callAttrs(m.command), "by", "you", "still_running", left)...)
			delete(w.manual, id)
		}
	}
}

// killManual sends SIGKILL, by PID, to each process escalation chose for a
// person's stop, as the guard's own escalation does, and logs it.
func (w *watcher) killManual(m *manualStop, kill []proc) {
	for _, p := range kill {
		if !p.Zombie {
			platform.Signal(p.Process, false, unix.SIGKILL)
		}
	}
	m.forced = true
	w.f.event(
		slog.LevelWarn,
		EventForced,
		"sent SIGKILL",
		append(callAttrs(m.command), "by", "you")...)
}
