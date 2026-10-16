package guard

import (
	"encoding/json"
	"fmt"
	"io/fs"
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

// folders is <state>/guard and its notes and killed folders, each private.
type folders struct{ root, notes, killed string }

func openFolders() (folders, error) {
	c, err := operation.Resolve(operation.Options{ReadOnly: true})
	if err != nil {
		return folders{}, err
	}
	f := folders{root: filepath.Join(c.Paths.State, "guard")}
	if f.notes, err = operation.PrivateDirectory(c.Paths.State, "guard/notes"); err != nil {
		return folders{}, err
	}
	if f.killed, err = operation.PrivateDirectory(c.Paths.State, "guard/killed"); err != nil {
		return folders{}, err
	}
	return f, nil
}

func (f folders) lock() string            { return filepath.Join(f.root, "watch.lock") }
func (f folders) log() string             { return filepath.Join(f.root, "watch.log") }
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

// logf appends one line to the guard log, cut once it is over 1 MiB
// (see [operation.AppendLog]).
func (f folders) logf(format string, args ...any) {
	operation.AppendLog(
		f.log(),
		fmt.Sprintf("%s %d %s", time.Now().Format(time.RFC3339), os.Getpid(),
			fmt.Sprintf(format, args...)),
		logLimit,
	)
}
