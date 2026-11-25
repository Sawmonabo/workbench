package guard

// This file is what the view and `agent guard history` need of the history
// beyond its store (history.go and the files it names): where it and the
// copies of damaged ones are, the guard folder made for the command that sets
// the time kept, the time kept while none is set, how much of a command a
// summary holds, and what is said of a history that cannot be written, was
// damaged or cannot be read, which the watcher's snapshot and the view, with
// or without a watcher, say in the same words.

import (
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// HistoryKeepDefault is how long finished commands are kept while no time is
// set.
const HistoryKeepDefault = historyKeepDefault

// HistoryRoot is the guard folder where Root says, which holds the history,
// once it passes the checks every entry applies to it (see folders.check).
// It creates nothing. A check that fails because the folder moved meanwhile
// is made again, once, where the folder is now (see HistoryFolderMoved).
func HistoryRoot() (string, error) {
	return historyRoot(folders.check)
}

// MakeHistoryRoot is HistoryRoot made private when it is missing, as the
// watcher makes it before it takes its lock (see folders.makeRoot), for the
// command that sets the time kept, which makes the history as the watcher's
// writer would (see OpenHistory). At its old path it makes nothing.
func MakeHistoryRoot() (string, error) {
	return historyRoot(func(f folders) error {
		if err := f.makeRoot(); err != nil {
			return err
		}
		return f.check()
	})
}

// historyRoot is the guard folder where Root says once prepare, which checks
// or makes it, accepts it; when prepare fails because the folder moved
// meanwhile, it is the folder where Root says now, prepared once more.
func historyRoot(prepare func(folders) error) (string, error) {
	state, err := stateFolder()
	if err != nil {
		return "", err
	}
	f := locate(state, "history")
	err = prepare(f)
	if err != nil && HistoryFolderMoved(f.root) {
		f = locate(state, "history")
		err = prepare(f)
	}
	return f.root, err
}

// HistoryFolderMoved is for `agent guard history`, whose read or write in the
// guard folder root failed: it reports whether root is the folder at its old
// path and that path is no longer a folder, so the folder has moved meanwhile
// (see Move), and the command finds it again (see HistoryRoot) and does that
// work there again, once, as the hooks do (see folders.again).
func HistoryFolderMoved(root string) bool {
	state, err := stateFolder()
	return err == nil && root == oldRoot(state) && !operation.IsFolder(root)
}

// KeepText is a time kept in words, as `agent guard history` and the
// writer's log say it: whole weeks, else whole days from two days, else
// whole hours ("1 week", "3 days", "24 hours"); any other time, which only a
// settings row written by something else can hold, in Go's form.
func KeepText(keep time.Duration) string {
	const day = 24 * time.Hour
	switch {
	case keep%(7*day) == 0:
		return display.Count(int(keep/(7*day)), "week")
	case keep%day == 0 && keep >= 2*day:
		return display.Count(int(keep/day), "day")
	case keep%time.Hour == 0:
		return display.Count(int(keep/time.Hour), "hour")
	}
	return keep.String()
}

// HistoryFile is the history's path in the guard folder root.
func HistoryFile(root string) string { return historyFile(root) }

// SummaryLength is how many characters of a command a [Summary] holds.
const SummaryLength = summaryLength

// GraphBars is the most bars a command's memory graph has, running or
// finished.
const GraphBars = graphBars

// HistoryDamagedCopies are the copies the guard folder root keeps of
// histories that could not be used, the newest first, each with the time it
// was set aside (see [MoveDamaged]).
func HistoryDamagedCopies(root string) []DamagedCopy { return damagedCopies(root) }

// ShowPaths is text, which names paths in the folder root (the guard folder,
// or the state folder that holds it), as people read it: each such path named
// from the home folder, as [display.Path] names one (~/…/history.db).
// A root that is not in the home folder, or that display.Path would quote, is left
// as it is.
func ShowPaths(text, root string) string {
	if shown := display.Path(root); strings.HasPrefix(shown, "~") {
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
// and earlier at TIME; the old files are PATH and PATH", the latest damage
// and then the oldest whose copy is kept (see [MoveDamaged]), each TIME with
// its date when it is not on the day of now, each PATH named from the home
// folder.
func HistoryDamaged(copies []DamagedCopy, now time.Time) string {
	if len(copies) == 0 {
		return ""
	}
	if len(copies) == 1 {
		return "Guard history was damaged and started again at " + display.Clock(
			copies[0].At,
			now,
		) +
			"; the old file is " + display.Path(
			copies[0].Path,
		)
	}
	return "Guard history was damaged and started again at " + display.Clock(copies[0].At, now) +
		", and earlier at " + display.Clock(copies[1].At, now) + "; the old files are " +
		display.Path(copies[0].Path) + " and " + display.Path(copies[1].Path)
}

// HistoryUnreadable is what is said of a history in the guard folder root
// that cannot be read: "Guard history unreadable: REASON", where REASON is
// err with the paths in root shown as [ShowPaths] shows them.
func HistoryUnreadable(err error, root string) string {
	return "Guard history unreadable: " + ShowPaths(err.Error(), root)
}
