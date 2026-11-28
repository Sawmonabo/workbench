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
	held, isHeld, err := TryLockFile(path)
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
