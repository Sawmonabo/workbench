package guard

import (
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/display"
)

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
