package guard

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// recordsKept bounds the call records kept in all, so a long session cannot
// grow the watcher without end; the oldest are dropped first.
const recordsKept = 4096

// stampSlack is how long before the second in a call stamp its shell may
// have started: the stamp line runs once the shell has started and read the
// env file, a moment after the kernel's start time, and that moment can be
// long on a loaded machine.
const stampSlack = 30 * time.Second

// callRecord is what the watcher keeps of a call whose note it read, after
// the note is gone, so that work a call's command left running can be traced
// back to the call. Shell is the command's shell, the root the watcher
// tracked, and stays zero until the call is tracked: a call that ended
// within one tick never is.
type callRecord struct {
	ID       string    `json:"id"`
	Agent    procKey   `json:"agent"`
	Kind     kind      `json:"kind"`
	Subagent string    `json:"subagent,omitempty"`
	Project  string    `json:"project,omitempty"`
	Cwd      string    `json:"cwd,omitempty"`
	DryRun   bool      `json:"dry_run,omitempty"`
	Shell    procRef   `json:"shell"`
	NotedAt  time.Time `json:"noted_at"`
}

// MarshalJSON writes a procKey as a procRef, so a call record keeps its agent
// when it is carried to the next watcher.
func (k procKey) MarshalJSON() ([]byte, error) {
	return json.Marshal(procRef{PID: k.pid, Start: k.start})
}

// UnmarshalJSON reads a procKey written by MarshalJSON.
func (k *procKey) UnmarshalJSON(data []byte) error {
	var ref procRef
	if err := json.Unmarshal(data, &ref); err != nil {
		return err
	}
	*k = procKey{ref.PID, ref.Start}
	return nil
}

// records are the call records by call ID. The zero value holds none and is
// ready to use.
type records struct {
	byCall map[string]callRecord
}

// add keeps the record of n's call, unless one is kept already (see keep).
func (r *records) add(n note) {
	if _, ok := r.byCall[n.ToolUseID]; ok {
		return
	}
	r.keep(callRecord{
		ID:       n.ToolUseID,
		Agent:    procKey{n.AgentPID, n.AgentStart},
		Kind:     n.Kind,
		Subagent: n.Subagent,
		Project:  n.Project,
		Cwd:      n.Cwd,
		DryRun:   n.DryRun,
		NotedAt:  n.NotedAt,
	})
}

// keep keeps rec in place of any record of its call, and drops the oldest
// other record once more than recordsKept are kept. Never rec itself: a note
// older than every kept record would otherwise be dropped by the read that
// added it, and come back at every read after.
func (r *records) keep(rec callRecord) {
	if r.byCall == nil {
		r.byCall = map[string]callRecord{}
	}
	r.byCall[rec.ID] = rec
	if len(r.byCall) <= recordsKept {
		return
	}
	var oldest *callRecord
	for id, other := range r.byCall {
		if id != rec.ID && (oldest == nil || olderRecord(other, *oldest) < 0) {
			oldest = &other
		}
	}
	if oldest != nil {
		delete(r.byCall, oldest.ID)
	}
}

// tracked keeps root as the shell of the call id, when its record is kept.
func (r *records) tracked(id string, root proc) {
	if rec, ok := r.byCall[id]; ok {
		rec.Shell = procRef{PID: root.PID, Start: root.Start}
		r.byCall[id] = rec
	}
}

// byID is the record of the call id.
func (r *records) byID(id string) (callRecord, bool) {
	rec, ok := r.byCall[id]
	return rec, ok
}

// byShell is the record of agent's call whose shell has the process ID shell
// and started, by the kernel's start time, in the stamp's second or no more
// than stampSlack before it. A later shell that reuses the ID started at
// another time, so a stamp never resolves to another call. It reports false
// when no record matches, or more than one does.
func (r *records) byShell(agent procKey, shell int, second int64) (callRecord, bool) {
	if shell <= 0 {
		return callRecord{}, false
	}
	var found []callRecord
	for _, rec := range r.byCall {
		if rec.Agent != agent || rec.Shell.PID != shell || rec.Shell.Start == 0 {
			continue
		}
		started := platform.StartTime(rec.Shell.Start)
		if started.IsZero() {
			continue
		}
		if late := second - started.Unix(); late >= 0 && late <= int64(stampSlack/time.Second) {
			found = append(found, rec)
		}
	}
	if len(found) != 1 {
		return callRecord{}, false
	}
	return found[0], true
}

// prune drops the records of agents that are gone.
func (r *records) prune(alive func(procKey) bool) {
	for id, rec := range r.byCall {
		if !alive(rec.Agent) {
			delete(r.byCall, id)
		}
	}
}

// all are the records, oldest first, as a copy.
func (r *records) all() []callRecord {
	out := make([]callRecord, 0, len(r.byCall))
	for _, rec := range r.byCall {
		out = append(out, rec)
	}
	slices.SortFunc(out, olderRecord)
	return out
}

func olderRecord(a, b callRecord) int {
	return cmp.Or(a.NotedAt.Compare(b.NotedAt), cmp.Compare(a.ID, b.ID))
}
