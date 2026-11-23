package guard

// This file is what the view and `agent guard history` need of the history
// beyond its store (history.go): where it and the copy of a damaged one are,
// the guard folder made for the command that sets the time kept, the time
// kept while none is set, how much of a command a summary holds, and what
// is said of a history that cannot be written, was damaged or cannot be
// read, which the watcher's snapshot and the view, with or without a
// watcher, say in the same words.

import (
	"os"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// HistoryKeepDefault is how long finished commands are kept while no time is
// set.
const HistoryKeepDefault = historyKeepDefault

// HistoryRoot is the guard folder where Root says, which holds the history.
// It creates nothing.
func HistoryRoot() (string, error) {
	state, err := stateFolder()
	if err != nil {
		return "", err
	}
	return Root(state), nil
}

// MakeHistoryRoot is HistoryRoot made private when it is missing, as the
// watcher makes it before it takes its lock (see folders.makeRoot), for the
// command that sets the time kept, which makes the history as the watcher's
// writer would (see OpenHistory). At its old path it makes nothing.
func MakeHistoryRoot() (string, error) {
	state, err := stateFolder()
	if err != nil {
		return "", err
	}
	f := locate(state, "history")
	return f.root, f.makeRoot()
}

// HistoryFile is the history's path in the guard folder root.
func HistoryFile(root string) string { return historyFile(root) }

// SummaryLength is how many characters of a command a [Summary] holds.
const SummaryLength = summaryLength

// HistoryDamagedFile is where the guard folder root keeps the copy of a
// history that could not be used (see [MoveDamaged]).
func HistoryDamagedFile(root string) string { return damagedFile(root) }

// ShowPaths is text, which names paths in the guard folder root, as people
// read it: each such path named from the home folder, as [operation.ShowPath]
// names one (~/…/history.db). A root that is not in the home folder, or that
// ShowPath would quote, is left as it is.
func ShowPaths(text, root string) string {
	home, _ := os.UserHomeDir()
	if shown := operation.ShowPath(home, root); strings.HasPrefix(shown, "~") {
		return strings.ReplaceAll(text, root, shown)
	}
	return text
}

// HistoryUnwritable is what is said while the watcher's writer cannot write
// the history: "Guard history cannot be written: REASON", where reason is
// why, with the paths in the guard folder shown as [ShowPaths] shows them
// (~/…/history.db: permission denied).
func HistoryUnwritable(reason string) string {
	return "Guard history cannot be written: " + reason
}

// HistoryDamaged is what is said while the guard folder keeps the copy of a
// history that could not be used, at copied, set aside at at (the copy's
// time, which [MoveDamaged] sets): "Guard history was damaged and started
// again at TIME; the old file is PATH", TIME with its date when it is not
// on the day of now, PATH named from the home folder.
func HistoryDamaged(at time.Time, copied string, now time.Time) string {
	at, now = at.Local(), now.Local()
	clock := at.Format("3:04 PM")
	if at.Format(time.DateOnly) != now.Format(time.DateOnly) {
		clock = at.Format("Jan 2 3:04 PM")
	}
	home, _ := os.UserHomeDir()
	return "Guard history was damaged and started again at " + clock + "; the old file is " +
		operation.ShowPath(home, copied)
}

// HistoryUnreadable is what is said of a history in the guard folder root
// that cannot be read: "Guard history unreadable: REASON", where REASON is
// err with the paths in root shown as [ShowPaths] shows them.
func HistoryUnreadable(err error, root string) string {
	return "Guard history unreadable: " + ShowPaths(err.Error(), root)
}
