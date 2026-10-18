package guard

import "time"

// protocolVersion is the version of the messages the watcher's socket speaks:
// JSON Lines, one message per line, each with its type.
const protocolVersion = 1

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

// finishedShown is how long a finished command stays in the snapshots.
const finishedShown = 60 * time.Second

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
// largest peak any command reached since the watcher started, and the
// commands it tracks, newest first, then those that finished within the last
// minute, the latest first.
type Snapshot struct {
	At          time.Time     `json:"at"`
	Memory      MemoryView    `json:"memory"`
	LargestPeak uint64        `json:"largest_peak"`
	Commands    []CommandView `json:"commands"`
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
// peak in bytes, its latest sizes, oldest first, and its process tree with
// each process's memory.
type CommandView struct {
	ID       string        `json:"id"`
	Agent    string        `json:"agent"`
	Subagent string        `json:"subagent"`
	Project  string        `json:"project"`
	Cwd      string        `json:"cwd"`
	Command  string        `json:"cmd"`
	State    string        `json:"state"`
	By       string        `json:"by"`
	DryRun   bool          `json:"dry_run"`
	Started  time.Time     `json:"started"`
	Ended    time.Time     `json:"ended"`
	Memory   uint64        `json:"memory"`
	Peak     uint64        `json:"peak"`
	History  []uint64      `json:"history"`
	Tree     []ProcessView `json:"tree"`
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
