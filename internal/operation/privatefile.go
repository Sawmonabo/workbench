package operation

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// ReadPrivateInput shares private-file validation with state/answer consumers.
// It never creates missing state and withholds unsafe or oversized input.
func ReadPrivateInput(path string, limit int64) ([]byte, error) {
	if limit < 1 || limit > 16<<20 {
		return nil, Fail(ExitInvalid, "input", "Invalid private input bound")
	}
	if err := checkPrivateFile(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, Fail(ExitInvalid, "input", "Cannot read private input")
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, Fail(ExitInvalid, "input", "Private input exceeds its read bound")
	}
	return data, nil
}

// WritePrivate is for bounded private metadata, not target files or checkpoint
// images. It uses same-directory create/fsync/rename/fsync and mode 0600. It
// first deletes the temporary files writes that were killed left in the
// folder (see [RemoveStaleWrites]): the configuration and state folders are
// written this way by more than one lock's holder, so a file is taken for a
// killed write's only once it is over a minute old.
func (m *Mutation) WritePrivate(path string, data []byte) error {
	if err := m.Check(); err != nil {
		return err
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return Fail(
			ExitInvalid,
			"scope",
			"Private metadata targets must be canonical absolute paths",
		)
	}
	// Atomic replacement of a lock file would leave flock on the old inode.
	// Compare filesystem identities: config and state overrides may themselves
	// alias the same directory under different spellings or mount paths.
	lockDirectory, err := os.Stat(filepath.Join(m.context.Paths.State, "locks"))
	if err != nil || !lockDirectory.IsDir() {
		return Fail(
			ExitConflict,
			"lock",
			"Cannot establish the held operation lock directory; stop before metadata writes",
		)
	}
	for ancestor := path; ; ancestor = filepath.Dir(ancestor) {
		info, err := os.Stat(ancestor)
		if err == nil && os.SameFile(lockDirectory, info) {
			return Fail(
				ExitInvalid,
				"scope",
				"Operation lock files are reserved and cannot be replaced by metadata writes",
			)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Fail(ExitInvalid, "scope", "Cannot inspect private metadata target ancestry")
		}
		if ancestor == filepath.Dir(ancestor) {
			break
		}
	}
	if len(data) > 1024*1024 {
		return Fail(ExitInvalid, "state", "Private metadata exceeds its 1 MiB limit")
	}
	allowed := false
	for _, base := range []string{m.context.Paths.Config, m.context.Paths.State} {
		if path != base && Within(base, path) {
			allowed = true
			if err := ensureRoot(base); err != nil {
				return err
			}
		}
	}
	if !allowed {
		return Fail(
			ExitInvalid,
			"scope",
			"Private metadata write is outside config/state directories",
		)
	}
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	RemoveStaleWrites(filepath.Dir(path))
	return ReplacePrivateFile(path, data)
}

// ReplacePrivateFile writes data to path, in an existing private folder,
// through a same-folder temporary file: create 0600, write, fsync, rename,
// fsync the folder. An existing path must already be a private file. It is
// for Workbench's own small records written outside a [Mutation], such as the
// migration record; [Mutation.WritePrivate] ends with it.
func ReplacePrivateFile(path string, data []byte) error {
	if _, err := os.Lstat(path); err == nil {
		if err := checkPrivateFile(path); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Fail(ExitInvalid, "state", "Cannot inspect private metadata target")
	}
	return writePrivateFile(path, data, false)
}

// CreatePrivateFile is [ReplacePrivateFile] for a path that must not exist:
// it puts the temporary file in place with [RenameNoReplace], so an existing
// path fails it with an error matching fs.ErrExist and keeps its content.
// Where the filesystem lacks the no-replace flag, a crash between its link and
// unlink leaves the temporary file as a second link, which
// [RemoveStaleWrites] deletes.
func CreatePrivateFile(path string, data []byte) error {
	return writePrivateFile(path, data, true)
}

// writePrivateFile writes data through a same-folder temporary file and puts
// it in place at path, never replacing a file there when create is set.
func writePrivateFile(path string, data []byte, create bool) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	id, err := NewID()
	if err != nil {
		return err
	}
	temporary := ".write-" + id
	f, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temporary) }()
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if create {
		err = RenameNoReplace(filepath.Join(filepath.Dir(path), temporary), path)
	} else {
		err = root.Rename(temporary, filepath.Base(path))
	}
	if err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return syncDirectory(directory)
}

// RemoveStaleWrites deletes the temporary files [ReplacePrivateFile] and
// [CreatePrivateFile] left in folder when their process was killed before the
// rename finished: those starting ".write-" and over a minute old, so a write
// still in progress keeps its file. It is best effort; a folder it can't read
// has nothing it deletes.
func RemoveStaleWrites(folder string) { removeWrites(folder, time.Minute) }

// RemoveWrites is [RemoveStaleWrites] whatever the files' age, for the only
// writer of folder, which holds the lock every writer there takes: none of
// its own writes is in progress, so each such file is left by a write that
// was killed.
func RemoveWrites(folder string) { removeWrites(folder, 0) }

// removeWrites deletes the temporary files of private writes in folder that
// are over age old, every one when age is 0.
func removeWrites(folder string, age time.Duration) {
	entries, _ := os.ReadDir(folder)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".write-") {
			continue
		}
		if info, err := entry.Info(); err == nil && (age == 0 || time.Since(info.ModTime()) > age) {
			_ = os.Remove(filepath.Join(folder, entry.Name()))
		}
	}
}

// SyncDirectory makes the entries of the folder at path durable: a file
// created, renamed or removed in it. A filesystem that cannot sync a folder
// (EINVAL or EBADF, as some network and Windows mounts answer) counts as
// synced, as PostgreSQL's fsync_fname does.
func SyncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return syncDirectory(directory)
}

func syncDirectory(directory *os.File) error {
	err := directory.Sync()
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EBADF) {
		return nil
	}
	return err
}

// RemoveIfThere deletes the file or empty folder at path; a path already gone
// is not an error.
func RemoveIfThere(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// RenameNoReplace renames from to to and never replaces a file already at to.
// A file at to fails it with an error matching fs.ErrExist, or, when from and
// to are on different filesystems, possibly syscall.EXDEV instead, whichever
// the system checks first. Neither moves anything; a caller that copies on
// EXDEV puts its copy in place with RenameNoReplace too, which then fails with
// fs.ErrExist. Where the filesystem lacks the no-replace flag, it links to and
// unlinks from, which fails the same way; where links are unsupported too, it
// checks to and renames, which is safe only under the caller's own lock, and
// an error checking to stops it.
func RenameNoReplace(from, to string) error {
	err := platform.RenameNoReplace(from, to)
	if err == nil {
		return nil
	}
	if !unsupported(err) {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	if err = os.Link(from, to); err == nil {
		return os.Remove(from)
	}
	if errors.Is(err, fs.ErrExist) || errors.Is(err, syscall.EXDEV) || !unsupported(err) &&
		!errors.Is(err, syscall.EPERM) {
		return err
	}
	if _, err = os.Lstat(to); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			err = &fs.PathError{Op: "rename", Path: to, Err: fs.ErrExist}
		}
		return err
	}
	return os.Rename(from, to)
}

// unsupported reports an error a filesystem gives for a call or flag it
// does not implement.
func unsupported(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOSYS)
}

func checkPrivateFile(path string) error {
	if err := safeParents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Fail(
			ExitInvalid,
			"state",
			display.Path(path)+" must be an existing regular private file",
		)
	}
	if err := owned(path, info); err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return Fail(
			ExitInvalid,
			"permissions",
			display.Path(path)+" has other hard links; a private file must have exactly one",
		)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return Fail(
			ExitInvalid,
			"permissions",
			fmt.Sprintf(
				"%s is open to other users (mode %04o); run chmod 600 %s and retry",
				display.Path(path),
				mode,
				ShellHomePath(path),
			),
		)
	}
	return nil
}
