package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
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
// line as a raw event.
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
// until ctx ends: a log cut in place (see [operation.AppendLog]) is read on
// from the line after the last one read, or from its start when the cut took
// that line too, and a log created or replaced is read from its start.
func ReadLog(
	ctx context.Context,
	path string,
	follow bool,
	emit func(raw []byte, e Event) error,
) error {
	var identity os.FileInfo
	var offset int64
	var last []byte // the last line read, with its newline
	for {
		info, err := os.Stat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			identity, offset, last = nil, 0, nil
		case err != nil:
			return err
		default:
			if identity != nil && !os.SameFile(identity, info) {
				offset, last = 0, nil
			}
			identity = info
			if offset, last, err = readFrom(path, offset, last, emit); err != nil {
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

// readFrom emits the complete lines after offset and returns the new offset
// and last line. It reads from the last line on, and when that is no longer
// where it was, the log was cut: it reads the whole log and emits what
// follows the last line, or every line when the cut took it.
func readFrom(
	path string,
	offset int64,
	last []byte,
	emit func([]byte, Event) error,
) (int64, []byte, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil, nil
	}
	if err != nil {
		return offset, last, err
	}
	defer func() { _ = file.Close() }()
	start := offset - int64(len(last))
	if _, err = file.Seek(start, io.SeekStart); err != nil {
		return offset, last, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return offset, last, err
	}
	if !bytes.HasPrefix(data, last) {
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return offset, last, err
		}
		if data, err = io.ReadAll(file); err != nil {
			return offset, last, err
		}
		start = 0
		if at := lastLine(data, last); at >= 0 {
			start = int64(at + len(last))
		} else {
			last = nil
		}
		data = data[start:]
	} else {
		start = offset
		data = data[len(last):]
	}
	end := bytes.LastIndexByte(data, '\n') + 1
	for line := range bytes.Lines(data[:end]) {
		last = line
		var e Event
		raw := bytes.TrimSuffix(line, []byte("\n"))
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		if json.Unmarshal(raw, &e) != nil || e.Event == "" {
			e = Event{Level: slog.LevelError.String(), Msg: string(raw), Event: EventRaw}
			raw = rawRecord(e.Msg)
		}
		if err = emit(raw, e); err != nil {
			return offset, last, err
		}
	}
	return start + int64(end), last, nil
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

// lastLine is where the whole line line last starts in data, or -1.
func lastLine(data, line []byte) int {
	if len(line) == 0 {
		return -1
	}
	for end := len(data); end > 0; {
		at := bytes.LastIndex(data[:end], line)
		if at < 0 {
			return -1
		}
		if at == 0 || data[at-1] == '\n' {
			return at
		}
		end = at + len(line) - 1
	}
	return -1
}
