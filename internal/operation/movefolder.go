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
// error. Where the file system cannot swap two names it moves nothing,
// removes the file it made and returns an error matching
// platform.ErrSwapUnsupported; any other failed swap also removes that file.
// moved is true with an error only when the swap happened and a folder could
// not be synced. A start identifier that cannot be read moves nothing.
func MoveFolder(from, to string) (moved bool, err error) {
	if Within(from, to) || Within(to, from) {
		return false, fmt.Errorf("cannot move %s to %s: one is inside the other", from, to)
	}
	if IsFolder(to) || !IsFolder(from) {
		return false, nil
	}
	boot, err := platform.BootID()
	if err != nil {
		return false, err
	}
	if err := ensurePrivateDirectory(filepath.Dir(to)); err != nil {
		return false, err
	}
	if err := ReplacePrivateFile(to, []byte(boot)); err != nil {
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
			return true, fmt.Errorf("moved %s to %s but cannot sync %s: %w", from, to, folder, err)
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
