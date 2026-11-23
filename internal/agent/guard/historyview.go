package guard

// This file is what the view and `agent guard history` need of the history
// beyond its store (history.go): where it and the copies of damaged ones are,
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

// HistoryRoot is the guard folder where Root says, which holds the history,
// once it passes the checks every entry applies to it (see folders.check).
// It creates nothing.
func HistoryRoot() (string, error) {
	state, err := stateFolder()
	if err != nil {
		return "", err
	}
	f := locate(state, "history")
	return f.root, f.check()
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
	if err := f.makeRoot(); err != nil {
		return f.root, err
	}
	return f.root, f.check()
}

// HistoryFile is the history's path in the guard folder root.
func HistoryFile(root string) string { return historyFile(root) }

// SummaryLength is how many characters of a command a [Summary] holds.
const SummaryLength = summaryLength

// HistoryDamagedCopies are the copies the guard folder root keeps of
// histories that could not be used, the newest first, each with the time it
// was set aside (see [MoveDamaged]).
func HistoryDamagedCopies(root string) []DamagedCopy { return damagedCopies(root) }

// ShowPaths is text, which names paths in the folder root (the guard folder,
// or the state folder that holds it), as people read it: each such path named
// from the home folder, as [operation.ShowPath] names one (~/…/history.db). A
// root that is not in the home folder, or that ShowPath would quote, is left
// as it is.
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

// HistoryDamaged is what is said while the guard folder keeps copies of
// histories that could not be used, the newest first, each set aside at its
// At (the copy's time, which [MoveDamaged] sets), "" while it keeps none:
// "Guard history was damaged and started again at TIME; the old file is
// PATH", or, with two, "Guard history was damaged and started again at TIME,
// and before that at TIME; the old files are PATH and PATH", each TIME with
// its date when it is not on the day of now, each PATH named from the home
// folder.
func HistoryDamaged(copies []DamagedCopy, now time.Time) string {
	if len(copies) == 0 {
		return ""
	}
	home, _ := os.UserHomeDir()
	clock := func(at time.Time) string {
		if at.Local().Format(time.DateOnly) != now.Local().Format(time.DateOnly) {
			return at.Local().Format("Jan 2 3:04 PM")
		}
		return at.Local().Format("3:04 PM")
	}
	if len(copies) == 1 {
		return "Guard history was damaged and started again at " + clock(copies[0].At) +
			"; the old file is " + operation.ShowPath(home, copies[0].Path)
	}
	return "Guard history was damaged and started again at " + clock(copies[0].At) +
		", and before that at " + clock(copies[1].At) + "; the old files are " +
		operation.ShowPath(
			home,
			copies[0].Path,
		) + " and " + operation.ShowPath(home, copies[1].Path)
}

// HistoryUnreadable is what is said of a history in the guard folder root
// that cannot be read: "Guard history unreadable: REASON", where REASON is
// err with the paths in root shown as [ShowPaths] shows them.
func HistoryUnreadable(err error, root string) string {
	return "Guard history unreadable: " + ShowPaths(err.Error(), root)
}
