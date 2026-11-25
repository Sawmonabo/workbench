package guard

// This file is the copies of histories that could not be used: where they
// are, moving one aside, and deleting them once they are older than the time
// kept (ADR 0020).

import (
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/sqlitefile"
)

// DamagedCopy is a copy of a history that could not be used, which the guard
// folder keeps for the time kept from At, when it was set aside (see
// [MoveDamaged]).
type DamagedCopy struct {
	Path string
	At   time.Time
}

// damagedCopies are the copies the guard folder root keeps, the newest first:
// history.damaged.db, then history.damaged.2.db.
func damagedCopies(root string) []DamagedCopy {
	var copies []DamagedCopy
	for _, path := range []string{damagedFile(root), olderDamagedFile(root)} {
		if info, err := os.Lstat(path); err == nil {
			copies = append(copies, DamagedCopy{Path: path, At: info.ModTime()})
		}
	}
	return copies
}

// removeDamagedBefore deletes each copy [MoveDamaged] made in the guard folder
// root once each of its files was last written before since. It is best
// effort: a copy it cannot delete stays, and the view keeps naming it.
func removeDamagedBefore(root string, since time.Time) {
	for _, copied := range []string{damagedFile(root), olderDamagedFile(root)} {
		removeCopyBefore(copied, since)
	}
}

// removeCopyBefore deletes the copy at copied with the files beside it (see
// [sqlitefile.Remove]) once each of its database and side files was last
// written before since.
func removeCopyBefore(copied string, since time.Time) {
	names := append([]string{copied}, sqlitefile.SideFiles(copied)...)
	found := false
	for _, name := range names {
		info, err := os.Lstat(name)
		if err == nil && !info.ModTime().Before(since) {
			return
		}
		found = found || err == nil
	}
	if found {
		_ = sqlitefile.Remove(copied)
	}
}

// MoveDamaged moves the history in the guard folder root, a file that cannot
// be used, aside to history.damaged.db with its -wal and -shm files (those at
// its name are its own: a writer that finds another file there deletes the
// side files of the one it opened when it closes, see [History.Close]), and
// returns that path, for the writer to start an empty history, and the time
// the copy it deleted to make room had been set aside, zero when it deleted
// none. It keeps at most two copies. A copy whose files were all last written
// before since, when the time kept began, is deleted first (see
// removeDamagedBefore). A copy at history.damaged.db younger than that moves
// to history.damaged.2.db, keeping its time, when that name is free, so two
// damages within the time kept keep both copies. When history.damaged.2.db
// holds a copy younger than since too, that copy, the oldest, stays, since it
// most likely holds the rows the history had before it was first damaged,
// which SQLite's recovery can often rebuild; the copy at history.damaged.db,
// of a history started after a damage, is deleted, with its -wal and -shm
// files, to make room for the new one. So a history that cannot be used is
// always moved aside and the history always starts again. Each move replaces
// the side files at its new name, or deletes them when the file moved has
// none, before the file itself, so no side file of another database is ever
// left beside a history, where SQLite would read it as that history's. The
// new copy's files are then marked as written now, so it is kept for the
// time kept from the moment it was set aside. The history must not be open.
func MoveDamaged(root string, since time.Time) (to string, replaced time.Time, err error) {
	from, to := historyFile(root), damagedFile(root)
	if _, err := os.Lstat(from); err != nil {
		return "", time.Time{}, err
	}
	removeDamagedBefore(root, since)
	if info, err := os.Lstat(to); err == nil {
		older := olderDamagedFile(root)
		var cleared error
		if _, err := os.Lstat(older); err == nil {
			cleared, replaced = sqlitefile.Remove(to), info.ModTime()
		} else {
			cleared = moveCopy(to, older)
		}
		// A copy deleted meanwhile has nothing to move.
		if cleared != nil && !errors.Is(cleared, fs.ErrNotExist) {
			return "", time.Time{}, cleared
		}
	}
	if err := moveCopy(from, to); err != nil {
		return "", replaced, err
	}
	// The copy is kept for the time kept from now, when it was set aside,
	// not from when it was last written (see removeDamagedBefore).
	now := time.Now()
	for _, name := range append([]string{to}, sqlitefile.SideFiles(to)...) {
		if err := os.Chtimes(name, now, now); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return to, replaced, err
		}
	}
	return to, replaced, nil
}

// moveCopy renames the database at from, with its -wal and -shm files, to to,
// each side file first, the side files at to deleted when from has none.
func moveCopy(from, to string) error {
	targets := sqlitefile.SideFiles(to)
	for i, side := range sqlitefile.SideFiles(from) {
		err := os.Rename(side, targets[i])
		if errors.Is(err, fs.ErrNotExist) {
			err = operation.RemoveIfThere(targets[i])
		}
		if err != nil {
			return err
		}
	}
	return os.Rename(from, to)
}
