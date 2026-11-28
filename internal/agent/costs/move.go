package costs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// LocationsIn is where the costs files live inside Workbench's runtime
// folders: the ledger in <data>/agent/costs, the import log and lock in
// <state>/agent/costs. Until [MoveAgentFolders] has moved them there, the data
// folder and the state folder each stay in use at <data>/costs and
// <state>/costs while that is a folder and either the folder under agent/ is
// not or an import holds the lock in the old state folder; the two resolve
// apart, since a move can stop between them. It looks at paths and that lock
// only, and makes nothing.
func LocationsIn(home string, runtime operation.Paths) Paths {
	f := agentFoldersIn(runtime)
	data, state := f.newData, f.newState
	if f.inUse(f.oldData, f.newData) {
		data = f.oldData
	}
	if f.inUse(f.oldState, f.newState) {
		state = f.oldState
	}
	return Paths{
		Home:    home,
		Runtime: runtime,
		Ledger:  filepath.Join(data, "ledger.sqlite"),
		State:   state,
	}
}

// agentFolders are the costs data and state folders under agent/ and the
// paths they are at until they move there.
type agentFolders struct {
	oldData, newData, oldState, newState string
}

func agentFoldersIn(runtime operation.Paths) agentFolders {
	return agentFolders{
		oldData:  filepath.Join(runtime.Data, "costs"),
		newData:  filepath.Join(runtime.Data, "agent", "costs"),
		oldState: filepath.Join(runtime.State, "costs"),
		newState: filepath.Join(runtime.State, "agent", "costs"),
	}
}

func (f agentFolders) oldLock() string { return Paths{State: f.oldState}.lock() }

// AgentFolders are the costs folders for migration 0007: the data folder and
// then the state folder, each as its old path and its path under agent/, and
// the import lock an import working in an old one holds: the one in the old
// state folder while that is a folder; once it is not, the one in the new
// state folder while the old data folder is in use (see [LocationsIn] and
// lockToMove); else "", since no import works in an old folder. It looks at
// paths only.
func AgentFolders(runtime operation.Paths) (folders [2][2]string, lock string) {
	f := agentFoldersIn(runtime)
	folders = [2][2]string{{f.oldData, f.newData}, {f.oldState, f.newState}}
	switch {
	case operation.IsFolder(f.oldState):
		lock = f.oldLock()
	case operation.IsFolder(f.oldData) && !operation.IsFolder(f.newData):
		lock = Paths{State: f.newState}.lock()
	}
	return folders, lock
}

// inUse reports whether the folder at old is the one in use rather than the
// one at new. The lock is looked at only while both are folders, so an entry
// that resolves its paths never makes an import starting at that moment find
// the lock taken.
func (f agentFolders) inUse(old, new string) bool {
	return operation.IsFolder(old) &&
		(!operation.IsFolder(new) || operation.IsLockHeld(f.oldLock()))
}

// MoveAgentFolders moves the costs data folder from <data>/costs to
// <data>/agent/costs, then the state folder from <state>/costs to
// <state>/agent/costs, each in one swap that leaves a plain file at the old
// path (see [operation.MoveFolder]), so a process of the release before that
// still names it fails there instead of making a new, empty ledger. It first
// deletes such a file a move left before the system last started (see
// [operation.RemoveClosedPath]), and the temporary files a move that was
// killed left in the folders it writes in: the agent folders above the new
// paths and the state folder, where the old state path is closed (see
// [operation.RemoveStaleWrites]).
//
// Only the holder of the import lock moves, so no import works in a folder
// while it moves: it takes the lock in the old state folder without waiting.
// When an import holds it, nothing moves and the entries go on using the old
// folders (see [LocationsIn]). A move that stopped after the data folder
// leaves the state folder, which the next call moves under the same lock. A
// move whose new folder is already there leaves the old one where it is. It
// lets the lock go before it returns, and the caller takes the locks it needs
// anew at the new path. With neither folder able to move (see
// [operation.CanMoveFolder]) it takes no lock and writes nothing.
//
// A folder whose file system cannot swap two names stays where it is, in use
// there, and the other folder is still tried, since the data and state
// folders can be on different file systems. err then matches
// platform.ErrSwapUnsupported and holds nothing else: migration 0007 reads it
// to say the folder stays, while every costs entry ignores it (see locate).
func MoveAgentFolders(home string, runtime operation.Paths) (moved bool, err error) {
	return moveAgentFolders(home, runtime, operation.TryLockIn)
}

// MigrateMoveAgentFolders is [MoveAgentFolders] for migration 0007: a lock
// it finds held it tries again for about 100 ms before it gives up (see
// [operation.TakeLockIn]), since an entry asking whether an import runs takes
// a shared lock on a free one for an instant, and a migration that met that
// instant would otherwise stop as if an import worked in the folders. The
// costs entries never wait.
func MigrateMoveAgentFolders(home string, runtime operation.Paths) (moved bool, err error) {
	return moveAgentFolders(home, runtime, operation.TakeLockIn)
}

// moveAgentFolders is [MoveAgentFolders], taking the import lock with take.
func moveAgentFolders(
	home string,
	runtime operation.Paths,
	take func(string) (*os.File, bool, error),
) (moved bool, err error) {
	f := agentFoldersIn(runtime)
	operation.RemoveClosedPath(f.oldData)
	operation.RemoveClosedPath(f.oldState)
	for _, folder := range []string{filepath.Dir(f.newData), filepath.Dir(f.newState), runtime.State} {
		operation.RemoveStaleWrites(folder)
	}
	// The lock is taken only when a folder can move: an entry asking whether
	// an import runs would take a mover that holds it for an import.
	dataCan, dataErr := operation.CanMoveFolder(f.oldData, f.newData)
	stateCan, stateErr := operation.CanMoveFolder(f.oldState, f.newState)
	for _, err := range []error{dataErr, stateErr} {
		// A folder others can write in: neither moves, as the lock is in
		// the old state folder, and no lock is made in it.
		if err != nil && !errors.Is(err, platform.ErrSwapUnsupported) {
			return false, err
		}
	}
	if !dataCan && !stateCan {
		return false, errors.Join(dataErr, stateErr)
	}
	release, closed, ok, err := f.lockToMove(runtime, take)
	if err != nil || !ok {
		return false, err
	}
	defer release()
	var refused []error
	for i, move := range [][2]string{{f.oldData, f.newData}, {f.oldState, f.newState}} {
		done, err := operation.MoveFolder(move[0], move[1])
		moved = moved || done
		if i == 0 && closed && !done {
			f.reopenState()
		}
		switch {
		case errors.Is(err, platform.ErrSwapUnsupported):
			refused = append(refused, err)
		case err != nil:
			return moved, fmt.Errorf("cannot move %s to %s: %w",
				display.PathFrom(home, move[0]), display.PathFrom(home, move[1]), err)
		}
	}
	return moved, errors.Join(refused...)
}

// lockToMove takes with take the import lock that an import working in the
// old folders holds. ok is false when an import holds it, or when the old
// state folder moved in between. A lock that cannot be taken is named from
// the home folder in err (see [operation.FileError]).
//
// That lock is in the old state folder. With no folder there, no import of
// the release before can work in the old data folder, since an import makes
// the state folder before it opens the ledger. lockToMove then closes the old
// state path with the file a move leaves (see [operation.ClosePath]; closed
// says it made one), so none can start, and only then makes the new state
// folder and takes the import lock there, where every import of this release
// takes it while no old state folder is there. A folder an import made at the
// old path in that instant is locked like any other, and it moves, since no
// new state folder was made beside it.
func (f agentFolders) lockToMove(
	runtime operation.Paths,
	take func(string) (*os.File, bool, error),
) (release func(), closed, ok bool, err error) {
	if !operation.IsFolder(f.oldState) {
		// The state folder itself, which the closed path sits in.
		if _, err = operation.PrivateDirectory(runtime.State, ""); err != nil {
			return nil, false, false, err
		}
		if closed, err = operation.ClosePath(f.oldState); err != nil {
			return nil, false, false, err
		}
		if !operation.IsFolder(f.oldState) {
			release, ok, err = f.lockNewState(runtime, take)
			if !ok && closed {
				f.reopenState()
			}
			return release, closed && ok, ok, err
		}
	}
	file, held, err := take(f.oldLock())
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return nil, false, false, nil
	case err != nil:
		return nil, false, false, operation.FileError(f.oldLock(), err)
	case held:
		return nil, false, false, nil
	}
	return func() { _ = file.Close() }, false, true, nil
}

// lockNewState makes the new state folder and takes with take the import
// lock there, for lockToMove. ok is false when an import holds it.
func (f agentFolders) lockNewState(
	runtime operation.Paths,
	take func(string) (*os.File, bool, error),
) (release func(), ok bool, err error) {
	if _, err = operation.PrivateDirectory(
		runtime.State,
		filepath.Join("agent", "costs"),
	); err != nil {
		return nil, false, err
	}
	lock := Paths{State: f.newState}.lock()
	file, held, err := take(lock)
	if err != nil || held {
		if err != nil {
			err = operation.FileError(lock, err)
		}
		return nil, false, err
	}
	return func() { _ = file.Close() }, true, nil
}

// reopenState deletes the file lockToMove closed the old state path with,
// when the data folder did not move after all. It unlinks, so it never
// removes a folder.
func (f agentFolders) reopenState() {
	if info, err := os.Lstat(f.oldState); err == nil && info.Mode().IsRegular() {
		_ = syscall.Unlink(f.oldState)
	}
}

// locate moves the costs folders under agent/ when it may (see
// [MoveAgentFolders]) and resolves the costs paths after that. A move that
// failed leaves the folders where they are, and the paths name them there; its
// error comes back beside them. A folder whose file system cannot swap two
// names is no error here: it stays in use where it is.
func locate(home string, runtime operation.Paths) (Paths, error) {
	_, err := MoveAgentFolders(home, runtime)
	if errors.Is(err, platform.ErrSwapUnsupported) {
		err = nil
	}
	return LocationsIn(home, runtime), err
}
