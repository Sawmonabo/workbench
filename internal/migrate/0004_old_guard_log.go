package migrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// oldGuardLog removes the free-text lines earlier builds of the agent command
// guard wrote to its log, which is JSON Lines now: the viewer would show each
// as raw watcher output. It removes those lines from watch.log in the guard
// folder (see guard.Root) in place and keeps every other line, records and
// raw output alike, because a running watcher's stdout and stderr stay open
// on the file and its records may follow the old lines; it rewrites the file
// with the log's own cut (see [operation.RewriteLog]). It deletes
// watch.log.1 beside it, the copy the first builds rotated the log into, when
// that holds such a line. A file that holds none is never touched.
var oldGuardLog = Migration{
	ID:    "0004-old-guard-log",
	Title: "Remove the old free-text lines from the guard log",
	Doing: "removing the old free-text lines from the guard log",
	What: "Removes the free-text lines earlier builds of the agent command guard wrote " +
		"(\"<time> <pid> watch: …\", or pre or post) from the guard log, watch.log in the " +
		"guard folder of Workbench's state folder, keeping every other line in place behind " +
		"a record of the cut, and " +
		"deletes watch.log.1 beside it, the copy the first builds rotated the log into, when " +
		"it holds such lines. A log without them is left alone.",
	Recovery: "nothing to undo: the removed lines are in an old format nothing reads, and " +
		"the guard's records stay",
	Check: func(l Locations) Found {
		// The log is read in the guard folder, so a folder refused there
		// is said instead (see guard.CheckedRoot).
		if _, err := guard.CheckedRoot(l.Runtime.State); err != nil {
			return Found{Status: Pending, Refusal: err}
		}
		var todo, other, unread []string
		for _, path := range guardLogPaths(l) {
			shown := display.PathFrom(l.Home, path)
			lines, err := oldGuardLines(path)
			switch {
			case err == nil && lines == 0:
			case operation.NotRegularLog(err):
				other = append(other, shown)
			case err != nil:
				unread = append(unread, operation.Unread(l.Home, path, err))
			case filepath.Ext(path) == ".1":
				todo = append(todo, "deletes "+shown)
			default:
				todo = append(todo, fmt.Sprintf("removes %d old lines from %s", lines, shown))
			}
		}
		text := todo
		if len(other) > 0 {
			text = append(text, keeps(other))
		}
		found := Found{Status: Pending, Headline: strings.Join(text, " and ")}
		switch {
		case len(unread) > 0:
			found.Refusal = errors.New(strings.Join(unread, " and "))
		case len(todo) == 0:
			found.Status = Done
		}
		return found
	},
	Run: func(_ context.Context, l Locations) ([]string, error) {
		var kept []string
		for _, path := range guardLogPaths(l) {
			var err error
			if filepath.Ext(path) == ".1" {
				var lines int
				if lines, err = oldGuardLines(path); err == nil && lines > 0 {
					err = operation.RemoveIfThere(path)
				}
			} else {
				err = operation.RewriteLog(path, withoutOldLines)
				if errors.Is(err, fs.ErrNotExist) {
					err = nil
				}
			}
			switch {
			case operation.NotRegularLog(err):
				kept = append(kept, display.PathFrom(l.Home, path))
			case err != nil:
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

// guardLogPaths are the guard log, in the guard folder where guard.Root says,
// and the copy the first builds rotated it into.
func guardLogPaths(l Locations) []string {
	log := guard.LogFile(guard.Root(l.Runtime.State))
	return []string{log, log + ".1"}
}

// withoutOldLines are the ranges of the log body that are not old free-text
// lines.
func withoutOldLines(body []byte) [][2]int {
	var keep [][2]int
	at := 0
	for line := range bytes.Lines(body) {
		end := at + len(line)
		switch {
		case oldGuardLine.Match(line):
		case len(keep) > 0 && keep[len(keep)-1][1] == at:
			keep[len(keep)-1][1] = end
		default:
			keep = append(keep, [2]int{at, end})
		}
		at = end
	}
	return keep
}

// oldGuardLines counts the old free-text lines in the log at path, read as
// [operation.LogReader] reads it: never through a link, and none when it is
// missing.
func oldGuardLines(path string) (int, error) {
	reader := operation.NewLogReader(path)
	defer reader.Close()
	data, err := reader.Read()
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
