package operation

import (
	"context"
	"os"
	"path/filepath"
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
	if _, err := os.Stat(target); !os.IsNotExist(err) {
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
