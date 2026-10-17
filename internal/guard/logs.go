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
// (clipped as in the stop reason), root_pid and size in bytes; a stop adds
// reason, the text the agent is told, and memory, the reading that called for
// it; a failure adds error. The watcher's own output goes to the same file,
// so a crash's trace can sit among the records: [ReadLog] gives each such
// line as a raw event. A log that was cut starts with a record of event cut
// (see [operation.RewriteLog]), which [ReadLog] never gives.
const (
	EventStarted   = "started"   // a watcher started
	EventIdle      = "idle"      // a watcher exited, idle
	EventExited    = "exited"    // a watcher exited because its lock file changed
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
	EventRaw       = "raw"       // a line of the log that is not a record, such as a crash's trace
)

// Event is the part of a guard log record the log viewer shows.
type Event struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Msg     string    `json:"msg"`
	Event   string    `json:"event"`
	Command string    `json:"command"`
	Size    uint64    `json:"size"`
	Reason  string    `json:"reason"`
	Error   string    `json:"error"`
	DryRun  bool      `json:"dry_run"`
}

func logFile(root string) string { return filepath.Join(root, "watch.log") }

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

// ReadLog calls emit with each record of the log at path, raw (without its
// newline) and decoded, oldest first. A non-blank line that is not a record,
// such as the trace of a watcher that crashed, comes as an [EventRaw] record
// at level ERROR with the line as its msg and no time, which is unknown,
// encoded by [slog.JSONHandler] as the log's records are. A missing log has
// no records. With follow it then keeps calling emit as records are appended,
// until ctx ends, keeping its place across a cut as [operation.LogReader]
// does.
func ReadLog(
	ctx context.Context,
	path string,
	follow bool,
	emit func(raw []byte, e Event) error,
) error {
	reader := operation.NewLogReader(path)
	for {
		data, err := reader.Read()
		if err != nil {
			return err
		}
		for line := range bytes.Lines(data) {
			raw := bytes.TrimSuffix(line, []byte("\n"))
			if len(bytes.TrimSpace(raw)) == 0 {
				continue
			}
			var e Event
			if json.Unmarshal(raw, &e) != nil || e.Event == "" {
				e = Event{Level: slog.LevelError.String(), Msg: string(raw), Event: EventRaw}
				raw = rawRecord(e.Msg)
			}
			if err = emit(raw, e); err != nil {
				return err
			}
		}
		if !follow {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(followPoll):
		}
	}
}

// rawRecord is a raw line as a record without a time, which
// [slog.JSONHandler] leaves out.
func rawRecord(line string) []byte {
	var b bytes.Buffer
	r := slog.NewRecord(time.Time{}, slog.LevelError, line, 0)
	r.AddAttrs(slog.String("event", EventRaw))
	_ = slog.NewJSONHandler(&b, nil).Handle(context.Background(), r)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}
