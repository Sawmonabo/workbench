package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

const logLimit = 1 << 20

// idPattern is a tool_use_id that may name a file; any other call is not
// guarded.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// kill is the record the watcher leaves for the post hook, which reads only
// Reason; ToolUseID and At are for a person reading the record.
type kill struct {
	ToolUseID string    `json:"tool_use_id"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
}

// folders is <state>/guard and its notes and killed folders, each private,
// with the part (pre, post or watch) that opened them and its logger.
type folders struct {
	root, notes, killed, part string
	logger                    *slog.Logger
}

func openFolders(part string) (folders, error) {
	c, err := operation.Resolve(operation.Options{ReadOnly: true})
	if err != nil {
		return folders{}, err
	}
	f := folders{root: filepath.Join(c.Paths.State, "guard"), part: part}
	if f.notes, err = operation.PrivateDirectory(c.Paths.State, "guard/notes"); err != nil {
		return folders{}, err
	}
	if f.killed, err = operation.PrivateDirectory(c.Paths.State, "guard/killed"); err != nil {
		return folders{}, err
	}
	f.logger = slog.New(slog.NewJSONHandler(logWriter(f.log()), nil))
	return f, nil
}

func (f folders) lock() string            { return filepath.Join(f.root, "watch.lock") }
func (f folders) log() string             { return logFile(f.root) }
func (f folders) output() string          { return outputFile(f.log()) }
func (f folders) socket() string          { return socketFile(f.root) }
func (f folders) note(id string) string   { return filepath.Join(f.notes, id+".json") }
func (f folders) record(id string) string { return filepath.Join(f.killed, id+".json") }

// noteIDs are the IDs of the notes on disk.
func (f folders) noteIDs() ([]string, error) {
	entries, err := listFolder(f.notes)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if id, ok := strings.CutSuffix(entry.Name(), ".json"); ok && idPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// listFolder lists a guard folder without temporary files, which start
// ".write-" (see [operation.ReplacePrivateFile]), after
// [operation.RemoveStaleWrites].
func listFolder(folder string) ([]fs.DirEntry, error) {
	operation.RemoveStaleWrites(folder)
	all, err := os.ReadDir(folder)
	if err != nil {
		return nil, err
	}
	var entries []fs.DirEntry
	for _, entry := range all {
		if !strings.HasPrefix(entry.Name(), ".write-") {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// writeJSON replaces the private file at path with value, so a reader never
// sees half a file.
func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return operation.ReplacePrivateFile(path, data)
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

// logWriter appends what it is given to the guard log at its path as one
// line, cut once the log is over 1 MiB (see [operation.AppendLog]). A
// [slog.JSONHandler] makes one Write per record, so each record is one line.
type logWriter string

func (path logWriter) Write(p []byte) (int, error) {
	operation.AppendLog(string(path), string(bytes.TrimSuffix(p, []byte("\n"))), logLimit)
	return len(p), nil
}

// event appends one record to the guard log: event names it (see
// [EventStarted]), part and pid name the process writing it, and args are the
// record's other attributes, as for [slog.Logger.Log].
func (f folders) event(level slog.Level, event, msg string, args ...any) {
	f.logger.Log(context.Background(), level, msg,
		append([]any{"event", event, "part", f.part, "pid", os.Getpid()}, args...)...)
}
