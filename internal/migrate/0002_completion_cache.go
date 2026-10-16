package migrate

import (
	"bufio"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// completionCache deletes the Tab-completion script the old startup files
// saved in ${XDG_CACHE_HOME:-~/.cache}. The startup files save it in
// Workbench's cache folder and rebuild it themselves, so it is deleted, not
// moved. Only a file whose first line is the marker they wrote counts; any
// other file of that name stays and is reported.
var completionCache = Migration{
	ID:    "0002-completion-cache",
	Title: "Delete the old shell completion cache",
	Doing: "deleting the old shell completion cache",
	What: "Deletes workbench-completion.zsh or .bash from ~/.cache (XDG_CACHE_HOME), the " +
		"Tab-completion script earlier startup files saved; the next shell saves it in " +
		"Workbench's cache folder. A file of that name Workbench didn't write stays, and " +
		"is named.",
	Recovery: "nothing to undo: the deleted file is a cache the next shell rebuilds in " +
		"Workbench's cache folder",
	Check: func(l Locations) (Status, string) {
		ours, other, unread := oldCompletions(l)
		var text []string
		if len(ours) > 0 {
			text = append(text, "deletes "+strings.Join(ours, ", "))
		}
		if len(unread) > 0 {
			text = append(text, "can't read "+strings.Join(unread, ", "))
		}
		if len(other) > 0 {
			text = append(text, keeps(other))
		}
		if len(ours) == 0 && len(unread) == 0 {
			return Done, strings.Join(text, " and ")
		}
		return Pending, strings.Join(text, " and ")
	},
	Run: func(_ context.Context, l Locations) ([]string, error) {
		var kept []string
		for _, path := range completionPaths(l) {
			marked, err := writtenByStartup(path)
			switch {
			case errors.Is(err, fs.ErrNotExist):
			case err != nil:
				return kept, err
			case marked:
				if err = operation.RemoveIfThere(path); err != nil {
					return kept, err
				}
			default:
				kept = append(kept, operation.ShowPath(l.Home, path))
			}
		}
		return kept, nil
	},
}

// completionMarker is the first line the old startup files wrote: "# " and
// the resolved path of the workbench they ran.
var completionMarker = regexp.MustCompile(`^# /.*/workbench$`)

func completionPaths(l Locations) []string {
	if l.OldCache == "" {
		return nil
	}
	return []string{
		filepath.Join(l.OldCache, "workbench-completion.zsh"),
		filepath.Join(l.OldCache, "workbench-completion.bash"),
	}
}

// oldCompletions are the old caches still there, home-relative: ours carry
// the marker, other files of those names don't, and unread can't be read, so
// they count as there.
func oldCompletions(l Locations) (ours, other, unread []string) {
	for _, path := range completionPaths(l) {
		marked, err := writtenByStartup(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			unread = append(unread, operation.ShowPath(l.Home, path))
		case marked:
			ours = append(ours, operation.ShowPath(l.Home, path))
		default:
			other = append(other, operation.ShowPath(l.Home, path))
		}
	}
	return ours, other, unread
}

// writtenByStartup reports whether path is a regular file whose first line is
// the marker. That one line is the only thing read. A path that isn't there,
// or can't be read, is the error.
func writtenByStartup(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	line, err := bufio.NewReader(io.LimitReader(file, 4096)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return completionMarker.MatchString(strings.TrimSuffix(line, "\n")), nil
}
