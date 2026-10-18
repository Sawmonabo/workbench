package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// The guard log is JSON Lines from [slog.JSONHandler]: each record has time,
// level, msg and event, one of these, with part (pre, post or watch) and pid,
// the process that wrote it. Records about a call add call, agent, command
// (clipped as in the stop reason), root_pid and, when the note has them, cwd,
// project and subagent; ended, unmatched and stopping add took, the seconds
// since the call's note, and ended and stopping add peak, the command's
// largest size in bytes. A stop adds by (guard), size in bytes, reason, the
// text the agent is told, and memory, the reading that called for it; a
// failure adds error. A log that was cut starts with a record of event
// cut (see [operation.RewriteLog]), which [ReadLog] never gives. The
// watcher's own stdout and stderr, such as a crash's trace, go to its output
// file (see outputFile), which each watcher starts with a record of event
// output; [ReadLog] gives each other line there as a raw event. Logs of
// earlier builds can hold such lines too.
const (
	EventStarted   = "started"   // a watcher started
	EventIdle      = "idle"      // a watcher exited, idle
	EventExited    = "exited"    // a watcher exited because its lock file changed or on a signal
	EventHandover  = "handover"  // a watcher handed over to the installed release's watcher
	EventTracking  = "tracking"  // a noted command was found
	EventEnded     = "ended"     // a tracked command ended
	EventUnmatched = "unmatched" // a note went away while its agent had run a command no shape matched
	EventAbandoned = "abandoned" // a note that never got its post hook was dropped
	EventMemory    = "memory"    // memory was low, but nothing was stopped
	EventStopping  = "stopping"  // a command is stopped (or, with dry_run, would be)
	EventForced    = "forced"    // a stopped command was sent SIGKILL
	EventGaveUp    = "gave-up"   // a stopped command outlived the stop
	EventRefused   = "refused"   // a stop signalled nothing
	EventError     = "error"     // something could not be read or written
	EventRaw       = "raw"       // a line that is not a record, such as a crash's trace
	EventOutput    = "output"    // a watcher started writing its output file; never shown
)

// Event is the part of a guard log record the log viewer shows. A raw line
// of the watcher's output file has Output set, Started at the start of the
// watcher that wrote it (zero when unknown) and Until at the next watcher's
// start, zero while none has started since.
type Event struct {
	Time     time.Time `json:"time"`
	Level    string    `json:"level"`
	Msg      string    `json:"msg"`
	Event    string    `json:"event"`
	Call     string    `json:"call"`
	Agent    string    `json:"agent"`
	Subagent string    `json:"subagent"`
	Project  string    `json:"project"`
	Cwd      string    `json:"cwd"`
	Command  string    `json:"command"`
	By       string    `json:"by"`
	Took     float64   `json:"took"`
	Size     uint64    `json:"size"`
	Peak     uint64    `json:"peak"`
	Floor    uint64    `json:"floor"`
	// Memory is the reading of a stop or memory record: macOS's level, or
	// Linux's available and total bytes.
	Memory struct {
		Level     int    `json:"level"`
		Available uint64 `json:"available"`
		Total     uint64 `json:"total"`
	} `json:"memory"`
	Reason       string    `json:"reason"`
	Error        string    `json:"error"`
	Process      int       `json:"process"`
	StillRunning []int     `json:"still_running"`
	Release      string    `json:"release"`
	OldRelease   string    `json:"old_release"`
	NewRelease   string    `json:"new_release"`
	NewPID       int       `json:"new_pid"`
	DryRun       bool      `json:"dry_run"`
	Started      time.Time `json:"started"`
	Output       bool      `json:"-"`
	Until        time.Time `json:"-"`
}

func logFile(root string) string { return filepath.Join(root, "watch.log") }

// outputFile is the watcher's stdout and stderr beside the log at path:
// watch.err, which is never cut, only renamed to watch.err.1 by a watcher
// that starts while it is over the log's cap (see [operation.RotateOutput]).
func outputFile(path string) string { return filepath.Join(filepath.Dir(path), "watch.err") }

// LogPath is the guard log's path. It reads nothing and creates nothing.
func LogPath() (string, error) {
	c, err := operation.Resolve(operation.Options{ReadOnly: true})
	if err != nil {
		return "", err
	}
	return logFile(filepath.Join(c.Paths.State, "guard")), nil
}

// followPoll is how often a followed log is checked for new lines.
const followPoll = 250 * time.Millisecond

// entry is one line ReadLog gives: raw, decoded, and the time it is ordered
// by.
type entry struct {
	raw []byte
	e   Event
	at  time.Time
}

// ReadLog calls emit with each record of the log at path and each line of
// the watcher's output file, raw (without its newline) and decoded, oldest
// first. A non-blank line that is not a record, such as a crash's trace,
// comes as an [EventRaw] record at level ERROR with the line as its msg and no
// time, which is unknown, encoded by [slog.JSONHandler] as the log's records
// are; one from the output file adds started, the time of the output record
// its watcher wrote there as it started, and is ordered by it among the
// records (a line of the log by the record before it). A missing file has no
// lines. With follow it then keeps calling emit as lines are appended, until
// ctx ends, keeping its place across a cut and a rotation as
// [operation.LogReader] does.
func ReadLog(
	ctx context.Context,
	path string,
	follow bool,
	emit func(raw []byte, e Event) error,
) error {
	v := &logView{
		log:    operation.NewLogReader(path),
		output: operation.NewLogReader(outputFile(path)),
	}
	defer v.log.Close()
	defer v.output.Close()
	older := operation.NewLogReader(outputFile(path) + ".1")
	before, err := older.Read()
	older.Close()
	if err != nil {
		return err
	}
	for {
		entries, err := v.read(before)
		if err != nil {
			return err
		}
		for _, next := range entries {
			if err = emit(next.raw, next.e); err != nil {
				return err
			}
		}
		if !follow {
			return nil
		}
		before = nil
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(followPoll):
		}
	}
}

// logView is what [ReadLog] keeps between reads: the readers and the time
// the last record of each file gave.
type logView struct {
	log, output      *operation.LogReader
	logAt, startedAt time.Time
}

// read gives the lines appended to the output file (after earlier, lines
// read before it) and to the log since the last read, merged by the time
// each is ordered by.
func (v *logView) read(earlier []byte) ([]entry, error) {
	data, err := v.output.Read()
	if err != nil {
		return nil, err
	}
	outputs := v.outputs(append(earlier, data...))
	if data, err = v.log.Read(); err != nil {
		return nil, err
	}
	var records []entry
	for raw, e := range lines(data) {
		if e.Event == EventRaw {
			raw = rawRecord(e)
		} else {
			v.logAt = e.Time
		}
		records = append(records, entry{raw, e, v.logAt})
	}
	merged := make([]entry, 0, len(outputs)+len(records))
	for len(records) > 0 || len(outputs) > 0 {
		if len(outputs) > 0 && (len(records) == 0 || outputs[0].at.Before(records[0].at)) {
			merged, outputs = append(merged, *outputs[0]), outputs[1:]
		} else {
			merged, records = append(merged, records[0]), records[1:]
		}
	}
	return merged, nil
}

// outputs are the output file's lines in data, each with the start of the
// watcher that wrote it and, when this data holds it, the next one's.
func (v *logView) outputs(data []byte) []*entry {
	var outputs, running []*entry
	for raw, e := range lines(data) {
		if e.Event == EventOutput {
			for _, o := range running {
				o.e.Until = e.Time
			}
			running, v.startedAt = nil, e.Time
			continue
		}
		e.Output, e.Started = true, v.startedAt
		if e.Event == EventRaw {
			raw = rawRecord(e)
		}
		o := &entry{raw, e, v.startedAt}
		running = append(running, o)
		outputs = append(outputs, o)
	}
	return outputs
}

// lines are the non-blank lines of data, each with its record, or an
// [EventRaw] record for a line that is not one.
func lines(data []byte) func(yield func([]byte, Event) bool) {
	return func(yield func([]byte, Event) bool) {
		for line := range bytes.Lines(data) {
			raw := bytes.TrimSuffix(line, []byte("\n"))
			if len(bytes.TrimSpace(raw)) == 0 {
				continue
			}
			var e Event
			if json.Unmarshal(raw, &e) != nil || e.Event == "" {
				e = Event{Level: slog.LevelError.String(), Msg: string(raw), Event: EventRaw}
			}
			if !yield(raw, e) {
				return
			}
		}
	}
}

// rawRecord is a raw line as a record without a time, which
// [slog.JSONHandler] leaves out, and with started when it is known.
func rawRecord(e Event) []byte {
	var b bytes.Buffer
	r := slog.NewRecord(time.Time{}, slog.LevelError, e.Msg, 0)
	r.AddAttrs(slog.String("event", EventRaw))
	if !e.Started.IsZero() {
		r.AddAttrs(slog.Time("started", e.Started))
	}
	_ = slog.NewJSONHandler(&b, nil).Handle(context.Background(), r)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}
