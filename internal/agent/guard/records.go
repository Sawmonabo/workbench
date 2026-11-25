package guard

import (
	"cmp"
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
// tracked, and stays zero until the call is tracked or its shell is learned
// when it starts (see glimpseShell); a call that ended within one tick while
// no watcher followed its agent never has one. StartedAgent marks a call found to have started
// an agent session (see watcher.markStartedAgents), set once and never
// cleared: that session's own processes carry the call's stamps, so a stamp
// that resolves to the call no longer proves the call's command forked the
// process (see stampProves).
type callRecord struct {
	ID           string    `json:"id"`
	Agent        procKey   `json:"agent"`
	Kind         kind      `json:"kind"`
	Subagent     string    `json:"subagent,omitempty"`
	Project      string    `json:"project,omitempty"`
	Cwd          string    `json:"cwd,omitempty"`
	DryRun       bool      `json:"dry_run,omitempty"`
	Shell        procKey   `json:"shell"`
	NotedAt      time.Time `json:"noted_at"`
	StartedAgent bool      `json:"started_agent,omitempty"`
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
		rec.Shell = procKey{root.PID, root.Start}
		r.byCall[id] = rec
	}
}

// startedAgent marks the call id, when its record is kept, as one that
// started an agent session.
func (r *records) startedAgent(id string) {
	if rec, ok := r.byCall[id]; ok && !rec.StartedAgent {
		rec.StartedAgent = true
		r.byCall[id] = rec
	}
}

// stampProves reports whether a stamp alone that resolves to the call id
// proves that the call's command forked the process: the call's record is
// kept and the call did not start an agent session, whose own subprocesses
// and shell setup carry the call's stamps too.
func (r *records) stampProves(id string) bool {
	rec, ok := r.byCall[id]
	return ok && !rec.StartedAgent
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
		if rec.Agent == agent && rec.Shell.pid == shell && rec.Shell.start != 0 &&
			fitsShell(rec.Shell.start, second) {
			found = append(found, rec)
		}
	}
	if len(found) != 1 {
		return callRecord{}, false
	}
	return found[0], true
}

// fitsShell reports whether a call stamp's second fits a shell that started
// at start, a kernel start time: in that second or no more than stampSlack
// after it. byShell judges by it.
func fitsShell(start uint64, second int64) bool {
	started := platform.StartTime(start)
	if started.IsZero() {
		return false
	}
	late := second - started.Unix()
	return late >= 0 && late <= int64(stampSlack/time.Second)
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
