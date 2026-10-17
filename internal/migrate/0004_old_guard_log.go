package migrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// oldGuardLog removes the free-text lines earlier builds of the agent command
// guard wrote to its log, which is JSON Lines now: the viewer would show each
// as raw watcher output. It removes those lines from guard/watch.log in place
// and keeps every other line, records and raw output alike, because a running
// watcher's stdout and stderr stay open on the file and its records may
// follow the old lines; it rewrites the file as the log's own cut does (see
// [operation.AppendLog]). It deletes guard/watch.log.1, the copy the first
// builds rotated the log into, when that holds such a line. A file that holds
// none is never touched.
var oldGuardLog = Migration{
	ID:    "0004-old-guard-log",
	Title: "Remove the old free-text lines from the guard log",
	Doing: "removing the old free-text lines from the guard log",
	What: "Removes the free-text lines earlier builds of the agent command guard wrote " +
		"(\"<time> <pid> watch: …\", or pre or post) from the guard log, watch.log in the " +
		"guard folder of Workbench's state folder, keeping every other line in place, and " +
		"deletes watch.log.1 beside it, the copy the first builds rotated the log into, when " +
		"it holds such lines. A log without them is left alone.",
	Recovery: "nothing to undo: the removed lines are in an old format nothing reads, and " +
		"the guard's records stay",
	Check: func(l Locations) (Status, string) {
		var text, other []string
		for _, path := range guardLogPaths(l) {
			shown := operation.ShowPath(l.Home, path)
			lines, err := oldGuardLines(path)
			switch {
			case errors.Is(err, fs.ErrNotExist), err == nil && lines == 0:
			case errors.Is(err, errNotRegular):
				other = append(other, shown)
			case err != nil:
				text = append(text, "can't read "+shown)
			case filepath.Ext(path) == ".1":
				text = append(text, "deletes "+shown)
			default:
				text = append(text, fmt.Sprintf("removes %d old lines from %s", lines, shown))
			}
		}
		if len(other) > 0 {
			text = append(text, keeps(other))
		}
		if len(text) == len(other) {
			return Done, strings.Join(text, " and ")
		}
		return Pending, strings.Join(text, " and ")
	},
	Run: func(_ context.Context, l Locations) ([]string, error) {
		var kept []string
		for _, path := range guardLogPaths(l) {
			data, err := readGuardLog(path)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				continue
			case errors.Is(err, errNotRegular):
				kept = append(kept, operation.ShowPath(l.Home, path))
				continue
			case err != nil:
				return kept, err
			}
			var rest bytes.Buffer
			removed := false
			for line := range bytes.Lines(data) {
				if oldGuardLine.Match(line) {
					removed = true
				} else {
					rest.Write(line)
				}
			}
			switch {
			case !removed:
			case filepath.Ext(path) == ".1":
				err = operation.RemoveIfThere(path)
			default:
				// Written in place, not replaced: the watcher's output stays
				// open on it.
				err = os.WriteFile(path, rest.Bytes(), 0o600)
			}
			if err != nil {
				return kept, err
			}
		}
		return kept, nil
	},
}

// oldGuardLine is one line of the old free-text format: the RFC 3339 time,
// the writing process's PID, and the part, "watch:", "pre:", "post:", or pre
// or post with the call's tool_use_id.
var oldGuardLine = regexp.MustCompile(
	`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2}) \d+ ` +
		`(watch|pre|post)( [A-Za-z0-9_-]{1,128})?: `,
)

// guardLogPaths are the guard log and the copy the first builds rotated it
// into.
func guardLogPaths(l Locations) []string {
	log := filepath.Join(l.Runtime.State, "guard", "watch.log")
	return []string{log, log + ".1"}
}

// errNotRegular is a guard log path that holds something other than a
// regular file, such as a link, which is never read through and stays.
var errNotRegular = errors.New("not a regular file")

// readGuardLog reads path when Lstat shows a regular file.
func readGuardLog(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errNotRegular
	}
	return os.ReadFile(path)
}

// oldGuardLines counts the old free-text lines in path.
func oldGuardLines(path string) (int, error) {
	data, err := readGuardLog(path)
	if err != nil {
		return 0, err
	}
	count := 0
	for line := range bytes.Lines(data) {
		if oldGuardLine.Match(line) {
			count++
		}
	}
	return count, nil
}
