package guard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestHistoryFilesArePrivate guards against credential exposure: a command's
// text may hold a token, so no other user may read the history, nor the WAL
// and shared-memory files SQLite makes beside it, which hold the same rows.
// It checks them after the first write, while the store is open, since the
// last connection to close removes the two side files, and again once a
// history left readable by others (a copy, or a mode another tool set) is
// opened, by the view and then by the writer: SQLite makes the side files
// with the history's own mode.
func TestHistoryFilesArePrivate(t *testing.T) {
	root := t.TempDir()
	for _, write := range []bool{true, false, true} {
		history, err := OpenHistory(root, write)
		if err != nil {
			t.Fatal(err)
		}
		if write {
			ended := time.Now()
			if err := history.Insert(t.Context(), Finished{
				CommandView: CommandView{
					ID:      "toolu_private_" + ended.Format(time.RFC3339Nano),
					Agent:   "claude",
					Command: "curl -H 'Authorization: Bearer synthetic-token' https://example.invalid",
					State:   stateDone,
					Started: ended.Add(-time.Minute),
					Ended:   ended,
					Peak:    1 << 20,
				},
				Graph:     []uint64{1 << 20},
				Processes: []ProcessPeak{{PID: 1234, Name: "curl", Peak: 1 << 20}},
			}); err != nil {
				t.Fatal(err)
			}
		} else if _, _, err := history.Count(t.Context()); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"history.db", "history.db-wal", "history.db-shm"} {
			info, err := os.Stat(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			if mode := info.Mode().Perm(); mode&0o077 != 0 {
				t.Errorf("%s has mode %v; only its owner may read it", name, mode)
			}
		}
		if err := history.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(root, "history.db"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestHistoryWriterKeepsItsLockThroughCheck guards against silent loss of
// every command the watcher writes: in WAL mode a connection holds a shared
// POSIX lock on the history for as long as it is open, which tells another
// connection that closes it that it is not the last one, and closing any
// descriptor of the file in the watcher's process drops that lock. Without
// it, a program built on SQLite before 3.51.0 (Python's sqlite3, for one)
// that opens and closes the history deletes its -wal and -shm from under the
// watcher, whose later rows then reach no reader and are lost if it is
// killed. The writer checks the file once a minute (History.Check), so a
// check that opens the path drops the lock within a minute of every start.
// A copy of the test binary asks for a write lock over SQLite's shared range
// (sqlite3's os_unix.c: SHARED_FIRST, PENDING_BYTE + 2, 510 bytes), which
// another process's lock refuses: it must find the writer's.
func TestHistoryWriterKeepsItsLockThroughCheck(t *testing.T) {
	if path := os.Getenv(probesLock); path != "" {
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: sharedFirst, Len: sharedSize}
		if err := unix.FcntlFlock(file.Fd(), unix.F_GETLK, &lock); err != nil {
			t.Fatal(err)
		}
		held := map[int16]string{
			unix.F_UNLCK: "no lock",
			unix.F_RDLCK: "a read lock",
			unix.F_WRLCK: "a write lock",
		}
		fmt.Printf("lock: %s of process %d\n", held[lock.Type], lock.Pid)
		_ = file.Close()
		return
	}
	root := t.TempDir()
	history, err := OpenHistory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	if err := history.Check(); err != nil {
		t.Fatal(err)
	}
	probe := exec.Command(
		os.Args[0],
		"-test.run=^TestHistoryWriterKeepsItsLockThroughCheck$",
		"-test.v",
	)
	probe.Env = append(os.Environ(), probesLock+"="+filepath.Join(root, "history.db"))
	out, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("the probe failed: %v\n%s", err, out)
	}
	want := fmt.Sprintf("lock: a read lock of process %d", os.Getpid())
	for line := range strings.Lines(string(out)) {
		if strings.HasPrefix(line, "lock: ") {
			if got := strings.TrimSpace(line); got != want {
				t.Fatalf("after the check another process found %q on SQLite's shared range, "+
					"not the writer's (%q): the writer's process dropped its lock", got, want)
			}
			return
		}
	}
	t.Fatalf("the probe printed no lock:\n%s", out)
}

// probesLock, set in a copy of the test binary's environment to a history's
// path, makes that copy only report the lock held on SQLite's shared range of
// the file (see TestHistoryWriterKeepsItsLockThroughCheck).
const probesLock = "WORKBENCH_GUARD_TEST_PROBES_LOCK"

// sharedFirst and sharedSize are SQLite's shared byte range of a database
// file, where its unix VFS takes a shared lock with a read lock.
const (
	sharedFirst = 0x40000000 + 2
	sharedSize  = 510
)
