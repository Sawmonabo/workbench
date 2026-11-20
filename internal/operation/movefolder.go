package operation

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Sawmonabo/workbench/internal/platform"
)

// MoveFolder moves the folder at from to to by swapping it with an empty
// private file, so from never stands empty: a process that still names from
// fails there instead of making a new, empty folder in its place. It makes
// the folder above to private, puts a new empty 0600 file at to (replacing a
// private plain file a stopped move left there, so the file always carries
// the time of this move), swaps the two names in one system call and makes
// both folders' entries durable. The folder then sits at to with its open
// files, locks and sockets, and the file at from until [RemoveClosedPath]
// deletes it after a restart.
//
// It never replaces a folder: with a folder already at to, or no folder at
// from (nothing to move), it does nothing and returns moved false with no
// error. Where the file system cannot swap two names it moves nothing,
// removes the file it made and returns an error matching
// platform.ErrSwapUnsupported; any other failed swap also removes that file.
// moved is true with an error only when the swap happened and a folder could
// not be synced.
func MoveFolder(from, to string) (moved bool, err error) {
	if Within(from, to) || Within(to, from) {
		return false, fmt.Errorf("cannot move %s to %s: one is inside the other", from, to)
	}
	if IsFolder(to) || !IsFolder(from) {
		return false, nil
	}
	if err := ensurePrivateDirectory(filepath.Dir(to)); err != nil {
		return false, err
	}
	if err := ReplacePrivateFile(to, nil); err != nil {
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

// IsFolder reports whether path is a folder itself: not a link to one, not a
// plain file and not missing.
func IsFolder(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

// RemoveClosedPath deletes the empty plain file a [MoveFolder] left at path
// once the system has restarted since: its modification time is before the
// boot time, so no process that named the folder there can still run. It
// does nothing for anything else, or when the boot time cannot be read. It
// unlinks, so it never removes a folder that took the file's place.
func RemoveClosedPath(path string) {
	boot, err := platform.BootTime()
	if err != nil {
		return
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != 0 ||
		!info.ModTime().Before(boot) {
		return
	}
	_ = syscall.Unlink(path)
}
