package operation

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// MoveFolder moves the folder at from to to by swapping it with a small
// private file, so from never stands empty: a process that still names from
// fails there instead of making a new, empty folder in its place. It makes
// the folder above to private, puts a 0600 file at to holding
// [platform.BootID] (replacing a private plain file a stopped move left
// there), swaps the two names in one system call and makes both folders'
// entries durable. The folder then sits at to with its open files, locks and
// sockets, and the file at from until [RemoveClosedPath] deletes it after a
// restart.
//
// It never replaces a folder: with a folder already at to, or no folder at
// from (nothing to move), it does nothing and returns moved false with no
// error. Where the file system cannot swap two names it moves nothing and
// returns an error matching platform.ErrSwapUnsupported: when
// [platform.CanSwap] says so it writes nothing at all, and when only the swap
// tells (an overlay file system refusing a folder of a lower layer) it
// removes the file it made. Any other failed swap also removes that file, and
// a failed move removes the folder above to when it made that folder itself
// and nothing else is in it; a move that finds that folder gone before it
// wrote in it makes it once more. moved is true with an error only when the swap
// happened and a folder could not be synced. A start identifier that cannot
// be read moves nothing.
//
// Its errors say why, naming neither from nor to, which the caller names
// once, from the home folder, as people read them ("cannot move ~/a to ~/b:
// REASON"); another path in a reason is named from the home folder too (see
// [ShowHomePath]).
func MoveFolder(from, to string) (moved bool, err error) {
	moved, err = moveFolder(from, to)
	return moved, moveReason(from, to, err)
}

// moveReason is err, of [MoveFolder] from from to to, without the two paths
// an error of the os package names around its reason, and with any other path
// it names shown from the home folder. Only an error that says no more than
// the os package's is cut so; it still matches what it matched.
func moveReason(from, to string, err error) error {
	if link, ok := errors.AsType[*os.LinkError](err); ok && link.Error() == err.Error() &&
		link.Old == from && link.New == to {
		return link.Err
	}
	if problem, ok := errors.AsType[*fs.PathError](err); ok && problem.Error() == err.Error() {
		if problem.Path == from || problem.Path == to {
			return problem.Err
		}
		return fmt.Errorf("%s: %w", ShowHomePath(problem.Path), problem.Err)
	}
	return err
}

// CanMoveFolder reports whether [MoveFolder] from from to to can move a
// folder, looking at paths and the file system only: from is a folder, to is
// not, and the file system holding from can swap two names or cannot be
// asked, since the swap then says why it fails. err matches
// platform.ErrSwapUnsupported, and can is false, when [platform.CanSwap] says
// that file system cannot. A caller that must hold a lock to move asks first,
// so a move that cannot happen never takes it; MoveFolder asks again, under
// that lock, where it decides.
func CanMoveFolder(from, to string) (can bool, err error) {
	if IsFolder(to) || !IsFolder(from) {
		return false, nil
	}
	if swaps, err := platform.CanSwap(from); err == nil && !swaps {
		return false, platform.ErrSwapUnsupported
	}
	return true, nil
}

func moveFolder(from, to string) (moved bool, err error) {
	if Within(from, to) || Within(to, from) {
		return false, errors.New("one is inside the other")
	}
	if can, err := CanMoveFolder(from, to); !can {
		return false, err
	}
	boot, err := platform.BootID()
	if err != nil {
		return false, err
	}
	parent := filepath.Dir(to)
	made := false
	defer func() {
		if made && !moved {
			// Remove fails on a folder that holds anything, so one another
			// process put a file in meanwhile stays.
			_ = os.Remove(parent)
		}
	}()
	// Another failed move removes the folder above to when it made it, which
	// can fall between making that folder here and writing in it: it is then
	// made once more.
	for try := 0; ; try++ {
		if _, err = os.Lstat(parent); errors.Is(err, fs.ErrNotExist) {
			made = true
		}
		if err = ensurePrivateDirectory(parent); err == nil {
			err = ReplacePrivateFile(to, []byte(boot))
		}
		if err == nil || try > 0 || !errors.Is(err, fs.ErrNotExist) {
			break
		}
	}
	if err != nil {
		return false, err
	}
	if err := platform.SwapNames(from, to); err != nil {
		if info, statErr := os.Lstat(to); statErr == nil && info.Mode().IsRegular() {
			_ = os.Remove(to)
		}
		return false, err
	}
	for _, folder := range []string{filepath.Dir(from), filepath.Dir(to)} {
		if err := SyncDirectory(folder); err != nil {
			return true, fmt.Errorf("it moved, but cannot sync %w", moveReason(from, to, err))
		}
	}
	return true, nil
}

// ClosePath puts the file a [MoveFolder] leaves, 0600 and holding
// [platform.BootID], at a path where nothing is, so a process that would make
// a folder there fails. closed says it made the file; whatever is there
// already stays, and so does a missing folder above path.
func ClosePath(path string) (closed bool, err error) {
	boot, err := platform.BootID()
	if err != nil {
		return false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	switch {
	case errors.Is(err, fs.ErrExist):
		return false, nil
	case err != nil:
		return false, err
	}
	if _, err = file.WriteString(boot); err != nil {
		_ = file.Close()
		_ = syscall.Unlink(path)
		return false, err
	}
	if err = file.Close(); err != nil {
		_ = syscall.Unlink(path)
		return false, err
	}
	return true, nil
}

// IsFolder reports whether path is a folder itself: not a link to one, not a
// plain file and not missing.
func IsFolder(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

// RemoveClosedPath deletes the plain file a [MoveFolder] left at path once
// the system has restarted since: it holds a start identifier other than the
// current [platform.BootID], so no process that named the folder there can
// still run. It does nothing for anything else (a folder, a missing path, a
// file holding the current identifier, an empty or unreadable file) or when
// the current identifier cannot be read. It unlinks, so it never removes a
// folder that took the file's place.
func RemoveClosedPath(path string) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > closedPathSize {
		return
	}
	boot, err := platform.BootID()
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	held := strings.TrimSpace(string(data))
	if held == "" || held == boot {
		return
	}
	_ = syscall.Unlink(path)
}

// closedPathSize bounds the file [RemoveClosedPath] reads: a start identifier
// is a UUID, so a larger file is not one a move left.
const closedPathSize = 64
