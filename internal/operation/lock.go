package operation

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/Sawmonabo/workbench/internal/display"
)

type locks struct{ files []*os.File }

// All writes currently hold an exclusive shared lock before their distinct
// machine/project lock. This also serializes overlapping parent/child scopes.
// Lock files are never unlinked: flock releases on close/process death.
func acquireLocks(c Context) (*locks, error) {
	if c.ReadOnly {
		return nil, Fail(ExitBlocked, "read_only", "Read-only operations cannot create locks")
	}
	if _, err := ReadState(c.Paths); err != nil {
		return nil, err
	}
	if err := ensureRoot(c.Paths.State); err != nil {
		return nil, err
	}
	directory := c.Paths.Locks()
	if err := ensurePrivateDirectory(directory); err != nil {
		return nil, err
	}
	result := &locks{}
	for _, name := range []string{"shared.lock", c.Scope.Kind + "-" + scopeDigest(c) + ".lock"} {
		path := filepath.Join(directory, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			result.close()
			return nil, Fail(
				ExitBlocked,
				"lock",
				"Cannot open the lock file "+FileError(path, err).Error(),
			)
		}
		result.files = append(result.files, f)
		if err := checkPrivateFile(path); err != nil {
			result.close()
			return nil, err
		}
		if held, err := flockNonBlocking(f); held || err != nil {
			result.close()
			if err != nil {
				return nil, Fail(
					ExitBlocked,
					"lock",
					"Cannot lock "+display.Path(path)+": "+err.Error(),
				)
			}
			return nil, Fail(
				ExitBlocked,
				"locked",
				"Another Workbench mutation holds the shared or selected scope lock",
				FixText("wait for it to finish"),
			)
		}
		if err := f.Truncate(0); err != nil {
			result.close()
			return nil, err
		}
		if _, err := fmt.Fprintf(
			f,
			"schema_version=1\npid=%d\nscope=%s\n",
			os.Getpid(),
			c.Scope.Kind,
		); err != nil {
			result.close()
			return nil, err
		}
	}
	if _, err := ReadState(c.Paths); err != nil {
		result.close()
		return nil, err
	}
	return result, nil
}

// flockNonBlocking takes an exclusive lock on f without waiting. held is true
// when another process has it, which is not an error.
func flockNonBlocking(f *os.File) (held bool, err error) {
	return tryFlock(f, syscall.LOCK_EX)
}

// tryFlock takes the lock how (LOCK_EX or LOCK_SH) on f without
// waiting. held is true when another process holds one it conflicts with,
// which is not an error.
func tryFlock(f *os.File, how int) (held bool, err error) {
	for {
		err = syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		switch {
		case err == nil:
			return false, nil
		case errors.Is(err, syscall.EINTR):
		case errors.Is(err, syscall.EWOULDBLOCK):
			return true, nil
		default:
			return false, err
		}
	}
}

// TryLock takes an exclusive lock on the file at path without waiting, for a
// background job that must run once at a time (the costs ingest worker). The
// file and its parent directory are created when missing (the directory 0700)
// and the file is never removed: the lock ends when release is called or the
// process dies. held is true, with no error, when another process has it.
func TryLock(path string) (release func(), held bool, err error) {
	f, held, err := tryLockFile(path)
	if held || err != nil {
		return nil, held, err
	}
	return func() { _ = f.Close() }, false, nil
}

// tryLockFile is [TryLock] giving the locked file, whose Close releases the
// lock, for a holder that hands it to a child it starts (see [InheritLock]).
func tryLockFile(path string) (f *os.File, held bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	return TryLockIn(path)
}

// TryLockIn is [TryLock] giving the locked file, whose Close releases the
// lock, for a lock file whose folder must already exist: it creates the file
// when missing but makes no folder, so a missing folder, or a plain file in
// its place, fails it.
func TryLockIn(path string) (f *os.File, held bool, err error) {
	return tryLockIn(path, syscall.LOCK_EX)
}

// lockTries and lockPause bound how long [TakeLockIn] tries a lock it finds
// held: about 100 ms in all.
const (
	lockTries = 6
	lockPause = 20 * time.Millisecond
)

// TakeLockIn is [TryLockIn] for a process that must have the lock to do its
// work: one that runs only while it holds the lock, for its life or its
// whole run (the guard watcher, the costs import worker), and migration 0007,
// which moves the folders those keep their locks in. A lock it finds held it
// tries again, lockTries times lockPause apart, before it gives up. flock(2)
// has no call that only asks, so whatever asks whether such a process runs
// takes a shared lock on a free one for an instant (see [LockHeld]), and a
// process that met that instant would otherwise give up, as if another held
// the lock, until something starts it again. One that holds the lock still
// holds it at the last try, so held then means another process runs.
func TakeLockIn(path string) (f *os.File, held bool, err error) {
	for try := 0; ; try++ {
		f, held, err = TryLockIn(path)
		if err != nil || !held || try == lockTries-1 {
			return f, held, err
		}
		time.Sleep(lockPause)
	}
}

// TryLockInShared is [TryLockIn] with a shared lock, kept until f closes,
// for a process that must keep the lock's holder from starting while it
// works, without being taken for that holder: the holder takes the lock
// exclusively, which any shared one refuses, while [LockHeld] asks with a
// shared lock, which only an exclusive one refuses. held is true while a
// process holds the lock exclusively.
func TryLockInShared(path string) (f *os.File, held bool, err error) {
	return tryLockIn(path, syscall.LOCK_SH)
}

// tryLockIn is [TryLockIn] with the lock how (LOCK_EX or LOCK_SH).
func tryLockIn(path string, how int) (f *os.File, held bool, err error) {
	f, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, false, err
	}
	held, err = tryFlock(f, how)
	if held || err != nil {
		_ = f.Close()
		return nil, held, err
	}
	return f, false, nil
}

// LockHeld reports whether another process holds the lock [TryLock] takes
// on the file at path. flock(2) has no call that only asks, so it
// takes a shared lock without waiting, for an instant, and lets it go: a
// shared lock is refused only while a process holds the exclusive one, and
// any number of processes hold shared ones at once, so two that ask at the
// same moment never take each other for the holder, and the answer comes at
// once either way. A process taking the lock exclusively in that instant is
// refused, so one that must have it tries again (see [TakeLockIn]). It opens
// the file without creating it, so a missing file, which no process can
// hold, is free and nothing is made.
func LockHeld(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	return tryFlock(f, syscall.LOCK_SH)
}

// LockKept is [LockHeld] for a process that asks whether the lock is held by
// one that holds it for its life or a whole run (see [TakeLockIn]), not by
// one that holds it for an instant, such as a mover swapping the folder that
// holds it: held is true only when the lock is held at each of lockTries
// tries lockPause apart.
func LockKept(path string) (held bool, err error) {
	for try := 0; ; try++ {
		held, err = LockHeld(path)
		if err != nil || !held || try == lockTries-1 {
			return held, err
		}
		time.Sleep(lockPause)
	}
}

// IsLockHeld is [LockHeld] for an entry that only picks a folder or a line by
// the answer: a lock that cannot be tried counts as free.
func IsLockHeld(path string) bool {
	held, err := LockHeld(path)
	return err == nil && held
}

// InheritLock takes on the lock on the file at path that a parent holding it
// passed down as f (see [TakeLockIn] and [Detached].Files). An flock lock
// belongs to the open file description, which a passed descriptor shares, and
// is held until the last descriptor on it closes, so the lock is never free
// while it passes from parent to child. f must be the file at path, and its
// description must hold the lock or get it without waiting: held is true when
// another description holds it, so a descriptor opened anew, or for another
// file, never stands for the lock. Closing f releases it once the parent has
// closed its own; nothing here unlocks, which would release the parent's too.
func InheritLock(f *os.File, path string) (held bool, err error) {
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if !os.SameFile(info, current) {
		return false, fmt.Errorf("the passed lock is not %s", path)
	}
	return flockNonBlocking(f)
}

func (l *locks) close() {
	for _, v := range slices.Backward(l.files) {
		_ = v.Close()
	}
	l.files = nil
}
