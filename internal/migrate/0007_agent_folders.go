package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/agent/costs"
	"github.com/Sawmonabo/workbench/internal/agent/guard"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// agentFolders moves the costs folders of the data and state folders and the
// guard folder of the state folder under agent/, the path of the commands
// that keep them. The moves belong to internal/agent/costs and
// internal/agent/guard, whose entries also move them by themselves, each when
// it holds the folder's lock (see [costs.MoveAgentFolders] and [guard.Move]);
// this migration looks at where each folder stands, runs those moves, and
// clears an old folder left beside one already in use.
var agentFolders = Migration{
	ID:    "0007-agent-folders",
	Title: "Move the costs and guard folders under agent/",
	Doing: "moving the costs and guard folders under agent/",
	What: "Moves costs/ in Workbench's data folder (the ledger and its price lists) and " +
		"costs/ and guard/ in its state folder (the costs import log and lock, and the agent " +
		"guard's notes, log, lock and socket) to agent/costs/ and agent/guard/ beside them. " +
		"Each folder moves whole, in one swap with a small file that stays at its old path " +
		"until the machine restarts, so a process of the release before fails there instead " +
		"of making a new, empty folder. A folder a guard watcher or a costs import is working " +
		"in stays, and that watcher, or the next import, moves it. On a file system that " +
		"can't swap two names the folders stay and are used where they are. From an old " +
		"folder beside one already in use it deletes the logs, output files, locks, sockets, " +
		"downloaded price lists and empty folders, then the folder once it is empty; anything " +
		"else, such as a ledger or a note, stays, and is named.",
	Recovery: "nothing is lost: each folder moves whole with every file in it, and the " +
		"logs, locks, sockets and price lists deleted from an old folder beside one in use " +
		"are made again",
	Check: func(l Locations) (Status, string) {
		status := Done
		var text []string
		for _, s := range inspectAgentFolders(l) {
			if s.due() {
				status = Pending
			}
			if t := s.text(l.Home); t != "" {
				text = append(text, t)
			}
		}
		return status, strings.Join(text, " and ")
	},
	Run: func(_ context.Context, l Locations) ([]string, error) {
		var errs []error
		if _, err := costs.MoveAgentFolders(l.Home, l.Runtime); err != nil {
			errs = append(errs, err)
		}
		if _, err := guard.Move(l.Runtime.State); err != nil {
			old, root, _ := guard.Folder(l.Runtime.State)
			errs = append(errs, fmt.Errorf("moving %s to %s: %w",
				operation.ShowPath(l.Home, old), operation.ShowPath(l.Home, root), err))
		}
		failed := len(errs) > 0
		var left []string
		for _, s := range inspectAgentFolders(l) {
			if s.standing == besideNew {
				if err := s.clear(); err != nil {
					errs = append(errs, err)
				}
				s = s.inspect()
			}
			switch {
			case s.standing == unread:
				errs = append(errs, s.err)
			case s.standing == movable && !failed:
				// Its lock was free a moment ago and taken when the move tried it.
				errs = append(errs, operation.Fail(
					operation.ExitBlocked,
					"busy",
					operation.ShowPath(
						l.Home,
						s.old,
					)+" didn't move, and the next apply tries again",
				))
			case s.standing != movable:
				if t := s.text(l.Home); t != "" {
					left = append(left, t)
				}
			}
		}
		return left, errors.Join(errs...)
	},
	Leaves: func(left []string) string { return strings.Join(left, " and ") },
}

// AgentFoldersLeft is doctor's line for 0007: the old folders that stay, each
// named as Check names it, "" when none does, and the line's status: pending
// while a run has something to do, conflict when an old folder keeps files
// beside the folder in use, and skipped when a watcher or an import works in
// one or its file system can't swap two names, which needs nothing done.
func AgentFoldersLeft(l Locations) (operation.Status, string) {
	status := operation.StatusSkipped
	var text []string
	for _, s := range inspectAgentFolders(l) {
		t := s.text(l.Home)
		if t == "" {
			continue
		}
		text = append(text, t)
		switch {
		case s.due():
			status = operation.StatusPending
		case s.standing == besideNew && status != operation.StatusPending:
			status = operation.StatusConflict
		}
	}
	return status, strings.Join(text, " and ")
}

// agentFolder is one folder 0007 moves: its old path, its path under agent/,
// the lock a process working in it at its old path holds ("" when none can),
// and that process, for the text.
type agentFolder struct {
	old, new, lock string
	guard          bool
}

// agentFolderPaths are the folders 0007 moves, in the order it moves them:
// the costs data folder, the costs state folder, then the guard folder.
func agentFolderPaths(l Locations) []agentFolder {
	moves, lock := costs.AgentFolders(l.Runtime)
	old, root, watchLock := guard.Folder(l.Runtime.State)
	return []agentFolder{
		{old: moves[0][0], new: moves[0][1], lock: lock},
		{old: moves[1][0], new: moves[1][1], lock: lock},
		{old: old, new: root, lock: watchLock, guard: true},
	}
}

// standing is where one old folder stands.
type standing int

const (
	moved     standing = iota // no folder at the old path
	movable                   // a run moves it
	inUse                     // a watcher or an import works in it, and moves it
	noSwap                    // its file system can't swap two names, so it stays in use
	besideNew                 // a folder is in use at the new path
	unread                    // it, or a folder in it, can't be read
)

// folderState is what [agentFolder.inspect] found. For a folder beside the
// one in use, gone lists what a run deletes from it, in the order it does,
// and stays the rest.
type folderState struct {
	agentFolder
	standing    standing
	gone, stays []string
	err         error
}

func inspectAgentFolders(l Locations) []folderState {
	var states []folderState
	for _, f := range agentFolderPaths(l) {
		states = append(states, f.inspect())
	}
	return states
}

// inspect says where the folder stands. It writes nothing; to tell a folder
// a watcher or an import works in, it tries that process's lock without
// waiting (see [operation.LockHeld]).
func (f agentFolder) inspect() folderState {
	s := folderState{agentFolder: f}
	switch {
	case !operation.IsFolder(f.old):
		s.standing = moved
	case f.lock != "" && lockHeld(f.lock):
		s.standing = inUse
	case operation.IsFolder(f.new):
		s.standing = besideNew
		if s.gone, s.stays, s.err = sortOut(f.old); s.err != nil {
			s.standing = unread
		}
	default:
		s.standing = movable
		// A file system that can't be asked is tried: the move says why it fails.
		if swaps, err := platform.CanSwap(f.old); err == nil && !swaps {
			s.standing = noSwap
		}
	}
	return s
}

// lockHeld reports whether a process holds the lock at path; one that can't
// be tried counts as free.
func lockHeld(path string) bool {
	held, err := operation.LockHeld(path)
	return err == nil && held
}

// due reports whether a run has something to do with the folder. A lock is
// no reason by itself: every guard hook makes one in an old folder it finds
// (see [guard.Move]).
func (s folderState) due() bool {
	switch s.standing {
	case movable, unread:
		return true
	case besideNew:
		return len(s.stays) == 0 || slices.ContainsFunc(s.gone, func(path string) bool {
			return filepath.Ext(path) != ".lock"
		})
	case moved, inUse, noSwap:
	}
	return false
}

// text is what Check says of the folder: what a run does with it, or why it
// stays, home-relative; "" once it has moved.
func (s folderState) text(home string) string {
	old, root := operation.ShowPath(home, s.old), operation.ShowPath(home, s.new)
	who := "an import"
	if s.guard {
		who = "a watcher"
	}
	switch s.standing {
	case movable:
		return "moves " + old + " to " + root
	case unread:
		return "can't read " + old
	case noSwap:
		return "leaves " + old + " in use where it is, since its file system can't swap two names"
	case inUse:
		switch {
		case operation.IsFolder(s.new):
			return "leaves " + old + ", beside " + root + ", while " + who + " works in it"
		case s.guard:
			return "leaves " + old + " to the watcher running in it, which moves it to " + root +
				" when it next hands over or stops"
		}
		return "leaves " + old + " to the import running in it, and the next import moves it to " + root
	case besideNew:
		return s.sorted(old, root)
	case moved:
	}
	return ""
}

// sorted is the text for an old folder beside the one in use.
func (s folderState) sorted(old, root string) string {
	var parts []string
	if s.due() {
		var names []string
		for _, path := range s.gone {
			switch {
			case path == s.old:
			case operation.IsFolder(path):
				names = append(names, "the empty folder "+s.inside(path))
			default:
				names = append(names, s.inside(path))
			}
		}
		if len(names) > 0 {
			parts = append(parts, "deletes "+strings.Join(names, ", ")+" from "+old)
		}
		if len(s.stays) == 0 {
			parts = append(parts, "removes "+old+" once it is empty")
		}
	}
	if len(s.stays) > 0 {
		kept := make([]string, len(s.stays))
		for i, path := range s.stays {
			kept[i] = s.inside(path)
		}
		parts = append(parts, "keeps "+strings.Join(kept, ", ")+" in "+old+" beside "+root+
			", which is in use, since nothing is merged into it")
	}
	return strings.Join(parts, " and ")
}

// inside is path relative to the old folder.
func (s folderState) inside(path string) string {
	if rel, err := filepath.Rel(s.old, path); err == nil {
		return rel
	}
	return path
}

// sortOut sorts what the folder at path holds into what a run deletes from an
// old folder beside the one in use and what stays. gone lists the logs and
// output files (.log, .err and their .1 copies), locks (.lock), sockets and
// downloaded price lists (rates-official.json), and each folder those leave
// empty after its files, ending with path itself when nothing stays. A link
// is never followed: it stays.
func sortOut(path string) (gone, stays []string, err error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		switch {
		case entry.IsDir():
			g, s, err := sortOut(child)
			if err != nil {
				return nil, nil, err
			}
			gone, stays = append(gone, g...), append(stays, s...)
		case entry.Type()&fs.ModeSocket != 0, entry.Type().IsRegular() && deletable(entry.Name()):
			gone = append(gone, child)
		default:
			stays = append(stays, child)
		}
	}
	if len(stays) == 0 {
		gone = append(gone, path)
	}
	return gone, stays, nil
}

// deletable reports whether a file of that name in an old folder is one a
// run deletes (see sortOut).
func deletable(name string) bool {
	switch filepath.Ext(strings.TrimSuffix(name, ".1")) {
	case ".log", ".err", ".lock":
		return true
	}
	return name == "rates-official.json"
}

// clear deletes from an old folder beside the one in use what sortOut lists
// to go, holding the lock of the process that would work in it, so none
// starts there meanwhile; when that lock is held, it deletes nothing. A folder
// something was added to meanwhile stays.
func (s folderState) clear() error {
	if s.lock != "" {
		lock, held, err := operation.TryLockIn(s.lock)
		switch {
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		case err != nil:
			return err
		case held:
			return nil
		default:
			defer func() { _ = lock.Close() }()
		}
	}
	gone, _, err := sortOut(s.old)
	if err != nil {
		return err
	}
	for _, path := range gone {
		if err := operation.RemoveIfThere(path); err != nil && !errors.Is(err, syscall.ENOTEMPTY) &&
			!errors.Is(err, syscall.EEXIST) {
			return err
		}
	}
	return nil
}
