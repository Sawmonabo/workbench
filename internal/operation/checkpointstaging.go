package operation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// stagedName is the name of the file or link Workbench stages beside target i
// of cp before it renames it over the target: the operation its journal
// records, then the target's index. An operation that is stopped while it
// stages one leaves it there, and whatever settles or reverts that operation
// finds it by that record (removeStaged), never by a pattern a person's file
// could match.
func (cp *Checkpoint) stagedName(i int) string {
	return fmt.Sprintf(".workbench-%s-%d", cp.journal.OperationID, i)
}

// removeStagedFiles removes what each checkpoint of checkpoints, c's scope's,
// that was stopped while it wrote left beside its targets (removeStaged). Only
// a checkpoint whose journal says it is running or unknown can have left one:
// a writer that returns removes what it staged, and records another status.
func removeStagedFiles(c Context, checkpoints []*Checkpoint) error {
	for _, cp := range checkpoints {
		if cp.journal.Status != JournalRunning && cp.journal.Status != JournalUnknown {
			continue
		}
		if err := cp.removeStaged(c); err != nil {
			return err
		}
	}
	return nil
}

// removeStaged removes, beside each target of cp, the file or link the
// operation its journal records staged there under stagedName and left when it
// was stopped: one that is this user's and a regular file with one link, or,
// where the target's image is a link, a link. Anything else at that name is
// not what Workbench staged and stays, and so does each target's folder.
func (cp *Checkpoint) removeStaged(c Context) error {
	for i, change := range cp.changes {
		link := change.Before.Kind == ImageSymlink || change.After.Kind == ImageSymlink
		if err := removeStagedAt(c, filepath.Dir(change.Path), cp.stagedName(i), link); err != nil {
			return err
		}
	}
	return nil
}

// removeStagedAt removes name in folder, a folder of c's scope opened from the
// scope's root without following a link, when it is this user's and a regular
// file with one link, or a link when link is set. A folder that is not there
// holds nothing to remove.
func removeStagedAt(c Context, folder, name string, link bool) error {
	fd, err := openScopeFolder(c, folder)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var stat unix.Stat_t
	err = unix.Fstatat(fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	kind := stat.Mode & unix.S_IFMT
	staged := kind == unix.S_IFREG && stat.Nlink == 1 || link && kind == unix.S_IFLNK
	if int(stat.Uid) != os.Geteuid() || !staged {
		return nil
	}
	if err = unix.Unlinkat(fd, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	return unix.Fsync(fd)
}

// openScopeFolder opens folder, the scope's root or a folder below it, one
// name at a time without following a link, as imageParent opens a target's
// folder, but without its checks of the folder's metadata: what it opens it
// only removes a staged file from.
func openScopeFolder(c Context, folder string) (int, error) {
	relative, err := filepath.Rel(c.Scope.Root, folder)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return -1, unix.ENOENT
	}
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Open(c.Scope.Root, flags, 0)
	if err != nil || relative == "." {
		return fd, err
	}
	for part := range strings.SplitSeq(relative, string(filepath.Separator)) {
		next, openErr := unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return -1, openErr
		}
		fd = next
	}
	return fd, nil
}
