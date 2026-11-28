package operation

import (
	"os"
	"path/filepath"
	"testing"
)

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
		if _, err := trustedExecutable(allowed, nil, nil); err != nil {
			t.Fatalf("%s should be trusted despite ~/package.json: %v", allowed, err)
		}
	}
	for _, refused := range []string{
		tool(filepath.Join(home, "proj", "bin")),
		tool(filepath.Join(home, "node_modules", ".bin")),
	} {
		if _, err := trustedExecutable(refused, nil, nil); err == nil {
			t.Fatalf("%s is project-controlled and must be refused", refused)
		}
	}
	if _, err := trustedExecutable(
		tool(filepath.Join(home, ".local", "bin")),
		[]string{home + "/.local"},
		nil,
	); err == nil {
		t.Fatal("explicit excluded root must still win")
	}
}

// Prevent unauthorized execution: Workbench runs the tools in its own folders
// as this user whenever it needs them, so a program there that another user
// put there, or a link there to another user's program, would run that
// user's code as this one. Only this user's own program there may run; the
// same link outside Workbench's folders is a system program, which may.
func TestRunsFromItsFoldersOnlyYourOwnPrograms(t *testing.T) {
	data := t.TempDir()
	folder := filepath.Join(data, "tools", "tool", "1")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	yours := filepath.Join(folder, "tool")
	if err := os.WriteFile(yours, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	others := filepath.Join(folder, "other")
	if err := os.Symlink("/bin/sh", others); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(others); err != nil || ownedByUser(info) {
		t.Skip("/bin/sh is not another user's here")
	}
	if _, err := trustedExecutable(yours, nil, []string{data}); err != nil {
		t.Fatalf("your own program in Workbench's folder must run: %v", err)
	}
	if _, err := trustedExecutable(others, nil, []string{data}); err == nil {
		t.Fatal("a link in Workbench's folder to another user's program must not run")
	}
	if _, err := trustedExecutable("/bin/sh", nil, []string{data}); err != nil {
		t.Fatalf("a system program outside Workbench's folders must run: %v", err)
	}
}
