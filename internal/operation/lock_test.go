package operation

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Prevent two guard watchers from stopping commands at once: a watcher started
// for a handover acts only on the lock its parent holds and passed down. A
// descriptor opened anew on the lock file while another watcher holds it, or
// one for another file, must never count as the lock.
func TestInheritLockTakesOnlyThePassedLock(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "watch.lock")
	held, isHeld, err := TryLockIn(path)
	if err != nil || isHeld {
		t.Fatalf("lock: held %v, %v", isHeld, err)
	}
	defer func() { _ = held.Close() }()
	fd, err := syscall.Dup(int(held.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	passed := os.NewFile(uintptr(fd), path)
	defer func() { _ = passed.Close() }()
	if isHeld, err := InheritLock(passed, path); err != nil || isHeld {
		t.Fatalf("the passed lock: held %v, %v", isHeld, err)
	}
	opened, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	if isHeld, err := InheritLock(opened, path); err != nil || !isHeld {
		t.Fatalf("a descriptor opened anew while the lock is held: held %v, %v", isHeld, err)
	}
	other, err := os.Create(filepath.Join(directory, "other"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if _, err := InheritLock(other, path); err == nil {
		t.Fatal("a descriptor for another file was taken for the lock")
	}
}

// Prevent running another user's program as this one: a lock's folder that
// its group or other users can write in may hold what one of them put there,
// such as a lock file that is a link to a file Workbench would then truncate,
// or a program in place of one it runs. Taking a lock there must refuse the
// folder and make nothing in it, as every other folder of Workbench's own
// files is refused, and leave its mode as it is.
func TestTryLockRefusesAFolderOthersCanWrite(t *testing.T) {
	root := testRoot(t)
	folder := filepath.Join(root, "locks")
	folderAt(t, root, 0o700)
	folderAt(t, folder, 0o775)
	release, held, err := TryLock(root, filepath.Join(folder, "migrate.lock"))
	if err == nil {
		release()
		t.Fatalf("a lock was taken in a folder of mode 0775 (held %v)", held)
	}
	if entries, err := os.ReadDir(folder); err != nil || len(entries) != 0 {
		t.Errorf("the folder holds %v (%v), want nothing", entries, err)
	}
	assertMode(t, folder, 0o775)
}
