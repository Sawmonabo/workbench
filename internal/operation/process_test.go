package operation

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Prevent unauthorized system mutation if a preview accidentally reaches the
// real subprocess owner. The command would create a directory if it ran.
func TestPreviewCannotExecuteMutation(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "unauthorized")
	c := Context{ReadOnly: true, Scope: Scope{Kind: "project", Root: directory}}
	_, err := Run(context.Background(), c, nil, Process{
		Executable: "/bin/mkdir", Args: []string{target}, Directory: directory, Mutates: true,
	})
	if ExitCode(err) != 3 {
		t.Fatalf("expected blocked mutation, got %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("preview executed mutation: %v", err)
	}
}

// Prevent unauthorized execution: a marker such as ~/package.json must not make
// Workbench's own tools untrusted, but the exemption must never admit an
// executable that lives inside a real project below home (a repo's ./bin or
// node_modules/.bin), whose contents a project controls.
func TestHomeMarkerAdmitsOnlyHomeLevelToolDirectories(t *testing.T) {
	home := t.TempDir()
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	home = canonical
	t.Setenv("HOME", home)
	data := filepath.Join(home, "wbdata")
	t.Setenv("WORKBENCH_DATA_DIR", data)
	tool := func(directory string) string {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "tool")
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write := func(path string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, "package.json"))
	write(filepath.Join(home, "proj", ".git", "HEAD"))
	for _, allowed := range []string{
		tool(filepath.Join(home, ".local", "bin")),
		tool(filepath.Join(data, "tools", "uv")),
	} {
		if _, err := trustedExecutable(allowed, nil); err != nil {
			t.Fatalf("%s should be trusted despite ~/package.json: %v", allowed, err)
		}
	}
	for _, refused := range []string{
		tool(filepath.Join(home, "proj", "bin")),
		tool(filepath.Join(home, "node_modules", ".bin")),
	} {
		if _, err := trustedExecutable(refused, nil); err == nil {
			t.Fatalf("%s is project-controlled and must be refused", refused)
		}
	}
	if _, err := trustedExecutable(
		tool(filepath.Join(home, ".local", "bin")),
		[]string{home + "/.local"},
	); err == nil {
		t.Fatal("explicit excluded root must still win")
	}
}

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
