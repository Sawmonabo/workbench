package guard

// This file is how the view and `agent guard history` find the history: the
// guard folder that holds it, looked for again where it is now when it moved
// meanwhile, and made for the command that sets the time kept; and the
// history the view follows from one read to the next, opened again when the
// file at its path is another one or the folder moved.

import (
	"errors"
	"io/fs"
	"os"

	"github.com/Sawmonabo/workbench/internal/operation"
)

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

// FollowedHistory is what [FollowHistory] found: History, the history to read
// now, nil while there is none to read; Path, History's path, or, with none,
// where the history is looked for, "" only when the state folder cannot be
// found; Damaged, the copies of damaged histories the guard folder keeps,
// the newest first (see [HistoryDamagedCopies]), none when the folder fails
// its checks; Other, when History is not the history held, another file or
// none, so what was read through that one goes; and Moved, when History is a
// new handle on the file held, found at another path once the guard folder
// moved, so what was read through that one stays.
type FollowedHistory struct {
	History *History
	Path    string
	Damaged []DamagedCopy
	Other   bool
	Moved   bool
}

// FollowHistory is the history a reader reads now, given held, the history
// it read last, opened by FollowHistory, nil for none: the view's handle on
// the history, which it follows at each read. It looks where the guard folder
// is now (see HistoryRoot) and compares the file at the history's path with
// held's by identity (device and inode), taken before the file is opened, so
// a file replaced in between is opened again at the next call. It gives:
//
//   - held, when the path names the file held opened, where it opened it;
//   - a new handle on that file, Moved, when the guard folder moved and the
//     path where it is now names it: a connection held makes from then on
//     would name the old path, where the file is no more; held, at its path,
//     when that handle cannot be opened, and the next call tries again;
//   - a new handle, Other, when the path names another file than held's, or
//     held is nil: the watcher's writer moves a damaged history aside and
//     starts a new one at the same path, whose seqs start at 1 again, and a
//     reader that kept the old file open would go on reading the one set
//     aside;
//   - no history, Other, and no error, while there is none yet: no file at the
//     path, or one without a history's schema (see [OpenHistory]);
//   - no history, Other, and the error, when the folder fails its checks or
//     the file cannot be looked at or opened, a file that is not a history
//     failing with [ErrHistoryUnusable].
//
// A look that fails, or finds no history, because the folder moved meanwhile
// is made again, once, where the folder is now (see HistoryFolderMoved), so a
// reader never shows an empty history for a move. held is never changed or
// closed: a find or a read of a command's parts may still run on it, and the
// reader closes it once FollowHistory gives another.
func FollowHistory(held *History) (FollowedHistory, error) {
	found, root, err := followHistory(held)
	if (err != nil || found.History == nil) && HistoryFolderMoved(root) {
		found, _, err = followHistory(held)
	}
	return found, err
}

// followHistory is FollowHistory's one look, with the guard folder it looked
// in.
func followHistory(held *History) (found FollowedHistory, root string, err error) {
	found.Other = true
	root, err = HistoryRoot()
	if root != "" {
		found.Path = HistoryFile(root)
	}
	if err != nil {
		return found, root, err
	}
	found.Damaged = HistoryDamagedCopies(root)
	file, err := os.Stat(found.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return found, root, nil
	case err != nil:
		return found, root, operation.FileError(found.Path, err)
	case held == nil || !os.SameFile(file, held.opened):
		found.History, err = openReader(root, file)
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
		return found, root, err
	}
	found.Other = false
	if found.Path == held.path {
		found.History = held
		return found, root, nil
	}
	if history, err := openReader(root, file); err == nil {
		found.History, found.Moved = history, true
	} else {
		found.History, found.Path = held, held.path
	}
	return found, root, nil
}

// openReader opens the history in the guard folder root for reading (see
// OpenHistory), noting file, the identity its path had before, as the file
// it opened. It gives nil with any error.
func openReader(root string, file os.FileInfo) (*History, error) {
	h, err := OpenHistory(root, false)
	if err != nil {
		return nil, err
	}
	h.opened = file
	return h, nil
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
