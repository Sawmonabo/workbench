package guard

// This file keeps a record on disk of each detached unit the watcher tracks,
// so a watcher that starts fresh, after one that was killed, takes on the
// units that still run (see restoreUnits).

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// unitRecord is what the watcher keeps on disk of a unit it tracks, in the
// guard folder's units folder, private, one file named by the unit's ID: the
// system start it was written in (Boot, see platform.BootID), since a
// process's start time names it only until the system restarts; its made-up
// note; its roots, each with the program it was judged in, whether it is
// settled, and its process name; its call and whether it was tied by stamp
// alone (Detached); the call's record (Call), which a unit tied by stamp alone
// needs to keep its call (see records.stampProves); whether it is passed over
// and who stopped it; and each root's chain in the ancestry store (see
// ancestry.chain), so a root still leaves the unit when what it came from is
// found to be an agent or protected (see dropAndLeave). Neither form of a
// root's command line is written (Args and Shown): the view's is read again
// from the process, and the one a root was judged in where the kernel keeps
// no versions can hold environment strings, so there each root counts as
// running another program and is judged again (see rejudgeRoots). Its memory
// is not kept either, and starts anew, as a command carried without memory
// does.
type unitRecord struct {
	Boot      string          `json:"boot"`
	Note      note            `json:"note"`
	Detached  detached        `json:"detached"`
	Call      *callRecord     `json:"call,omitempty"`
	Passed    bool            `json:"passed,omitempty"`
	StoppedBy string          `json:"stopped_by,omitempty"`
	Ancestry  []carriedOrigin `json:"ancestry,omitempty"`
}

// unitRecordOf is the record of the tracked unit c, written in the system
// start boot.
func (w *watcher) unitRecordOf(id string, c command, boot string) unitRecord {
	rec := unitRecord{
		Boot: boot, Note: c.Note, Detached: *c.Detached,
		Passed: w.passed[id], StoppedBy: w.stoppedBy[id],
	}
	rec.Detached.Roots = slices.Clone(c.Detached.Roots)
	for i := range rec.Detached.Roots {
		r := &rec.Detached.Roots[i]
		r.Args, r.Shown = "", ""
		rec.Ancestry = append(rec.Ancestry, w.ancestry.chain(procKey{r.PID, r.Start})...)
	}
	if call, ok := w.records.byID(c.Detached.Call); ok && c.Detached.Call != "" {
		rec.Call = &call
	}
	return rec
}

// saveUnits brings the unit records on disk in step with the units tracked,
// at the end of each tick: it writes the record of each unit whose record
// changed (see unitRecordOf) and deletes the record of each unit no longer
// tracked. Nothing is written when the system start cannot be read, since
// nothing could be taken on from such a record. A failure is logged once,
// until a save goes through, and the save is tried again at the next tick.
func (w *watcher) saveUnits() {
	if w.boot == "" {
		boot, err := platform.BootID()
		if err != nil {
			w.unitsFailed(err)
			return
		}
		w.boot = boot
	}
	if w.unitsSaved == nil {
		if err := w.f.makeUnits(); err != nil {
			w.unitsFailed(err)
			return
		}
		w.unitsSaved = map[string][]byte{}
	}
	var errs []error
	for id, c := range w.tracked {
		if c.Detached == nil {
			continue
		}
		data, err := json.Marshal(w.unitRecordOf(id, c, w.boot))
		if err == nil && !bytes.Equal(data, w.unitsSaved[id]) {
			if err = operation.ReplacePrivateFile(w.f.unit(id), data); err == nil {
				w.unitsSaved[id] = data
			}
		}
		errs = append(errs, err)
	}
	for id := range w.unitsSaved {
		if c, ok := w.tracked[id]; !ok || c.Detached == nil {
			err := removeUnit(w.f.units, id+".json")
			if err == nil {
				delete(w.unitsSaved, id)
			}
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		w.unitsFailed(err)
		return
	}
	w.unitSaveFailed = false
}

// dropStrayUnits deletes, for a watcher handed the watch, at its first tick
// once it has taken on what was carried, the record of each unit it does not
// track: the records are the watcher's before it, and what it carried is
// what it tracked last. A folder that cannot be listed deletes nothing.
func (w *watcher) dropStrayUnits() {
	entries, err := listFolder(w.f.units)
	if err != nil {
		return
	}
	for _, entry := range entries {
		id, _ := strings.CutSuffix(entry.Name(), ".json")
		if c, ok := w.tracked[id]; !ok || c.Detached == nil {
			_ = removeUnit(w.f.units, entry.Name())
		}
	}
}

// unitsFailed logs, once until a save goes through, that the unit records
// cannot be read or kept: a watcher that starts fresh then may not take on
// the units.
func (w *watcher) unitsFailed(err error) {
	if !w.unitSaveFailed {
		w.unitSaveFailed = true
		w.f.event(slog.LevelError, EventError,
			"cannot read or keep the records of the detached work the guard tracks; a watcher "+
				"that starts after this one is killed may not find it", "error", err)
	}
}

// removeUnit deletes the file name in the units folder units; one already
// gone is no error.
func removeUnit(units, name string) error {
	return operation.RemoveIfThere(filepath.Join(units, name))
}

// restoreUnits is for a watcher that starts fresh, at its first tick, in
// place of what a watcher handing over carries: it takes on each unit whose
// record (see unitRecord) was written in this system start and names at
// least one root that runs, with its start time, in s, as adopt takes on a
// carried unit (see adoptUnit), with only those roots, the record's call
// record, kept in place of the one made from a note on disk, the record's
// ancestry merged into the store (see ancestry.adopt) before this tick
// records the trees, and its agent known while it is in the process table,
// as a handover carries the known agents: the unit was made only for a known
// agent (see unitRootWhy), and a root judged again for an agent the guard
// does not know would leave its unit. Each root's ten seconds start again,
// as at a handover. A record that does not parse, of another system start,
// of a unit none of whose roots runs, or that does not name a unit is
// deleted, and one that cannot be read is left. Every record is left when
// the folder cannot be listed or the system start read: restoreUnits then
// reports false, and the next tick tries again, taking on no unit already
// tracked.
func (w *watcher) restoreUnits(s snapshot) bool {
	entries, err := listFolder(w.f.units)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	boot, bootErr := platform.BootID()
	if err = errors.Join(err, bootErr); err != nil {
		w.unitsFailed(err)
		return false
	}
	for _, entry := range entries {
		id, _ := strings.CutSuffix(entry.Name(), ".json")
		if _, tracked := w.tracked[id]; tracked {
			continue
		}
		var rec unitRecord
		data, err := os.ReadFile(w.f.unit(id))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			w.unitsFailed(err)
			continue
		}
		if !idPattern.MatchString(id) || !strings.HasPrefix(id, unitPrefix) ||
			json.Unmarshal(data, &rec) != nil || rec.Boot != boot || rec.Note.ToolUseID != id {
			_ = removeUnit(w.f.units, entry.Name())
			continue
		}
		unit := rec.Detached
		unit.Roots = slices.DeleteFunc(slices.Clone(unit.Roots), func(r detachedRoot) bool {
			return !s.alive(r.PID, r.Start)
		})
		if len(unit.Roots) == 0 {
			_ = removeUnit(w.f.units, entry.Name())
			continue
		}
		if rec.Call != nil && rec.Call.ID == unit.Call {
			w.records.keep(*rec.Call)
		}
		if agent := (procKey{rec.Note.AgentPID, rec.Note.AgentStart}); s.present(
			agent.pid,
			agent.start,
		) {
			w.known[agent] = rec.Note.Kind
		}
		w.ancestry.adopt(rec.Ancestry, time.Now())
		var passed []string
		if rec.Passed {
			passed = []string{id}
		}
		w.adoptUnit(s, id, unit, &carried{Notes: map[string]note{id: rec.Note}, Passed: passed})
		if rec.StoppedBy != "" {
			w.stoppedBy[id] = rec.StoppedBy
		}
	}
	return true
}
