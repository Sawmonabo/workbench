package guard

import "time"

// protocolVersion is the version of the messages the watcher's socket speaks:
// JSON Lines, one message per line, each with its type.
const protocolVersion = 2

// The message types. A client says hello with the versions it speaks; the
// watcher answers hello with the highest it shares and its release, or error
// and closes. It then sends a snapshot after each tick; the client may send a
// stop at any time, answered by stopped, or by error when the watcher is busy.
const (
	messageHello    = "hello"
	messageError    = "error"
	messageSnapshot = "snapshot"
	messageStop     = "stop"
	messageStopped  = "stopped"
)

// The states of a command in a snapshot.
const (
	stateRunning   = "running"
	stateStopping  = "stopping"
	stateDone      = "done"
	stateUnmatched = "unmatched"
	stateStopped   = "stopped"
)

// message is one line on the watcher's socket, of type Type; each type uses
// only its own fields.
type message struct {
	Type     string       `json:"type"`
	Versions []int        `json:"versions,omitempty"`
	Version  int          `json:"version,omitempty"`
	Release  string       `json:"release,omitempty"`
	Error    string       `json:"error,omitempty"`
	Snapshot *Snapshot    `json:"snapshot,omitempty"`
	IDs      []string     `json:"ids,omitempty"`
	Force    bool         `json:"force,omitempty"`
	Results  []StopResult `json:"results,omitempty"`
}

// Snapshot is what the watcher sees at one tick: the machine's memory, the
// largest peak any command reached since the watcher started, the commands
// and detached programs it tracks, newest first, and the commands that
// finished that the history does not hold yet, oldest first, each whole, at
// most pendingLimit bytes of them (see keepWithin); the history holds every
// other finished command.
// HistoryError is what the view says of the history, empty when there is
// nothing to say: "Guard history cannot be written: REASON" while the last
// write failed (the writer tries again every ten seconds), REASON naming the
// file once and then why (~/…/history.db: permission denied), else, while
// copies of histories that could not be used are kept, "Guard history was
// damaged and started again at TIME; the old file is PATH", or its form for
// two copies (see HistoryDamaged): the history the writer started then is a
// new file, whose commands are numbered from 1 again.
// Each path in it is named from the home folder (see display.PathFrom).
type Snapshot struct {
	At           time.Time     `json:"at"`
	Memory       MemoryView    `json:"memory"`
	LargestPeak  uint64        `json:"largest_peak"`
	Commands     []CommandView `json:"commands"`
	Pending      []Finished    `json:"pending"`
	HistoryError string        `json:"history_error"`
}

// MemoryView is the machine's memory, in bytes, as the guard reads it. Level
// is macOS's pressure level (normal, warning or critical) with the installed
// memory and swap, or on Linux normal or low by the guard's thresholds, with
// the memory available of the total user memory may take. Used is the memory
// in use of Installed as the system's own monitor counts it (see
// platform.Memory).
type MemoryView struct {
	Level     string `json:"level"`
	Installed uint64 `json:"installed"`
	Used      uint64 `json:"used"`
	Available uint64 `json:"available"`
	Total     uint64 `json:"total"`
	SwapFree  uint64 `json:"swap_free"`
	SwapTotal uint64 `json:"swap_total"`
}

// CommandView is one command in a snapshot: its call's ID, agent, subagent,
// project, folder and command text, its state (running, stopping, done,
// unmatched or stopped), who stopped it (guard or you), whether its note is a
// dry run, when it was noted and when it ended, its memory now and at its
// peak in bytes, the graph of its memory over its whole run in bytes per bar,
// oldest first, at most GraphBars bars, each the largest of the readings it
// covers (see usage), and its process tree with each process's memory. Kind
// is empty for a command, else detached for a program that detached from a
// command; PID is a detached program's first root that runs, and LaunchedBy
// the ID of the call that started it, empty when unknown. Held is, for a
// detached program that runs, why the guard itself will not choose it now:
// "no call" (no call is proved
// to have started it), "young" (none of its roots has been tracked ten
// seconds since it was found or started its program) or "unread" (none of
// those is settled: its setting cannot be read, or it is being judged
// again), the first that applies; it is empty when the guard may choose it.
type CommandView struct {
	ID         string        `json:"id"`
	Kind       string        `json:"kind"`
	PID        int           `json:"pid"`
	LaunchedBy string        `json:"launched_by"`
	Held       string        `json:"held,omitempty"`
	Agent      string        `json:"agent"`
	Subagent   string        `json:"subagent"`
	Project    string        `json:"project"`
	Cwd        string        `json:"cwd"`
	Command    string        `json:"cmd"`
	State      string        `json:"state"`
	By         string        `json:"by"`
	DryRun     bool          `json:"dry_run"`
	Started    time.Time     `json:"started"`
	Ended      time.Time     `json:"ended"`
	Memory     uint64        `json:"memory"`
	Peak       uint64        `json:"peak"`
	History    []uint64      `json:"history"`
	Tree       []ProcessView `json:"tree"`
}

// ProcessView is one process of a command's tree and its resident memory.
type ProcessView struct {
	PID    int    `json:"pid"`
	Name   string `json:"name"`
	Memory uint64 `json:"memory"`
}

// StopResult is the answer to a stop for one ID: OK once the command was
// signalled (or, for a dry-run note, logged), else why not: unknown, already
// finished or not allowed.
type StopResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// memoryView is m as a snapshot shows it.
func memoryView(m memory) MemoryView {
	level := "normal"
	switch {
	case m.Level >= criticalLevel:
		level = "critical"
	case m.Level >= 2:
		level = "warning"
	case m.Level == 0:
		if act, _ := decide(m); act != nothing {
			level = "low"
		}
	}
	return MemoryView{
		Level: level, Installed: m.Installed, Used: m.Used, Available: m.Available,
		Total: m.Total, SwapFree: m.SwapFree, SwapTotal: m.SwapTotal,
	}
}
