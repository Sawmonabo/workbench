package toolconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// The apply writes a trusted_hash for each of Workbench's hooks under every
// path CodexHooksFiles names, and Codex then runs, without asking, whatever
// hooks.json at such a path holds under the same index with the same hash.
// A path that names a file Workbench did not install would let a hooks.json
// it never wrote run unreviewed, so each path must be the one file the apply
// installs, however $HOME spells it.
func TestCodexHooksFilesNameOnlyTheInstalledFile(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	other := filepath.Join(root, "other")
	for _, dir := range []string{home, other} {
		if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(dir, ".codex", "hooks.json"),
			[]byte("{}"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := os.Stat(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{other, link, home, "relative/home", ""} {
		t.Setenv("HOME", env)
		files := CodexHooksFiles(home)
		if len(files) == 0 {
			t.Fatalf("HOME=%q: no hooks.json path", env)
		}
		for _, file := range files {
			info, err := os.Stat(file)
			if err != nil || !os.SameFile(info, installed) {
				t.Errorf("HOME=%q: trusts %s, not the hooks.json the apply installs", env, file)
			}
		}
	}
}
